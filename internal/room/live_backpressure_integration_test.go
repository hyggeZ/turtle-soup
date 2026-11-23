package room

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"turtle-soup/internal/account"
)

// 握手仍走真实 TCP。开启闸门后阻塞服务器写入，直到生产逻辑实际关闭传输。
type liveWriteGate struct {
	net.Conn
	armed    atomic.Bool
	started  chan time.Time
	closed   chan struct{}
	closedAt time.Time
	once     sync.Once
}

func (g *liveWriteGate) Write(data []byte) (int, error) {
	if !g.armed.Load() {
		return g.Conn.Write(data)
	}
	select {
	case g.started <- time.Now():
	default:
	}
	<-g.closed
	return 0, net.ErrClosed
}

func (g *liveWriteGate) Close() error {
	err := g.Conn.Close()
	g.once.Do(func() {
		g.closedAt = time.Now()
		close(g.closed)
	})
	return err
}

type liveGateListener struct {
	net.Listener
	accepted chan *liveWriteGate
}

func (l *liveGateListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	gate := &liveWriteGate{Conn: conn, started: make(chan time.Time, 1), closed: make(chan struct{})}
	l.accepted <- gate
	return gate, nil
}

func readChanged(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	kind, payload, err := conn.Read(ctx)
	if err != nil || kind != websocket.MessageText || string(payload) != `{"type":"changed"}` {
		t.Fatalf("健康连接未收到更新提示：类型 %v，消息 %q，错误 %v", kind, payload, err)
	}
}

func livePending(hub *liveHub, roomID, userID int64) (int, int) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	for updates, viewer := range hub.viewers[roomID] {
		if viewer.user == userID {
			return len(updates), cap(updates)
		}
	}
	return 0, 0
}

func TestLiveBlockedWriteReleasesCapacityWithPostgres(t *testing.T) {
	f := newLiveSessionTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var player int64
	if err := f.db.QueryRow(ctx, `INSERT INTO users(username,password_hash) VALUES('slow_player','test') RETURNING id`).Scan(&player); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(ctx, `INSERT INTO room_members(room_id,user_id,role) VALUES($1,$2,'player')`, f.room, player); err != nil {
		t.Fatal(err)
	}

	handler := NewHandler(f.app, account.NewHandler(f.store, false))
	router := gin.New()
	handler.Register(router)
	server := httptest.NewUnstartedServer(router)
	listener := &liveGateListener{Listener: server.Listener, accepted: make(chan *liveWriteGate, 2)}
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	dial := func(userID int64) (*websocket.Conn, *liveWriteGate) {
		t.Helper()
		cookie := admissionCookie(t, f, userID)
		header := http.Header{"Origin": {server.URL}, "Cookie": {cookie.String()}}
		url := "ws" + strings.TrimPrefix(server.URL, "http") + fmt.Sprintf("/api/rooms/%d/ws", f.room)
		conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: header})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.CloseNow() })
		gate := <-listener.accepted
		t.Cleanup(func() { _ = gate.Close() })
		return conn, gate
	}
	healthy, _ := dial(f.host)
	_, slow := dial(player)
	readChanged(t, healthy) // 确认慢连接已激活，并取走加入房间的通知。
	slow.armed.Store(true)
	handler.NotifyRoom(f.room)
	var blockedAt time.Time
	select {
	case blockedAt = <-slow.started:
	case <-ctx.Done():
		t.Fatal("慢连接未进入真正的消息写入")
	}
	readChanged(t, healthy)

	const healthyUpdates = 4
	var slowestRead time.Duration
	for range healthyUpdates {
		start := time.Now()
		handler.NotifyRoom(f.room)
		readChanged(t, healthy)
		slowestRead = max(slowestRead, time.Since(start))
	}
	const burst = 10_000
	started := time.Now()
	for range burst {
		handler.NotifyRoom(f.room)
	}
	burstTime := time.Since(started)
	if pending, slots := livePending(handler.liveHub, f.room, player); pending != 1 || slots != 1 {
		t.Fatalf("慢连接应只保留一条待处理提示，实际 %d / %d", pending, slots)
	}
	if got := liveTotal(handler.liveHub); got != 2 {
		t.Fatalf("写入阻塞时预期两个连接，实际 %d", got)
	}

	select {
	case <-slow.closed:
	case <-time.After(8 * time.Second):
		t.Fatal("生产写入超时没有回收慢连接")
	}
	closedAfter := slow.closedAt.Sub(blockedAt)
	if closedAfter < 4*time.Second || closedAfter > 8*time.Second {
		t.Fatalf("慢连接应由 5 秒写入超时回收，实际 %s", closedAfter)
	}
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(time.Millisecond)
	defer poll.Stop()
	for liveTotal(handler.liveHub) != 1 {
		select {
		case <-deadline.C:
			t.Fatal("慢连接关闭后没有释放名额")
		case <-poll.C:
		}
	}
	if handler.liveHub.hasUser(f.room, player) || !handler.liveHub.hasUser(f.room, f.host) {
		t.Fatal("慢连接释放后在线状态错误")
	}
	releasedAfter := time.Since(blockedAt)
	handler.NotifyRoom(f.room)
	readChanged(t, healthy)
	t.Logf("慢连接写阻塞：%d 次批量通知 %s，待处理 1/1；健康连接 %d 次逐条读，最慢 %s；关闭用时 %s，名额 2→1 用时 %s",
		burst, burstTime, healthyUpdates, slowestRead, closedAfter, releasedAfter)
}
