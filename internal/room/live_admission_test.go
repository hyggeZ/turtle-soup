package room

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"turtle-soup/internal/account"
)

func liveTotal(hub *liveHub) int {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	return hub.processTotal
}

func admissionCookie(t *testing.T, f *liveSessionTest, user int64) *http.Cookie {
	t.Helper()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(token))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := f.db.Exec(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at)
		VALUES($1,$2,NOW()+INTERVAL '1 hour')`, hash[:], user); err != nil {
		t.Fatal(err)
	}
	f.ticks.Store(token, make(chan time.Time))
	t.Cleanup(func() { f.ticks.Delete(token) })
	return &http.Cookie{Name: "turtle_session", Value: token}
}

func admissionRequest(t *testing.T, f *liveSessionTest, cookie *http.Cookie) *http.Response {
	t.Helper()
	url := fmt.Sprintf("%s/api/rooms/%d/ws", f.server.URL, f.room)
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(cookie)
	response, err := f.server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	return response
}

func TestLiveAdmissionRejectsClosedAndUpgradeFailure(t *testing.T) {
	f := newLiveSessionTest(t)
	cookie := admissionCookie(t, f, f.host)
	// 普通 HTTP 请求可走到升级失败路径，预占的名额必须释放。
	response := admissionRequest(t, f, cookie)
	if response.StatusCode != http.StatusUpgradeRequired || liveTotal(f.hub) != 0 || f.hub.hasUser(f.room, f.host) {
		t.Fatalf("升级失败后名额未释放：HTTP %d，连接 %d", response.StatusCode, liveTotal(f.hub))
	}
	if status := f.status(); status != "active" {
		t.Fatalf("未建立的主持连接触发了暂停：%s", status)
	}
	if _, err := f.db.Exec(context.Background(), `UPDATE rooms SET status='closed',closed_at=NOW() WHERE id=$1`, f.room); err != nil {
		t.Fatal(err)
	}
	response = admissionRequest(t, f, cookie)
	if response.StatusCode != http.StatusConflict || liveTotal(f.hub) != 0 {
		t.Fatalf("关闭房间仍能占用实时名额：HTTP %d，连接 %d", response.StatusCode, liveTotal(f.hub))
	}
}

func TestLiveAdmissionRoomLimitResponse(t *testing.T) {
	f := newLiveSessionTest(t)
	var spectator int64
	if err := f.db.QueryRow(context.Background(), `INSERT INTO users(username,password_hash)
		VALUES('spectator','test') RETURNING id`).Scan(&spectator); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(context.Background(), `INSERT INTO room_members(room_id,user_id,role)
		VALUES($1,$2,'spectator')`, f.room, spectator); err != nil {
		t.Fatal(err)
	}
	cookie := admissionCookie(t, f, spectator)
	for i := 0; i < maxLiveRoomSpectators; i++ {
		reserveLive(t, f.hub, f.room, int64(10000+i), liveSpectator)
	}
	response := admissionRequest(t, f, cookie)
	if response.StatusCode != http.StatusTooManyRequests || response.Header.Get("Retry-After") != "15" {
		t.Fatalf("房间旁观名额拒绝错误：HTTP %d，Retry-After=%q", response.StatusCode, response.Header.Get("Retry-After"))
	}
	var body bytes.Buffer
	if _, err := body.ReadFrom(response.Body); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body.Bytes(), []byte(`"ROOM_LIVE_FULL"`)) || liveTotal(f.hub) != maxLiveRoomSpectators {
		t.Fatalf("拒绝响应或连接计数异常：%s，%d", body.String(), liveTotal(f.hub))
	}
	// 旁观席满后主持仍可建立连接。
	if _, _, limit := f.hub.join(f.room, f.host, liveHost); limit != liveAllowed {
		t.Fatalf("旁观席占满后主持名额丢失：%d", limit)
	}
}

func TestLiveAdmissionProcessLimitResponse(t *testing.T) {
	f := newLiveSessionTest(t)
	var spectator int64
	if err := f.db.QueryRow(context.Background(), `INSERT INTO users(username,password_hash)
		VALUES('spectator','test') RETURNING id`).Scan(&spectator); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(context.Background(), `INSERT INTO room_members(room_id,user_id,role)
		VALUES($1,$2,'spectator')`, f.room, spectator); err != nil {
		t.Fatal(err)
	}
	cookie := admissionCookie(t, f, spectator)
	for i := 0; i < maxLiveProcessSpectators; i++ {
		room := int64(1000 + i/maxLiveRoomSpectators)
		reserveLive(t, f.hub, room, int64(10000+i), liveSpectator)
	}
	response := admissionRequest(t, f, cookie)
	if response.StatusCode != http.StatusServiceUnavailable || response.Header.Get("Retry-After") != "30" {
		t.Fatalf("进程容量拒绝错误：HTTP %d，Retry-After=%q", response.StatusCode, response.Header.Get("Retry-After"))
	}
	var body bytes.Buffer
	if _, err := body.ReadFrom(response.Body); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body.Bytes(), []byte(`"LIVE_CAPACITY_EXHAUSTED"`)) || liveTotal(f.hub) != maxLiveProcessSpectators {
		t.Fatalf("进程拒绝响应或计数错误：%s，%d", body.String(), liveTotal(f.hub))
	}
}

func TestPendingHostCannotStartWithReservedSocket(t *testing.T) {
	f := newLiveSessionTest(t)
	ctx := context.Background()
	if _, err := f.db.Exec(ctx, `UPDATE rooms SET status='waiting' WHERE id=$1`, f.room); err != nil {
		t.Fatal(err)
	}
	var player int64
	if err := f.db.QueryRow(ctx, `INSERT INTO users(username,password_hash) VALUES('player','test') RETURNING id`).Scan(&player); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(ctx, `INSERT INTO room_members(room_id,user_id,role) VALUES($1,$2,'player')`, f.room, player); err != nil {
		t.Fatal(err)
	}
	cookie := admissionCookie(t, f, f.host)
	updates := reserveLive(t, f.hub, f.room, f.host, liveHost)
	handler := NewHandler(f.app, account.NewHandler(f.store, false))
	handler.liveHub = f.hub
	router := gin.New()
	router.POST("/api/rooms/:id/actions", handler.action)
	call := func(op string) *httptest.ResponseRecorder {
		data := []byte(fmt.Sprintf(`{"type":"start","operationId":%q}`, op))
		request := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/rooms/%d/actions", f.room), bytes.NewReader(data))
		request.Header.Set("Content-Type", "application/json")
		request.AddCookie(cookie)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}
	if response := call("pending_host_001"); response.Code != http.StatusConflict || !bytes.Contains(response.Body.Bytes(), []byte("主持人未在线")) {
		t.Fatalf("仅预占名额的主持人仍能开局：%d %s", response.Code, response.Body.String())
	}
	f.hub.activate(f.room, updates, nil)
	if response := call("online_host_001"); response.Code != http.StatusOK {
		t.Fatalf("握手完成后的主持人无法开局：%d %s", response.Code, response.Body.String())
	}
}

func TestSyncApprovedHostUsesLatestDatabaseState(t *testing.T) {
	f := newLiveSessionTest(t)
	ctx := context.Background()
	var middle, latest int64
	for i, out := range []*int64{&middle, &latest} {
		if err := f.db.QueryRow(ctx, `INSERT INTO users(username,password_hash) VALUES($1,'test') RETURNING id`, fmt.Sprintf("candidate_%d", i)).Scan(out); err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.Exec(ctx, `INSERT INTO room_members(room_id,user_id,role) VALUES($1,$2,'player')`, f.room, *out); err != nil {
			t.Fatal(err)
		}
	}
	oldUpdates, oldEvict, limit := f.hub.join(f.room, f.host, liveHost)
	if limit != liveAllowed {
		t.Fatal("旧主持连接预留失败")
	}
	f.hub.activate(f.room, oldUpdates, nil)
	newUpdates := reserveLive(t, f.hub, f.room, latest, livePlayer)
	f.hub.activate(f.room, newUpdates, nil)
	// 两次审批已提交，旧回调随后抵达时仍必须以数据库中的最新主持为准。
	if _, err := f.db.Exec(ctx, `UPDATE rooms SET host_id=$2 WHERE id=$1`, f.room, middle); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(ctx, `UPDATE rooms SET host_id=$2 WHERE id=$1`, f.room, latest); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(f.app, nil)
	handler.liveHub = f.hub
	if err := handler.syncApprovedHost(ctx, f.room); err != nil {
		t.Fatal(err)
	}
	if err := handler.syncApprovedHost(ctx, f.room); err != nil {
		t.Fatal(err)
	}
	if f.hub.hasUser(f.room, f.host) || !f.hub.hasUser(f.room, latest) {
		t.Fatal("逆序回调覆盖了数据库最新主持")
	}
	select {
	case <-oldEvict:
	default:
		t.Fatal("旧主持连接没有被驱逐")
	}
	if f.hub.leave(f.room, oldUpdates) {
		t.Fatal("被驱逐连接重复释放了主持在线状态")
	}
}

func TestLiveAdmissionWaitsForHostApproval(t *testing.T) {
	f := newLiveSessionTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var nextHost int64
	if err := f.db.QueryRow(ctx, `INSERT INTO users(username,password_hash) VALUES('next_host','test') RETURNING id`).Scan(&nextHost); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(ctx, `INSERT INTO room_members(room_id,user_id,role) VALUES($1,$2,'player')`, f.room, nextHost); err != nil {
		t.Fatal(err)
	}
	cookie := admissionCookie(t, f, nextHost)
	approver, err := f.db.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer approver.Release()
	tx, err := approver.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(ctx, `UPDATE rooms SET host_id=$2 WHERE id=$1`, f.room, nextHost); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE room_members SET role='spectator' WHERE room_id=$1 AND user_id=$2`, f.room, f.host); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE room_members SET role='host' WHERE room_id=$1 AND user_id=$2`, f.room, nextHost); err != nil {
		t.Fatal(err)
	}
	var blocker int32
	if err := approver.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&blocker); err != nil {
		t.Fatal(err)
	}
	type dialResult struct {
		conn *websocket.Conn
		err  error
	}
	finished := make(chan dialResult, 1)
	header := http.Header{}
	header.Set("Origin", f.server.URL)
	header.Set("Cookie", cookie.String())
	url := "ws" + strings.TrimPrefix(f.server.URL, "http") + fmt.Sprintf("/api/rooms/%d/ws", f.room)
	go func() {
		conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: header})
		finished <- dialResult{conn: conn, err: err}
	}()
	// 必须观察到请求被审批事务的房间行锁阻塞，再提交审批。
	blocked := false
	for !blocked {
		var pid int32
		if err := f.db.QueryRow(ctx, `SELECT COALESCE((SELECT pid FROM pg_stat_activity
			WHERE $1=ANY(pg_blocking_pids(pid)) LIMIT 1),0)`, blocker).Scan(&pid); err != nil {
			t.Fatal(err)
		}
		blocked = pid != 0
		if !blocked {
			select {
			case <-ctx.Done():
				t.Fatal("握手未等待审批提交")
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	if f.hub.hasUser(f.room, nextHost) {
		t.Fatal("审批未提交时新主持已被计为在线")
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-finished:
		if result.err != nil {
			t.Fatalf("审批后主持握手失败：%v", result.err)
		}
		defer result.conn.CloseNow()
	case <-ctx.Done():
		t.Fatal("审批提交后握手未完成")
	}
	current := false
	for !current {
		f.hub.mu.Lock()
		for _, viewer := range f.hub.viewers[f.room] {
			if viewer.user == nextHost && viewer.kind == liveHost && viewer.active {
				current = true
			}
		}
		f.hub.mu.Unlock()
		if !current {
			select {
			case <-ctx.Done():
				t.Fatal("审批后主持连接未激活")
			case <-time.After(time.Millisecond):
			}
		}
	}
}
