package room

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
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
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<10)
	var in actionInput
	if c.ShouldBindJSON(&in) != nil || len(in.OperationID) < 8 || len(in.OperationID) > 100 {
		c.JSON(400, gin.H{"error": "请求内容无效"})
		return
	}
	in.Body = strings.TrimSpace(in.Body)
	payload, _ := json.Marshal(in)
	hash := sha256.Sum256(payload)
	tx, e := h.db.Begin(c.Request.Context())
	if e != nil {
		fail(c, e)
		return
	}
	defer tx.Rollback(c.Request.Context())
	var owner, soupID int64
	var host *int64
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
	tag, e := tx.Exec(c.Request.Context(), `INSERT INTO game_operations(room_id,user_id,operation_id,request_hash) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, id, u.ID, in.OperationID, hash[:])
	if e != nil {
		fail(c, e)
		return
	}
	if tag.RowsAffected() == 0 {
		var savedHash []byte
		var message *string
		e = tx.QueryRow(c.Request.Context(), `SELECT request_hash,message FROM game_operations WHERE room_id=$1 AND user_id=$2 AND operation_id=$3`, id, u.ID, in.OperationID).Scan(&savedHash, &message)
		if e != nil {
			fail(c, e)
			return
		}
		if len(savedHash) != 0 && !bytes.Equal(savedHash, hash[:]) {
			c.JSON(409, gin.H{"error": "同一操作 ID 不能用于不同内容"})
			return
		}
		result := gin.H{"ok": true, "duplicate": true}
		if message != nil {
			result["message"] = *message
		}
		c.JSON(200, result)
		return
	}
	hostID := int64(0)
	if host != nil {
		hostID = *host
	}
	message, err := perform(c.Request.Context(), tx, id, u.ID, owner, hostID, soupID, max, mode, status, role, turn, deadline, in)
	if err != nil {
		c.JSON(409, gin.H{"error": err.Error()})
		return
	}
	if _, e = tx.Exec(c.Request.Context(), `UPDATE game_operations SET message=$4 WHERE room_id=$1 AND user_id=$2 AND operation_id=$3`, id, u.ID, in.OperationID, message); e != nil {
		fail(c, e)
		return
	}
	if e = tx.Commit(c.Request.Context()); e != nil {
		fail(c, e)
		return
	}
	h.liveHub.notify(id)
	c.JSON(200, gin.H{"ok": true, "message": message})
}
