// Package admin 提供仅管理员可用的账号管理接口。
package admin

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"turtle-soup/internal/account"
)

type Handler struct {
	db   *pgxpool.Pool
	auth *account.Handler
}

func NewHandler(db *pgxpool.Pool, auth *account.Handler) *Handler {
	return &Handler{db: db, auth: auth}
}
func (h *Handler) Register(r *gin.Engine) {
	r.GET("/api/admin/users", h.list)
	r.PATCH("/api/admin/users/:id", h.update)
}
func (h *Handler) administrator(c *gin.Context) (account.User, bool) {
	u, err := h.auth.CurrentUser(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录"})
		return u, false
	}
	if !u.IsAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "无管理权限"})
		return u, false
	}
	return u, true
}
func (h *Handler) list(c *gin.Context) {
	if _, ok := h.administrator(c); !ok {
		return
	}
	q := strings.TrimSpace(c.Query("q"))
	rows, err := h.db.Query(c.Request.Context(), `SELECT id,username,is_admin,disabled,created_at FROM users WHERE username ILIKE '%' || $1 || '%' ORDER BY id DESC LIMIT 100`, q)
	if err != nil {
		c.JSON(500, gin.H{"error": "读取账号失败"})
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	for rows.Next() {
		var id int64
		var name string
		var admin, disabled bool
		var created interface{}
		if rows.Scan(&id, &name, &admin, &disabled, &created) != nil {
			c.JSON(500, gin.H{"error": "读取账号失败"})
			return
		}
		items = append(items, gin.H{"id": id, "username": name, "isAdmin": admin, "disabled": disabled, "createdAt": created})
	}
	if rows.Err() != nil {
		c.JSON(500, gin.H{"error": "读取账号失败"})
		return
	}
	c.JSON(200, items)
}
func (h *Handler) update(c *gin.Context) {
	operator, ok := h.administrator(c)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id < 1 {
		c.JSON(400, gin.H{"error": "账号 ID 无效"})
		return
	}
	if id == operator.ID {
		c.JSON(400, gin.H{"error": "不能停用自己的账号"})
		return
	}
	var input struct {
		Disabled *bool `json:"disabled"`
	}
	if c.ShouldBindJSON(&input) != nil || input.Disabled == nil {
		c.JSON(400, gin.H{"error": "请填写账号状态"})
		return
	}
	var name string
	err = h.db.QueryRow(c.Request.Context(), `UPDATE users SET disabled=$2 WHERE id=$1 AND is_admin=FALSE RETURNING username`, id, *input.Disabled).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(404, gin.H{"error": "账号不存在或不能修改管理员"})
		return
	}
	if err != nil {
		c.JSON(500, gin.H{"error": "更新账号失败"})
		return
	}
	if *input.Disabled {
		_, _ = h.db.Exec(c.Request.Context(), `DELETE FROM sessions WHERE user_id=$1`, id)
	}
	c.JSON(200, gin.H{"id": id, "username": name, "disabled": *input.Disabled})
}
