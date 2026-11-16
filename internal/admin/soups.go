package admin

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// 后台列表只返回汤面和审核状态，任何角色都不能从这里读取汤底。
func (h *Handler) listSoups(c *gin.Context) {
	operator, ok := h.operator(c)
	if !ok {
		return
	}
	status := c.DefaultQuery("status", "all")
	if status != "all" && status != "visible" && status != "hidden" && status != "deleted" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "题目状态无效"})
		return
	}
	query := strings.TrimSpace(c.Query("q"))
	if len([]rune(query)) > 50 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "搜索内容不能超过 50 字"})
		return
	}
	var before int64
	if raw := c.Query("before"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id < 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "题目游标无效"})
			return
		}
		before = id
	}

	rows, err := h.db.Query(c.Request.Context(), `
		SELECT s.id,s.title,s.surface,s.genre,s.difficulty,s.horror,
			s.created_at,s.deleted_at,s.hidden_at,u.id,u.username,u.role,
			COALESCE(m.username,'')
		FROM soups s JOIN users u ON u.id=s.author_id
		LEFT JOIN users m ON m.id=s.hidden_by
		WHERE (u.role='user' OR ($1='super_admin' AND u.role='admin'))
		AND ($2=0 OR s.id<$2)
		AND ($3='' OR s.title ILIKE '%'||$3||'%' OR u.username ILIKE '%'||$3||'%')
		AND ($4='all'
			OR $4='visible' AND s.deleted_at IS NULL AND s.hidden_at IS NULL
			OR $4='hidden' AND s.deleted_at IS NULL AND s.hidden_at IS NOT NULL
			OR $4='deleted' AND s.deleted_at IS NOT NULL)
		ORDER BY s.id DESC LIMIT 50`, operator.Role, before, query, status)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取题目失败"})
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	for rows.Next() {
		var id, authorID int64
		var title, surface, genre, author, authorRole, hiddenBy string
		var difficulty, horror int
		var created time.Time
		var deleted, hidden *time.Time
		if err := rows.Scan(&id, &title, &surface, &genre, &difficulty, &horror,
			&created, &deleted, &hidden, &authorID, &author, &authorRole, &hiddenBy); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "读取题目失败"})
			return
		}
		state := "visible"
		if hidden != nil {
			state = "hidden"
		}
		if deleted != nil {
			state = "deleted"
		}
		items = append(items, gin.H{
			"id": id, "title": title, "surface": surface, "genre": genre,
			"difficulty": difficulty, "horror": horror, "createdAt": created,
			"authorId": authorID, "author": author, "authorRole": authorRole,
			"status": state, "hiddenBy": hiddenBy,
		})
	}
	if rows.Err() != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取题目失败"})
		return
	}
	c.JSON(http.StatusOK, items)
}

func canModerate(operatorRole, authorRole string) bool {
	return authorRole == "user" || operatorRole == "super_admin" && authorRole == "admin"
}

type moderationInput struct {
	Hidden *bool `json:"hidden"`
}

