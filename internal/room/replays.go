package room

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// 公开回看独立于招募列表；只公开摘要，具体过程仍需入场后按成员身份读取。
func (h *Handler) listReplays(c *gin.Context) {
	var before int64
	if raw := c.Query("before"); raw != "" {
		var err error
		before, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || before < 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "房间游标无效"})
			return
		}
	}
	rows, err := h.db.Query(c.Request.Context(), `SELECT r.id,r.code,r.mode,r.max_players,r.status,
		r.soup_title,r.soup_genre,r.soup_difficulty,r.soup_horror,r.answer_published,
		(SELECT count(*) FROM room_members m WHERE m.room_id=r.id AND m.role='player')
		FROM rooms r JOIN soups s ON s.id=r.soup_id
		WHERE r.public=TRUE AND r.status='finished'
		AND s.deleted_at IS NULL AND s.hidden_at IS NULL AND ($1=0 OR r.id<$1)
		ORDER BY r.id DESC LIMIT 50`, before)
	if err != nil {
		fail(c, err)
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	for rows.Next() {
		var id int64
		var code, mode, status, title, genre string
		var max, difficulty, horror int
		var answerPublished bool
		var players int64
		if err := rows.Scan(&id, &code, &mode, &max, &status, &title, &genre, &difficulty, &horror, &answerPublished, &players); err != nil {
			fail(c, err)
			return
		}
		items = append(items, gin.H{"id": id, "code": code, "mode": mode, "maxPlayers": max,
			"players": players, "status": status, "title": title, "genre": genre,
			"difficulty": difficulty, "horror": horror, "answerPublished": answerPublished})
	}
	if err := rows.Err(); err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, items)
}
