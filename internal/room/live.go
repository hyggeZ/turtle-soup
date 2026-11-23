package room

import (
	"context"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// 正常停机时记录暂停时刻；主持人重连后按这段停机时间顺延竞答回合。
func PauseActive(ctx context.Context, db *pgxpool.Pool, at time.Time) error {
	_, err := db.Exec(ctx, `UPDATE rooms SET status='paused',paused_at=$1 WHERE status='active'`, at)
	return err
}

// 异常退出没有停机时刻可用。启动时暂停遗留对局，并给竞答回合完整的 60 秒。
func RecoverActive(ctx context.Context, db *pgxpool.Pool) error {
	_, err := db.Exec(ctx, `UPDATE rooms SET status='paused',paused_at=NOW(),
		turn_deadline=CASE WHEN mode='competitive' AND turn_deadline IS NOT NULL
			THEN GREATEST(turn_deadline,NOW()+INTERVAL '60 seconds') ELSE turn_deadline END
		WHERE status='active'`)
	return err
}

// WebSocket 连接仅在登录且属于房间后建立，默认执行同源校验。
func (h *Handler) live(c *gin.Context) {
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	h.liveWithTicks(c, ticker.C)
}

// 将心跳时钟传入连接循环，测试可立即触发心跳而不必等待 25 秒。
func (h *Handler) liveWithTicks(c *gin.Context, ticks <-chan time.Time) {
	user, ok := h.user(c)
	if !ok {
		return
	}
	id, ok := roomID(c)
	if !ok {
		return
	}
	updates, evict, ok := h.admitLive(c, id, user.ID)
	if !ok {
		return
	}
	conn, err := websocket.Accept(c.Writer, c.Request, nil)
	if err != nil {
		h.liveHub.leave(id, updates)
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(1024)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	activated, first := h.liveHub.activate(id, updates, cancel)
	if !activated {
		return
	}
	closed := conn.CloseRead(ctx)
	defer func() {
		if h.liveHub.leave(id, updates) {
			h.pauseIfHost(id, user.ID)
			h.liveHub.notify(id)
		}
	}()
	if h.resumeIfHost(id, user.ID) {
		h.liveHub.notify(id)
	} else if first {
		// 新连接已经会在打开时读取快照，只提醒其他连接。
		h.liveHub.notifyExcept(id, updates)
	}
	for {
		select {
		case <-closed.Done():
			return
		case <-evict:
			return
		case <-updates:
			writeCtx, stop := context.WithTimeout(closed, 5*time.Second)
			err = conn.Write(writeCtx, websocket.MessageText, []byte(`{"type":"changed"}`))
			stop()
			if err != nil {
				return
			}
		case <-ticks:
			// 退出、改密或停用后的旧连接最多保留一个心跳周期；查询失败时也关闭连接。
			checkCtx, checkCancel := context.WithTimeout(closed, 3*time.Second)
			current, checkErr := h.auth.SessionUser(checkCtx, c.Request)
			checkCancel()
			if checkErr != nil || current.ID != user.ID {
				return
			}
			pingCtx, pingCancel := context.WithTimeout(closed, 5*time.Second)
			err = conn.Ping(pingCtx)
			pingCancel()
			if err != nil {
				return
			}
		}
	}
}

// 在房间共享锁内读取最新成员身份并预占名额，提交后才升级连接。
func (h *Handler) admitLive(c *gin.Context, id, user int64) (chan struct{}, chan struct{}, bool) {
	ctx := c.Request.Context()
	// 成员读取与名额预占共用房间共享锁；换主持不能夹在两者之间。
	tx, err := h.db.Begin(ctx)
	if err != nil {
		fail(c, err)
		return nil, nil, false
	}
	rollback := func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = tx.Rollback(releaseCtx)
	}
	defer rollback()
	var role string
	var hostID *int64
	var status string
	err = tx.QueryRow(ctx, `SELECT host_id,status FROM rooms WHERE id=$1 FOR SHARE`, id).Scan(&hostID, &status)
	if err == pgx.ErrNoRows {
		rollback()
		c.JSON(http.StatusForbidden, gin.H{"error": "你不在房间中"})
		return nil, nil, false
	}
	if err != nil {
		rollback()
		fail(c, err)
		return nil, nil, false
	}
	err = tx.QueryRow(ctx, `SELECT role FROM room_members WHERE room_id=$1 AND user_id=$2`, id, user).Scan(&role)
	if err == pgx.ErrNoRows {
		rollback()
		c.JSON(http.StatusForbidden, gin.H{"error": "你不在房间中"})
		return nil, nil, false
	}
	if err != nil {
		rollback()
		fail(c, err)
		return nil, nil, false
	}
	if status == "closed" {
		rollback()
		c.JSON(http.StatusConflict, gin.H{"error": "房间已关闭"})
		return nil, nil, false
	}
	currentHost := int64(0)
	if hostID != nil {
		currentHost = *hostID
	}
	if h.liveHub.changeHost(id, currentHost) {
		h.liveHub.notify(id)
	}
	kind := livePlayer
	if currentHost == user {
		kind = liveHost
	} else if role == "spectator" {
		kind = liveSpectator
	}
	// 升级前预留名额，超限时才能返回普通 HTTP 状态码。
	updates, evict, limit := h.liveHub.join(id, user, kind)
	if limit != liveAllowed {
		rollback()
		if limit == liveProcessLimit {
			c.Header("Retry-After", "30")
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "实时服务繁忙，请稍后重试", "code": "LIVE_CAPACITY_EXHAUSTED"})
			return nil, nil, false
		}
		c.Header("Retry-After", "15")
		if limit == liveRoomLimit {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "房间实时连接已满，请稍后重试", "code": "ROOM_LIVE_FULL"})
			return nil, nil, false
		}
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "实时连接数过多，请关闭多余页面"})
		return nil, nil, false
	}
	if err := tx.Commit(ctx); err != nil {
		h.liveHub.leave(id, updates)
		rollback()
		fail(c, err)
		return nil, nil, false
	}
	return updates, evict, true
}

// 主持人离线暂停对局；恢复时把竞答截止时间顺延离线时长。
func (h *Handler) pauseIfHost(room, user int64) {
	if h.liveHub.hasUser(room, user) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	tag, err := h.db.Exec(ctx, `UPDATE rooms SET status='paused',paused_at=NOW() WHERE id=$1 AND host_id=$2 AND status='active'`, room, user)
	if err == nil && tag.RowsAffected() > 0 && h.liveHub.hasUser(room, user) {
		h.resumeIfHost(room, user)
	}
}

func (h *Handler) resumeIfHost(room, user int64) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	tag, err := h.db.Exec(ctx, `UPDATE rooms SET status='active',turn_deadline=turn_deadline+(NOW()-paused_at),paused_at=NULL WHERE id=$1 AND host_id=$2 AND status='paused' AND paused_at IS NOT NULL`, room, user)
	return err == nil && tag.RowsAffected() > 0
}