// 隐藏题目会关闭该题尚未开局的房间；已开局对局继续使用创建时的快照。
func (h *Handler) moderateSoup(c *gin.Context) {
	operator, ok := h.operator(c)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "题目 ID 无效"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<10)
	var input moderationInput
	if c.ShouldBindJSON(&input) != nil || input.Hidden == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请指定题目是否隐藏"})
		return
	}
	ctx := c.Request.Context()
	for attempt := 0; attempt < 16; attempt++ {
		closed, status, err := h.moderateSoupOnce(ctx, operator.Role, operator.ID, id, *input.Hidden)
		if status == http.StatusNotFound {
			c.JSON(status, gin.H{"error": "题目不存在"})
			return
		}
		if status == http.StatusConflict {
			c.JSON(status, gin.H{"error": "作者已删除该题，后台不能恢复"})
			return
		}
		if err == nil {
			for _, roomID := range closed {
				if h.notifyRoom != nil {
					h.notifyRoom(roomID)
				}
			}
			c.JSON(http.StatusOK, gin.H{"id": id, "hidden": *input.Hidden, "closedRooms": len(closed)})
			return
		}
		var pgErr *pgconn.PgError
		if !*input.Hidden || !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "操作失败"})
			return
		}
		if attempt == 15 {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "房间状态繁忙，请稍后重试"})
			return
		}
		// 新房间可能被换题请求锁住；先释放题目锁，再短暂等待重试。
		delay := time.Duration(attempt+1) * 10 * time.Millisecond
		if delay > 100*time.Millisecond {
			delay = 100 * time.Millisecond
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (h *Handler) moderateSoupOnce(ctx context.Context, operatorRole string, operatorID, id int64, hidden bool) ([]int64, int, error) {
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback(ctx)

	// 先做只读权限检查，避免无权请求锁住他人的等待房间。
	var role string
	var deleted *time.Time
	err = tx.QueryRow(ctx, `SELECT u.role,s.deleted_at FROM soups s
		JOIN users u ON u.id=s.author_id WHERE s.id=$1`, id).Scan(&role, &deleted)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !canModerate(operatorRole, role) {
		return nil, http.StatusNotFound, nil
	}
	if err != nil {
		return nil, 0, err
	}
	if deleted != nil {
		return nil, http.StatusConflict, nil
	}
	if hidden {
		// 房间设置先锁房间再锁题目；这里沿用同一顺序避免互相等待。
		if err := lockWaitingRooms(ctx, tx, id, false); err != nil {
			return nil, 0, err
		}
	}
	// 建房与单人开局都会锁题目行，隐藏操作在此与它们串行化。
	err = tx.QueryRow(ctx, `SELECT u.role,s.deleted_at FROM soups s
		JOIN users u ON u.id=s.author_id WHERE s.id=$1 FOR UPDATE OF s`, id).
		Scan(&role, &deleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, http.StatusNotFound, nil
	}
	if err != nil {
		return nil, 0, err
	}
	if !canModerate(operatorRole, role) {
		return nil, http.StatusNotFound, nil
	}
	if deleted != nil {
		return nil, http.StatusConflict, nil
	}
	_, err = tx.Exec(ctx, `UPDATE soups SET
		hidden_at=CASE WHEN $2 THEN COALESCE(hidden_at,NOW()) ELSE NULL END,
		hidden_by=CASE WHEN $2 THEN COALESCE(hidden_by,$3) ELSE NULL END,
		updated_at=NOW() WHERE id=$1`, id, hidden, operatorID)
	if err != nil {
		return nil, 0, err
	}
	closed := make([]int64, 0)
	if hidden {
		// 首轮锁房后可能刚建出新房；此时持有题目锁，不能等待锁住新房的换题请求。
		if err := lockWaitingRooms(ctx, tx, id, true); err != nil {
			return nil, 0, err
		}
		rows, err := tx.Query(ctx, `UPDATE rooms SET status='closed',closed_at=NOW()
			WHERE soup_id=$1 AND status='waiting' RETURNING id`, id)
		if err != nil {
			return nil, 0, err
		}
		for rows.Next() {
			var roomID int64
			if err := rows.Scan(&roomID); err != nil {
				rows.Close()
				return nil, 0, err
			}
			closed = append(closed, roomID)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, 0, err
	}
	return closed, 0, nil
}

func lockWaitingRooms(ctx context.Context, tx pgx.Tx, soupID int64, nowait bool) error {
	query := `SELECT id FROM rooms WHERE soup_id=$1 AND status='waiting' ORDER BY id FOR UPDATE`
	if nowait {
		query += ` NOWAIT`
	}
	rows, err := tx.Query(ctx, query, soupID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
	}
	err = rows.Err()
	rows.Close()
	return err
}
