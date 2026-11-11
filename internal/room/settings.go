package room

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

type settingsInput struct {
	SoupID     int64  `json:"soupId"`
	Mode       string `json:"mode"`
	MaxPlayers int    `json:"maxPlayers"`
	Public     bool   `json:"public"`
}

// 房主只能在等待期改设置；换题时重新计算谁有权查看新汤底。
func (h *Handler) settings(c *gin.Context) {
	user, ok := h.user(c)
	if !ok {
		return
	}
	id, ok := roomID(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2<<10)
	var input settingsInput
	if c.ShouldBindJSON(&input) != nil || input.SoupID < 1 ||
		(input.Mode != "casual" && input.Mode != "competitive") ||
		input.MaxPlayers < 2 || input.MaxPlayers > 20 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请检查房间设置"})
		return
	}

	ctx := c.Request.Context()
	tx, err := h.db.Begin(ctx)
	if err != nil {
		fail(c, err)
		return
	}
	defer tx.Rollback(ctx)
	var owner int64
	var status string
	err = tx.QueryRow(ctx, `SELECT owner_id,status FROM rooms WHERE id=$1 FOR UPDATE`, id).Scan(&owner, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(c, err)
		return
	}
	if err != nil {
		fail(c, err)
		return
	}
	if owner != user.ID || status != "waiting" {
		c.JSON(http.StatusForbidden, gin.H{"error": "只有房主能在开始前修改设置"})
		return
	}

	var author int64
	var title, surface, answer, genre string
	var difficulty, horror int
	err = tx.QueryRow(ctx, `SELECT author_id,title,surface,answer,genre,difficulty,horror FROM soups WHERE id=$1 AND deleted_at IS NULL AND hidden_at IS NULL FOR UPDATE`, input.SoupID).
		Scan(&author, &title, &surface, &answer, &genre, &difficulty, &horror)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "题目不存在"})
		return
	}
	if err != nil {
		fail(c, err)
		return
	}
	var players int
	err = tx.QueryRow(ctx, `SELECT count(*) FROM room_members WHERE room_id=$1 AND role='player'`, id).Scan(&players)
	if err != nil {
		fail(c, err)
		return
	}
	if players > input.MaxPlayers {
		c.JSON(http.StatusConflict, gin.H{"error": "人数上限低于当前玩家数"})
		return
	}
	var ineligible bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM room_members m WHERE m.room_id=$1 AND m.role='player'
		AND (m.user_id=$2 OR EXISTS(SELECT 1 FROM soup_reveals v WHERE v.soup_id=$3 AND v.user_id=m.user_id)
		OR EXISTS(SELECT 1 FROM ai_games g WHERE g.soup_id=$3 AND g.user_id=m.user_id AND g.status='active'))
	)`, id, author, input.SoupID).Scan(&ineligible)
	if err != nil {
		fail(c, err)
		return
	}
	if ineligible {
		c.JSON(http.StatusConflict, gin.H{"error": "有玩家已知道新题目的汤底"})
		return
	}
	var playingElsewhere bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM room_members here
		JOIN room_members other ON other.user_id=here.user_id AND other.role='player'
		JOIN rooms r ON r.id=other.room_id
		WHERE here.room_id=$1 AND r.id<>$1 AND r.soup_id=$2
		AND r.status IN ('waiting','active','paused')
		AND (here.role='host' OR here.user_id=$3 OR EXISTS(
			SELECT 1 FROM soup_reveals v WHERE v.soup_id=$2 AND v.user_id=here.user_id))
	)`, id, input.SoupID, author).Scan(&playingElsewhere)
	if err != nil {
		fail(c, err)
		return
	}
	if playingElsewhere {
		c.JSON(http.StatusConflict, gin.H{"error": "有成员仍在其他同题房间推理"})
		return
	}
	var aiMember bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM room_members m JOIN ai_games g ON g.user_id=m.user_id
		WHERE m.room_id=$1 AND g.soup_id=$2 AND g.status='active'
	)`, id, input.SoupID).Scan(&aiMember)
	if err != nil {
		fail(c, err)
		return
	}
	if aiMember {
		c.JSON(http.StatusConflict, gin.H{"error": "有成员正在进行新题目的单人对局"})
		return
	}
	_, err = tx.Exec(ctx, `UPDATE rooms SET soup_id=$2,mode=$3,max_players=$4,public=$5,
		soup_title=$6,soup_surface=$7,soup_answer=$8,soup_genre=$9,soup_difficulty=$10,soup_horror=$11
		WHERE id=$1`, id, input.SoupID, input.Mode, input.MaxPlayers, input.Public,
		title, surface, answer, genre, difficulty, horror)
	if err != nil {
		fail(c, err)
		return
	}
	_, err = tx.Exec(ctx, `UPDATE room_members m SET seen_answer=(m.role='host' OR m.user_id=$2 OR
		EXISTS(SELECT 1 FROM soup_reveals v WHERE v.soup_id=$3 AND v.user_id=m.user_id)) WHERE m.room_id=$1`, id, author, input.SoupID)
	if err != nil {
		fail(c, err)
		return
	}
	_, err = tx.Exec(ctx, `INSERT INTO soup_reveals(soup_id,user_id)
		SELECT $1,user_id FROM room_members WHERE room_id=$2 AND role='host' ON CONFLICT DO NOTHING`, input.SoupID, id)
	if err != nil {
		fail(c, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		fail(c, err)
		return
	}
	h.liveHub.notify(id)
	c.Status(http.StatusNoContent)
}
