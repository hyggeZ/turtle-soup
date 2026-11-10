package room

import (
	"context"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
)

// liveHub 只推送“房间有变化”的提示；私有内容仍由带权限检查的 HTTP 快照提供。
type liveHub struct {
	mu      sync.Mutex
	viewers map[int64]map[chan struct{}]struct{}
	users   map[int64]map[int64]int
}

func newLiveHub() *liveHub {
	return &liveHub{viewers: make(map[int64]map[chan struct{}]struct{}), users: make(map[int64]map[int64]int)}
}

func (h *liveHub) join(room, user int64) chan struct{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.viewers[room] == nil {
		h.viewers[room] = make(map[chan struct{}]struct{})
		h.users[room] = make(map[int64]int)
	}
	updates := make(chan struct{}, 1)
	h.viewers[room][updates] = struct{}{}
	h.users[room][user]++
	return updates
}

func (h *liveHub) leave(room, user int64, updates chan struct{}) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.viewers[room], updates)
	h.users[room][user]--
	last := h.users[room][user] == 0
	if last {
		delete(h.users[room], user)
	}
	if len(h.viewers[room]) == 0 {
		delete(h.viewers, room)
		delete(h.users, room)
	}
	return last
}

func (h *liveHub) notify(room int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for updates := range h.viewers[room] {
		select {
		case updates <- struct{}{}:
		default:
		}
	}
}

func (h *liveHub) hasUser(room, user int64) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.users[room][user] > 0
}

// WebSocket 连接仅在登录且属于房间后建立，默认执行同源校验。
func (h *Handler) live(c *gin.Context) {
	user, ok := h.user(c)
	if !ok {
		return
	}
	id, ok := roomID(c)
	if !ok {
		return
	}
	var member bool
	err := h.db.QueryRow(c.Request.Context(), `SELECT EXISTS(SELECT 1 FROM room_members WHERE room_id=$1 AND user_id=$2)`, id, user.ID).Scan(&member)
	if err != nil {
		fail(c, err)
		return
	}
	if !member {
		c.JSON(403, gin.H{"error": "你不在房间中"})
		return
	}
	conn, err := websocket.Accept(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(1024)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	closed := conn.CloseRead(ctx)
	updates := h.liveHub.join(id, user.ID)
	defer func() {
		if h.liveHub.leave(id, user.ID, updates) {
			h.pauseIfHost(id, user.ID)
		}
	}()
	h.resumeIfHost(id, user.ID)
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-closed.Done():
			return
		case <-updates:
			writeCtx, stop := context.WithTimeout(closed, 5*time.Second)
			err = conn.Write(writeCtx, websocket.MessageText, []byte(`{"type":"changed"}`))
			stop()
			if err != nil {
				return
			}
		case <-ticker.C:
			pingCtx, stop := context.WithTimeout(closed, 5*time.Second)
			err = conn.Ping(pingCtx)
			stop()
			if err != nil {
				return
			}
		}
	}
}

// 主持人离线暂停对局；恢复时把竞答截止时间顺延离线时长。
func (h *Handler) pauseIfHost(room, user int64) {
	if h.liveHub.hasUser(room, user) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	tag, err := h.db.Exec(ctx, `UPDATE rooms SET status='paused',paused_at=NOW() WHERE id=$1 AND host_id=$2 AND status='active'`, room, user)
	if err == nil && tag.RowsAffected() > 0 {
		if h.liveHub.hasUser(room, user) {
			h.resumeIfHost(room, user)
			return
		}
		h.liveHub.notify(room)
	}
}

func (h *Handler) resumeIfHost(room, user int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	tag, err := h.db.Exec(ctx, `UPDATE rooms SET status='active',turn_deadline=turn_deadline+(NOW()-paused_at),paused_at=NULL WHERE id=$1 AND host_id=$2 AND status='paused' AND paused_at IS NOT NULL`, room, user)
	if err == nil && tag.RowsAffected() > 0 {
		h.liveHub.notify(room)
	}
}
