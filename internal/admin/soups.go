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
	tx, err := h.db.Begin(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "操作失败"})
		return
	}
	defer tx.Rollback(ctx)

	// 先做只读权限检查，避免无权请求锁住他人的等待房间。
	var role string
	var deleted *time.Time
	err = tx.QueryRow(ctx, `SELECT u.role,s.deleted_at FROM soups s
		JOIN users u ON u.id=s.author_id WHERE s.id=$1`, id).Scan(&role, &deleted)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !canModerate(operator.Role, role) {
		c.JSON(http.StatusNotFound, gin.H{"error": "题目不存在"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "操作失败"})
		return
	}
	if deleted != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "作者已删除该题，后台不能恢复"})
		return
	}
	if *input.Hidden {
		// 房间设置先锁房间再锁题目；这里沿用同一顺序避免互相等待。
		if err := lockWaitingRooms(ctx, tx, id); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "操作失败"})
			return
		}
	}
	// 建房与单人开局都会锁题目行，隐藏操作在此与它们串行化。
	err = tx.QueryRow(ctx, `SELECT u.role,s.deleted_at FROM soups s
		JOIN users u ON u.id=s.author_id WHERE s.id=$1 FOR UPDATE OF s`, id).
		Scan(&role, &deleted)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "操作失败"})
		return
	}
	if !canModerate(operator.Role, role) {
		c.JSON(http.StatusNotFound, gin.H{"error": "题目不存在"})
		return
	}
	if deleted != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "作者已删除该题，后台不能恢复"})
		return
	}
	_, err = tx.Exec(ctx, `UPDATE soups SET
		hidden_at=CASE WHEN $2 THEN COALESCE(hidden_at,NOW()) ELSE NULL END,
		hidden_by=CASE WHEN $2 THEN COALESCE(hidden_by,$3) ELSE NULL END,
		updated_at=NOW() WHERE id=$1`, id, *input.Hidden, operator.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "操作失败"})
		return
	}
	closed := make([]int64, 0)
	if *input.Hidden {
		rows, err := tx.Query(ctx, `UPDATE rooms SET status='closed',closed_at=NOW()
			WHERE soup_id=$1 AND status='waiting' RETURNING id`, id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "操作失败"})
			return
		}
		for rows.Next() {
			var roomID int64
			if err := rows.Scan(&roomID); err != nil {
				rows.Close()
				c.JSON(http.StatusInternalServerError, gin.H{"error": "操作失败"})
				return
			}
			closed = append(closed, roomID)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "操作失败"})
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "操作失败"})
		return
	}
	for _, roomID := range closed {
		if h.notifyRoom != nil {
			h.notifyRoom(roomID)
		}
	}
	c.JSON(http.StatusOK, gin.H{"id": id, "hidden": *input.Hidden, "closedRooms": len(closed)})
}

func lockWaitingRooms(ctx context.Context, tx pgx.Tx, soupID int64) error {
	rows, err := tx.Query(ctx, `SELECT id FROM rooms
		WHERE soup_id=$1 AND status='waiting' ORDER BY id FOR UPDATE`, soupID)
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
