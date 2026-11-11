package room

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

// 历史房间只列出本人加入过的已结束或已关闭房间，不返回房间码与汤底。
func (h *Handler) listMineHistory(c *gin.Context) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	var before int64
	if raw, exists := c.GetQuery("before"); exists {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id < 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "房间游标无效"})
			return
		}
		before = id
	}
	rows, err := h.db.Query(c.Request.Context(), `SELECT r.id,r.mode,r.status,r.max_players,r.public,
		r.soup_title,r.soup_genre,r.soup_difficulty,r.soup_horror,m.role,
		(SELECT count(*) FROM room_members player WHERE player.room_id=r.id AND player.role='player')
		FROM room_members m JOIN rooms r ON r.id=m.room_id
		WHERE m.user_id=$1 AND r.status IN ('finished','closed') AND ($2=0 OR r.id<$2)
		ORDER BY r.id DESC LIMIT 50`, u.ID, before)
	if err != nil {
		fail(c, err)
		return
	}
	defer rows.Close()
	items, err := scanRoomSummaries(rows)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, items)
}

// 两个“我的房间”接口共用相同概要格式，避免历史列表意外带出私有字段。
func scanRoomSummaries(rows pgx.Rows) ([]gin.H, error) {
	items := make([]gin.H, 0)
	for rows.Next() {
		var id int64
		var mode, status, title, genre, role string
		var max, difficulty, horror int
		var players int64
		var public bool
		if err := rows.Scan(&id, &mode, &status, &max, &public, &title, &genre, &difficulty, &horror, &role, &players); err != nil {
			return nil, err
		}
		items = append(items, gin.H{"id": id, "mode": mode, "status": status, "maxPlayers": max,
			"public": public, "title": title, "genre": genre, "difficulty": difficulty,
			"horror": horror, "myRole": role, "players": players})
	}
	return items, rows.Err()
}
