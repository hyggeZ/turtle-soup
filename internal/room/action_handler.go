package room

import (
	"github.com/gin-gonic/gin"
	"strings"
	"time"
)

type actionInput struct {
	Type        string `json:"type"`
	Body        string `json:"body"`
	Result      string `json:"result"`
	EventID     int64  `json:"eventId"`
	UserID      int64  `json:"userId"`
	OperationID string `json:"operationId"`
}

func (h *Handler) action(c *gin.Context) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	id, ok := roomID(c)
	if !ok {
		return
	}
	var in actionInput
	if c.ShouldBindJSON(&in) != nil || len(in.OperationID) < 8 || len(in.OperationID) > 100 {
		c.JSON(400, gin.H{"error": "请求内容无效"})
		return
	}
	in.Body = strings.TrimSpace(in.Body)
	tx, e := h.db.Begin(c.Request.Context())
	if e != nil {
		fail(c, e)
		return
	}
	defer tx.Rollback(c.Request.Context())
	var owner, host, soupID int64
	var mode, status, role string
	var turn *int64
	var deadline *time.Time
	var max int
	e = tx.QueryRow(c.Request.Context(), `SELECT owner_id,host_id,soup_id,mode,status,turn_user_id,turn_deadline,max_players FROM rooms WHERE id=$1 FOR UPDATE`, id).Scan(&owner, &host, &soupID, &mode, &status, &turn, &deadline, &max)
	if e != nil {
		fail(c, e)
		return
	}
	e = tx.QueryRow(c.Request.Context(), `SELECT role FROM room_members WHERE room_id=$1 AND user_id=$2`, id, u.ID).Scan(&role)
	if e != nil {
		c.JSON(403, gin.H{"error": "你不在房间中"})
		return
	}
	tag, e := tx.Exec(c.Request.Context(), `INSERT INTO game_operations(room_id,user_id,operation_id) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, id, u.ID, in.OperationID)
	if e != nil {
		fail(c, e)
		return
	}
	if tag.RowsAffected() == 0 {
		c.JSON(200, gin.H{"ok": true, "duplicate": true})
		return
	}
	message, err := perform(c.Request.Context(), tx, id, u.ID, owner, host, soupID, max, mode, status, role, turn, deadline, in)
	if err != nil {
		c.JSON(409, gin.H{"error": err.Error()})
		return
	}
	if e = tx.Commit(c.Request.Context()); e != nil {
		fail(c, e)
		return
	}
	c.JSON(200, gin.H{"ok": true, "message": message})
}
