// Package room 管理真人主持房间和对局。
package room

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
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
	r.GET("/api/rooms", h.list)
	r.POST("/api/rooms", h.create)
	r.POST("/api/rooms/join", h.join)
	r.GET("/api/rooms/:id", h.get)
	r.POST("/api/rooms/:id/actions", h.action)
}
func (h *Handler) user(c *gin.Context) (account.User, bool) {
	u, err := h.auth.CurrentUser(c)
	if err != nil {
		c.JSON(401, gin.H{"error": "请先登录"})
		return u, false
	}
	return u, true
}
func roomID(c *gin.Context) (int64, bool) {
	id, e := strconv.ParseInt(c.Param("id"), 10, 64)
	if e != nil || id < 1 {
		c.JSON(400, gin.H{"error": "房间 ID 无效"})
		return 0, false
	}
	return id, true
}
func fail(c *gin.Context, e error) {
	if errors.Is(e, pgx.ErrNoRows) {
		c.JSON(404, gin.H{"error": "房间不存在"})
	} else {
		c.JSON(500, gin.H{"error": "操作失败"})
	}
}

type createInput struct {
	SoupID     int64  `json:"soupId"`
	Mode       string `json:"mode"`
	MaxPlayers int    `json:"maxPlayers"`
	Public     bool   `json:"public"`
}

func (h *Handler) create(c *gin.Context) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	var in createInput
	if c.ShouldBindJSON(&in) != nil || in.SoupID < 1 || (in.Mode != "casual" && in.Mode != "competitive") || in.MaxPlayers < 2 || in.MaxPlayers > 20 {
		c.JSON(400, gin.H{"error": "请检查房间设置"})
		return
	}
	var exists bool
	if e := h.db.QueryRow(c.Request.Context(), `SELECT EXISTS(SELECT 1 FROM soups WHERE id=$1)`, in.SoupID).Scan(&exists); e != nil || !exists {
		c.JSON(404, gin.H{"error": "题目不存在"})
		return
	}
	bytes := make([]byte, 4)
	if _, e := rand.Read(bytes); e != nil {
		fail(c, e)
		return
	}
	code := strings.ToUpper(hex.EncodeToString(bytes))
	tx, e := h.db.Begin(c.Request.Context())
	if e != nil {
		fail(c, e)
		return
	}
	defer tx.Rollback(c.Request.Context())
	var id int64
	e = tx.QueryRow(c.Request.Context(), `INSERT INTO rooms(code,owner_id,host_id,soup_id,mode,max_players,public) VALUES($1,$2,$2,$3,$4,$5,$6) RETURNING id`, code, u.ID, in.SoupID, in.Mode, in.MaxPlayers, in.Public).Scan(&id)
	if e == nil {
		_, e = tx.Exec(c.Request.Context(), `INSERT INTO room_members(room_id,user_id,role,seen_answer) VALUES($1,$2,'host',TRUE)`, id, u.ID)
	}
	if e != nil {
		fail(c, e)
		return
	}
	if e = tx.Commit(c.Request.Context()); e != nil {
		fail(c, e)
		return
	}
	c.JSON(201, gin.H{"id": id, "code": code})
}
func (h *Handler) list(c *gin.Context) {
	rows, e := h.db.Query(c.Request.Context(), `SELECT r.id,r.code,r.mode,r.max_players,r.status,s.title,s.genre,s.difficulty,s.horror,(SELECT count(*) FROM room_members m WHERE m.room_id=r.id AND m.role='player') FROM rooms r JOIN soups s ON s.id=r.soup_id WHERE r.public=TRUE AND r.status IN ('waiting','active') ORDER BY r.id DESC LIMIT 50`)
	if e != nil {
		fail(c, e)
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0)
	for rows.Next() {
		var id int64
		var code, mode, status, title, genre string
		var max, difficulty, horror int
		var players int64
		if e = rows.Scan(&id, &code, &mode, &max, &status, &title, &genre, &difficulty, &horror, &players); e != nil {
			fail(c, e)
			return
		}
		items = append(items, gin.H{"id": id, "code": code, "mode": mode, "maxPlayers": max, "players": players, "status": status, "title": title, "genre": genre, "difficulty": difficulty, "horror": horror})
	}
	if rows.Err() != nil {
		fail(c, rows.Err())
		return
	}
	c.JSON(200, items)
}
func (h *Handler) join(c *gin.Context) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	var in struct {
		Code string `json:"code"`
	}
	if c.ShouldBindJSON(&in) != nil {
		c.JSON(400, gin.H{"error": "请输入房间码"})
		return
	}
	tx, e := h.db.Begin(c.Request.Context())
	if e != nil {
		fail(c, e)
		return
	}
	defer tx.Rollback(c.Request.Context())
	var id, soupID int64
	var status, mode string
	var max int
	e = tx.QueryRow(c.Request.Context(), `SELECT id,soup_id,status,mode,max_players FROM rooms WHERE code=$1 FOR UPDATE`, strings.ToUpper(strings.TrimSpace(in.Code))).Scan(&id, &soupID, &status, &mode, &max)
	if e != nil {
		fail(c, e)
		return
	}
	var member bool
	_ = tx.QueryRow(c.Request.Context(), `SELECT EXISTS(SELECT 1 FROM room_members WHERE room_id=$1 AND user_id=$2)`, id, u.ID).Scan(&member)
	if member {
		c.JSON(200, gin.H{"id": id})
		return
	}
	if status == "finished" {
		c.JSON(409, gin.H{"error": "对局已结束"})
		return
	}
	var authorID int64
	e = tx.QueryRow(c.Request.Context(), `SELECT author_id FROM soups WHERE id=$1`, soupID).Scan(&authorID)
	if e != nil {
		fail(c, e)
		return
	}
	role := "player"
	if status == "active" && mode == "competitive" {
		role = "spectator"
	}
	if authorID == u.ID {
		role = "spectator"
	}
	if role == "player" {
		var count int
		_ = tx.QueryRow(c.Request.Context(), `SELECT count(*) FROM room_members WHERE room_id=$1 AND role='player'`, id).Scan(&count)
		if count >= max {
			c.JSON(409, gin.H{"error": "房间已满"})
			return
		}
	}
	_, e = tx.Exec(c.Request.Context(), `INSERT INTO room_members(room_id,user_id,role,seen_answer) VALUES($1,$2,$3,$4)`, id, u.ID, role, authorID == u.ID)
	if e != nil {
		fail(c, e)
		return
	}
	if e = tx.Commit(c.Request.Context()); e != nil {
		fail(c, e)
		return
	}
	c.JSON(200, gin.H{"id": id})
}
