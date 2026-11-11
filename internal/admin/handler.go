// Package admin 管理后台用户和角色，所有权限都由后端检查。
package admin

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
	"turtle-soup/internal/account"
	"turtle-soup/internal/telemetry"
)

type Handler struct {
	db      *pgxpool.Pool
	auth    *account.Handler
	monitor *telemetry.Store
}

func NewHandler(db *pgxpool.Pool, auth *account.Handler, monitor *telemetry.Store) *Handler {
	return &Handler{db: db, auth: auth, monitor: monitor}
}

func (h *Handler) Register(r *gin.Engine) {
	r.GET("/api/admin/users", h.list)
	r.POST("/api/admin/users", h.create)
	r.PATCH("/api/admin/users/:id", h.update)
	r.GET("/api/admin/telemetry", h.telemetry)
	r.GET("/api/admin/ai-metrics", h.aiMetrics)
}

// 日志统计只对后台账号开放，返回路由与耗时，不暴露请求体。
func (h *Handler) telemetry(c *gin.Context) {
	if _, ok := h.operator(c); !ok {
		return
	}
	c.JSON(http.StatusOK, h.monitor.Snapshot())
}

func (h *Handler) operator(c *gin.Context) (account.User, bool) {
	u, err := h.auth.CurrentUser(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录后台"})
		return u, false
	}
	if !u.IsAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "无管理权限"})
		return u, false
	}
	return u, true
}

// 管理员可以查看普通用户；超级管理员还可以查看管理员。
func (h *Handler) list(c *gin.Context) {
	u, ok := h.operator(c)
	if !ok {
		return
	}
	q := strings.TrimSpace(c.Query("q"))
	if len([]rune(q)) > 50 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "搜索内容不能超过 50 字"})
		return
	}
	var before int64
	if raw := c.Query("before"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "账号游标无效"})
			return
		}
		before = parsed
	}
	rows, err := h.db.Query(c.Request.Context(), `
		SELECT id,username,role,disabled,created_at FROM users
		WHERE username ILIKE '%' || $1 || '%'
		AND (role='user' OR $2='super_admin')
		AND ($3=0 OR id<$3)
		ORDER BY id DESC LIMIT 100`, q, u.Role, before)
	if err != nil {
		c.JSON(500, gin.H{"error": "读取账号失败"})
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	for rows.Next() {
		var id int64
		var name, role string
		var disabled bool
		var created time.Time
		if err = rows.Scan(&id, &name, &role, &disabled, &created); err != nil {
			break
		}
		items = append(items, gin.H{"id": id, "username": name, "role": role, "disabled": disabled, "createdAt": created})
	}
	if err != nil || rows.Err() != nil {
		c.JSON(500, gin.H{"error": "读取账号失败"})
		return
	}
	c.JSON(200, items)
}

type createInput struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

// 创建普通账号对管理员开放；创建管理员仅超级管理员可用。
func (h *Handler) create(c *gin.Context) {
	u, ok := h.operator(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4<<10)
	var in createInput
	if c.ShouldBindJSON(&in) != nil {
		c.JSON(400, gin.H{"error": "请求内容无效"})
		return
	}
	in.Username = strings.ToLower(strings.TrimSpace(in.Username))
	if !account.ValidUsername(in.Username) || len(in.Password) < 8 || len([]byte(in.Password)) > 72 || (in.Role != "user" && in.Role != "admin") {
		c.JSON(400, gin.H{"error": "用户名、密码或角色无效"})
		return
	}
	if in.Role == "admin" && u.Role != "super_admin" {
		c.JSON(403, gin.H{"error": "只有超级管理员能创建管理员"})
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(500, gin.H{"error": "创建账号失败"})
		return
	}
	var id int64
	err = h.db.QueryRow(c.Request.Context(), `INSERT INTO users(username,password_hash,role,is_admin) VALUES($1,$2,$3,$3='admin') RETURNING id`, in.Username, string(hash), in.Role).Scan(&id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		c.JSON(409, gin.H{"error": "用户名已存在"})
		return
	}
	if err != nil {
		c.JSON(500, gin.H{"error": "创建账号失败"})
		return
	}
	c.JSON(201, gin.H{"id": id, "username": in.Username, "role": in.Role})
}

type updateInput struct {
	Disabled *bool  `json:"disabled"`
	Password string `json:"password"`
}

// 只允许管理低于自己级别的账号，修改密码或停用后立即撤销该账号会话。
func (h *Handler) update(c *gin.Context) {
	u, ok := h.operator(c)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id < 1 {
		c.JSON(400, gin.H{"error": "账号 ID 无效"})
		return
	}
	if id == u.ID {
		c.JSON(400, gin.H{"error": "不能修改自己的后台账号"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4<<10)
	var in updateInput
	if c.ShouldBindJSON(&in) != nil || (in.Disabled == nil && in.Password == "") {
		c.JSON(400, gin.H{"error": "请填写账号状态或新密码"})
		return
	}
	if in.Password != "" && (len(in.Password) < 8 || len([]byte(in.Password)) > 72) {
		c.JSON(400, gin.H{"error": "新密码需为 8–72 字节"})
		return
	}
	tx, err := h.db.Begin(c.Request.Context())
	if err != nil {
		c.JSON(500, gin.H{"error": "修改失败"})
		return
	}
	defer tx.Rollback(c.Request.Context())
	var role string
	err = tx.QueryRow(c.Request.Context(), `SELECT role FROM users WHERE id=$1 FOR UPDATE`, id).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(404, gin.H{"error": "账号不存在"})
		return
	}
	if err != nil {
		c.JSON(500, gin.H{"error": "修改失败"})
		return
	}
	if role == "super_admin" || (role == "admin" && u.Role != "super_admin") {
		c.JSON(403, gin.H{"error": "不能管理同级或更高级的账号"})
		return
	}
	if in.Disabled != nil {
		_, err = tx.Exec(c.Request.Context(), `UPDATE users SET disabled=$2 WHERE id=$1`, id, *in.Disabled)
		if err != nil {
			c.JSON(500, gin.H{"error": "修改失败"})
			return
		}
	}
	if in.Password != "" {
		hash, hashErr := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
		if hashErr != nil {
			c.JSON(500, gin.H{"error": "修改失败"})
			return
		}
		_, err = tx.Exec(c.Request.Context(), `UPDATE users SET password_hash=$2 WHERE id=$1`, id, string(hash))
		if err != nil {
			c.JSON(500, gin.H{"error": "修改失败"})
			return
		}
	}
	_, err = tx.Exec(c.Request.Context(), `DELETE FROM sessions WHERE user_id=$1`, id)
	if err != nil {
		c.JSON(500, gin.H{"error": "修改失败"})
		return
	}
	if err = tx.Commit(c.Request.Context()); err != nil {
		c.JSON(500, gin.H{"error": "修改失败"})
		return
	}
	c.JSON(200, gin.H{"id": id, "role": role})
}
