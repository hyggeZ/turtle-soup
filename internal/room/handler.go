// Package room 管理真人主持房间和对局。
package room

import (
	"crypto/rand"
	"encoding/hex"
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
	db      *pgxpool.Pool
	auth    *account.Handler
	liveHub *liveHub
	codes   *codeLimiter
}

func NewHandler(db *pgxpool.Pool, auth *account.Handler) *Handler {
	return &Handler{db: db, auth: auth, liveHub: newLiveHub(), codes: newCodeLimiter()}
}
func (h *Handler) Register(r *gin.Engine) {
	r.GET("/api/rooms", h.list)
	r.POST("/api/rooms", h.create)
	r.POST("/api/rooms/join", h.join)
	r.GET("/api/rooms/:id", h.get)
	r.PATCH("/api/rooms/:id/settings", h.settings)
	r.GET("/api/rooms/:id/ws", h.live)
	r.POST("/api/rooms/:id/actions", h.action)
}
func (h *Handler) user(c *gin.Context) (account.User, bool) {
	u, err := h.auth.CurrentUser(c)
	if err != nil {
		if errors.Is(err, account.ErrNotFound) {
			c.JSON(401, gin.H{"error": "请先登录"})
		} else {
			c.JSON(500, gin.H{"error": "读取登录状态失败"})
		}
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
	HostSelf   *bool  `json:"hostSelf"`
}

func (h *Handler) create(c *gin.Context) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	var in createInput
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2<<10)
	if c.ShouldBindJSON(&in) != nil || in.SoupID < 1 || (in.Mode != "casual" && in.Mode != "competitive") || in.MaxPlayers < 2 || in.MaxPlayers > 20 {
		c.JSON(400, gin.H{"error": "请检查房间设置"})
		return
	}
	// 16 字节随机码用于私密房间，避免仅凭猜测加入。
	bytes := make([]byte, 16)
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
	var title, surface, answer, genre string
	var authorID int64
	var difficulty, horror int
	e = tx.QueryRow(c.Request.Context(), `SELECT author_id,title,surface,answer,genre,difficulty,horror FROM soups WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, in.SoupID).Scan(&authorID, &title, &surface, &answer, &genre, &difficulty, &horror)
	if errors.Is(e, pgx.ErrNoRows) {
		c.JSON(404, gin.H{"error": "题目不存在"})
		return
	}
	if e != nil {
		fail(c, e)
		return
	}
	var aiPlaying bool
	e = tx.QueryRow(c.Request.Context(), `SELECT EXISTS(SELECT 1 FROM ai_games WHERE soup_id=$1 AND user_id=$2 AND status='active')`, in.SoupID, u.ID).Scan(&aiPlaying)
	if e != nil {
		fail(c, e)
		return
	}
	if aiPlaying {
		c.JSON(409, gin.H{"error": "正在进行同题单人对局"})
		return
	}
	hostSelf := in.HostSelf == nil || *in.HostSelf
	if hostSelf {
		playing, checkErr := playingElsewhere(c.Request.Context(), tx, in.SoupID, u.ID, 0)
		if checkErr != nil {
			fail(c, checkErr)
			return
		}
		if playing {
			c.JSON(http.StatusConflict, gin.H{"error": "仍在其他同题房间推理，不能主持"})
			return
		}
	}
	if !hostSelf {
		var revealed bool
		e = tx.QueryRow(c.Request.Context(), `SELECT EXISTS(SELECT 1 FROM soup_reveals WHERE soup_id=$1 AND user_id=$2)`, in.SoupID, u.ID).Scan(&revealed)
		if e != nil {
			fail(c, e)
			return
		}
		if authorID == u.ID || revealed {
			c.JSON(409, gin.H{"error": "已经知道汤底，不能作为本题玩家"})
			return
		}
	}
	var hostID *int64
	role := "player"
	if hostSelf {
		hostID = &u.ID
		role = "host"
	}
	var id int64
	e = tx.QueryRow(c.Request.Context(), `INSERT INTO rooms(code,owner_id,host_id,soup_id,mode,max_players,public,soup_title,soup_surface,soup_answer,soup_genre,soup_difficulty,soup_horror) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) RETURNING id`, code, u.ID, hostID, in.SoupID, in.Mode, in.MaxPlayers, in.Public, title, surface, answer, genre, difficulty, horror).Scan(&id)
	if e == nil {
		_, e = tx.Exec(c.Request.Context(), `INSERT INTO room_members(room_id,user_id,role,seen_answer) VALUES($1,$2,$3,$4)`, id, u.ID, role, hostSelf)
	}
	if e == nil && hostSelf {
		_, e = tx.Exec(c.Request.Context(), `INSERT INTO soup_reveals(soup_id,user_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, in.SoupID, u.ID)
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
	rows, e := h.db.Query(c.Request.Context(), `SELECT r.id,r.code,r.mode,r.max_players,r.status,r.soup_title,r.soup_genre,r.soup_difficulty,r.soup_horror,(SELECT count(*) FROM room_members m WHERE m.room_id=r.id AND m.role='player') FROM rooms r WHERE r.public=TRUE AND r.status IN ('waiting','active') ORDER BY r.id DESC LIMIT 50`)
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
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<10)
	if c.ShouldBindJSON(&in) != nil {
		c.JSON(400, gin.H{"error": "请输入房间码"})
		return
	}
	code := strings.ToUpper(strings.TrimSpace(in.Code))
	if !h.codes.allow(c.ClientIP(), u.ID) {
		// 已加入的成员不受试码限额影响，网络断开后仍可返回房间。
		var existing int64
		err := h.db.QueryRow(c.Request.Context(), `SELECT r.id FROM rooms r JOIN room_members m ON m.room_id=r.id WHERE r.code=$1 AND m.user_id=$2`, code, u.ID).Scan(&existing)
		if err == nil {
			c.JSON(200, gin.H{"id": existing})
			return
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			fail(c, err)
			return
		}
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "尝试加入次数过多，请稍后再试"})
		return
	}
	if !validRoomCode(code) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "房间码无效"})
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
	e = tx.QueryRow(c.Request.Context(), `SELECT id,soup_id,status,mode,max_players FROM rooms WHERE code=$1 FOR UPDATE`, code).Scan(&id, &soupID, &status, &mode, &max)
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
	e = tx.QueryRow(c.Request.Context(), `SELECT author_id FROM soups WHERE id=$1 FOR UPDATE`, soupID).Scan(&authorID)
	if e != nil {
		fail(c, e)
		return
	}
	role := "player"
	if (status == "active" || status == "paused") && mode == "competitive" {
		role = "spectator"
	}
	var revealed, aiPlaying bool
	e = tx.QueryRow(c.Request.Context(), `SELECT EXISTS(SELECT 1 FROM soup_reveals WHERE soup_id=$1 AND user_id=$2),
		EXISTS(SELECT 1 FROM ai_games WHERE soup_id=$1 AND user_id=$2 AND status='active')`, soupID, u.ID).Scan(&revealed, &aiPlaying)
	if e != nil {
		fail(c, e)
		return
	}
	if aiPlaying {
		c.JSON(409, gin.H{"error": "正在进行同题单人对局"})
		return
	}
	if authorID == u.ID || revealed {
		role = "spectator"
	}
	if role == "player" {
		var count int
		e = tx.QueryRow(c.Request.Context(), `SELECT count(*) FROM room_members WHERE room_id=$1 AND role='player'`, id).Scan(&count)
		if e != nil {
			fail(c, e)
			return
		}
		if count >= max {
			role = "spectator"
		}
	}
	_, e = tx.Exec(c.Request.Context(), `INSERT INTO room_members(room_id,user_id,role,seen_answer) VALUES($1,$2,$3,$4)`, id, u.ID, role, authorID == u.ID || revealed)
	if e != nil {
		fail(c, e)
		return
	}
	if e = tx.Commit(c.Request.Context()); e != nil {
		fail(c, e)
		return
	}
	h.liveHub.notify(id)
	c.JSON(200, gin.H{"id": id})
}

// 兼容已有的 8 位房间码；新建房间统一使用 32 位十六进制码。
func validRoomCode(code string) bool {
	if len(code) != 8 && len(code) != 32 {
		return false
	}
	for _, char := range code {
		if char < '0' || char > '9' {
			if char < 'A' || char > 'F' {
				return false
			}
		}
	}
	return true
}
