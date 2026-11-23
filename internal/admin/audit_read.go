package admin

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

const auditPageSize = 50

type auditEntry struct {
	ID          int64     `json:"id"`
	ActorID     int64     `json:"actorId"`
	ActorName   string    `json:"actorName"`
	Action      string    `json:"action"`
	SubjectType string    `json:"subjectType"`
	SubjectID   int64     `json:"subjectId"`
	CreatedAt   time.Time `json:"createdAt"`
}

// 操作审计仅供超级管理员查看，按主键倒序逐页读取。
func (h *Handler) listAudit(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	u, ok := h.operator(c)
	if !ok {
		return
	}
	if u.Role != "super_admin" {
		c.JSON(http.StatusForbidden, gin.H{"error": "只有超级管理员可以查看操作审计"})
		return
	}

	query := `SELECT a.id, a.actor_id, COALESCE(u.username, ''), a.action, a.subject_type, a.subject_id, a.created_at
		FROM admin_audit_logs a LEFT JOIN users u ON u.id=a.actor_id`
	args := []any{auditPageSize + 1}
	if values, present := c.Request.URL.Query()["before"]; present {
		if len(values) != 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "审计游标无效"})
			return
		}
		before, err := strconv.ParseInt(values[0], 10, 64)
		if err != nil || before < 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "审计游标无效"})
			return
		}
		query += ` WHERE a.id < $2`
		args = append(args, before)
	}
	query += ` ORDER BY a.id DESC LIMIT $1`
	rows, err := h.db.Query(c.Request.Context(), query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取操作审计失败"})
		return
	}
	defer rows.Close()

	items := make([]auditEntry, 0, auditPageSize+1)
	for rows.Next() {
		var item auditEntry
		if err := rows.Scan(&item.ID, &item.ActorID, &item.ActorName, &item.Action, &item.SubjectType, &item.SubjectID, &item.CreatedAt); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "读取操作审计失败"})
			return
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取操作审计失败"})
		return
	}
	var cursor int64
	if len(items) > auditPageSize {
		items = items[:auditPageSize]
		cursor = items[len(items)-1].ID
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "nextBefore": cursor})
}
