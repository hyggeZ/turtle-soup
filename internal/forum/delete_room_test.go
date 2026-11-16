package forum_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"turtle-soup/internal/account"
	"turtle-soup/internal/forum"
	"turtle-soup/internal/room"
	"turtle-soup/migrations"
)

type deletionFixture struct {
	t        *testing.T
	db       *pgxpool.Pool
	router   *gin.Engine
	server   *httptest.Server
	users    [3]int64
	cookies  [3]*http.Cookie
	nextCode int64
}

func newDeletionFixture(t *testing.T, deleteTracer, roomTracer pgx.QueryTracer) *deletionFixture {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("需要 TEST_DATABASE_URL")
	}
	ctx := context.Background()
	adminDB, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(adminDB.Close)
	schema := "delete_room_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if _, err := adminDB.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = adminDB.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })
	open := func(tracer pgx.QueryTracer) *pgxpool.Pool {
		config, err := pgxpool.ParseConfig(url)
		if err != nil {
			t.Fatal(err)
		}
		config.ConnConfig.RuntimeParams["search_path"] = schema
		config.ConnConfig.Tracer = tracer
		db, err := pgxpool.NewWithConfig(ctx, config)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(db.Close)
		return db
	}
	db := open(nil)
	forumDB := open(deleteTracer)
	roomDB := open(roomTracer)
	if err := migrations.Apply(ctx, db); err != nil {
		t.Fatal(err)
	}
	f := &deletionFixture{t: t, db: db}
	for i := range f.users {
		name := fmt.Sprintf("delete_user_%d", i)
		if err := db.QueryRow(ctx, "INSERT INTO users(username,password_hash) VALUES($1,'test') RETURNING id", name).Scan(&f.users[i]); err != nil {
			t.Fatal(err)
		}
		token := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{byte(i + 1)}, 32))
		hash := sha256.Sum256([]byte(token))
		if _, err := db.Exec(ctx, "INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,NOW()+INTERVAL '1 hour')", hash[:], f.users[i]); err != nil {
			t.Fatal(err)
		}
		f.cookies[i] = &http.Cookie{Name: "turtle_session", Value: token}
	}
	gin.SetMode(gin.TestMode)
	f.router = gin.New()
	auth := account.NewHandler(account.NewPGStore(db), false)
	rooms := room.NewHandler(roomDB, auth)
	forums := forum.NewHandler(forum.NewStore(forumDB), auth)
	forums.SetRoomNotifier(rooms.NotifyRoom)
	rooms.Register(f.router)
	forums.Register(f.router)
	f.server = httptest.NewServer(f.router)
	t.Cleanup(f.server.Close)
	return f
}

func (f *deletionFixture) call(method, path string, user int, body any) *httptest.ResponseRecorder {
	f.t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		f.t.Fatal(err)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(data))
	request.Header.Set("Content-Type", "application/json")
	if user >= 0 {
		request.AddCookie(f.cookies[user])
	}
	response := httptest.NewRecorder()
	f.router.ServeHTTP(response, request)
	return response
}

func (f *deletionFixture) soup() int64 {
	f.t.Helper()
	var id int64
	err := f.db.QueryRow(context.Background(), "INSERT INTO soups(author_id,title,surface,answer,genre,difficulty,horror) VALUES($1,'待删除题目','测试汤面','保留的汤底','本格',2,1) RETURNING id", f.users[0]).Scan(&id)
	if err != nil {
		f.t.Fatal(err)
	}
	return id
}

func (f *deletionFixture) room(soup int64, status string) (int64, string) {
	f.t.Helper()
	f.nextCode++
	code := fmt.Sprintf("%032x", f.nextCode)
	var id int64
	err := f.db.QueryRow(context.Background(), "INSERT INTO rooms(code,owner_id,host_id,soup_id,mode,max_players,public,status,soup_title,soup_surface,soup_answer,soup_genre,soup_difficulty,soup_horror) VALUES($1,$2,$2,$3,'casual',6,TRUE,$4,'快照标题','快照汤面','快照汤底','本格',2,1) RETURNING id", code, f.users[0], soup, status).Scan(&id)
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.db.Exec(context.Background(), "INSERT INTO room_members(room_id,user_id,role,seen_answer) VALUES($1,$2,'host',TRUE),($1,$3,'player',FALSE)", id, f.users[0], f.users[1]); err != nil {
		f.t.Fatal(err)
	}
	return id, code
}

func (f *deletionFixture) socket(roomID int64) *websocket.Conn {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	header := http.Header{}
	header.Set("Origin", f.server.URL)
	header.Set("Cookie", f.cookies[0].String())
	url := "ws" + strings.TrimPrefix(f.server.URL, "http") + fmt.Sprintf("/api/rooms/%d/ws", roomID)
	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { conn.CloseNow() })
	return conn
}

func waitResponse(t *testing.T, result <-chan *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	t.Helper()
	select {
	case response := <-result:
		return response
	case <-time.After(8 * time.Second):
		t.Fatal("并发请求超时")
		return nil
	}
}

func waitSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(8 * time.Second):
		t.Fatal("未到达预期的数据库锁阶段")
	}
}

type deletionBarrier struct {
	beforeSoup   chan struct{}
	resumeSoup   chan struct{}
	beforeSecond chan struct{}
	resumeSecond chan struct{}
	soupOnce     sync.Once
	secondOnce   sync.Once
	secondCalls  atomic.Int32
}

func (b *deletionBarrier) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.Contains(data.SQL, "SELECT author_id FROM soups WHERE id=$1 AND deleted_at IS NULL FOR UPDATE") {
		b.soupOnce.Do(func() { close(b.beforeSoup); <-b.resumeSoup })
	}
	if strings.Contains(data.SQL, "ORDER BY id FOR UPDATE NOWAIT") {
		b.secondCalls.Add(1)
		b.secondOnce.Do(func() { close(b.beforeSecond); <-b.resumeSecond })
	}
	return ctx
}

func (*deletionBarrier) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

type settingsSignal struct {
	armed  atomic.Bool
	once   sync.Once
	atSoup chan struct{}
}

func (s *settingsSignal) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if s.armed.Load() && strings.Contains(data.SQL, "SELECT author_id,title,surface,answer,genre,difficulty,horror FROM soups") {
		s.once.Do(func() { close(s.atSoup) })
	}
	return ctx
}

func (*settingsSignal) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestDeleteClosesWaitingAndPreservesStartedRooms(t *testing.T) {
	f := newDeletionFixture(t, nil, nil)
	soupID := f.soup()
	waiting, waitingCode := f.room(soupID, "waiting")
	active, activeCode := f.room(soupID, "active")
	paused, _ := f.room(soupID, "paused")
	finished, _ := f.room(soupID, "finished")
	connection := f.socket(waiting)
	path := fmt.Sprintf("/api/soups/%d", soupID)
	if response := f.call(http.MethodDelete, path, 0, nil); response.Code != http.StatusNoContent {
		t.Fatalf("删除题目：%d %s", response.Code, response.Body.String())
	}
	// 通知驱动房间页重新读取已提交的关闭快照。
	readCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, message, err := connection.Read(readCtx)
	if err != nil || string(message) != "{\"type\":\"changed\"}" {
		t.Fatalf("等待房未收到刷新通知：%q %v", message, err)
	}
	var status string
	var closedAt *time.Time
	if err := f.db.QueryRow(context.Background(), "SELECT status,closed_at FROM rooms WHERE id=$1", waiting).Scan(&status, &closedAt); err != nil || status != "closed" || closedAt == nil {
		t.Fatalf("等待房未关闭：%q %v %v", status, closedAt, err)
	}
	for _, item := range []struct {
		id   int64
		want string
	}{{active, "active"}, {paused, "paused"}, {finished, "finished"}} {
		if err := f.db.QueryRow(context.Background(), "SELECT status FROM rooms WHERE id=$1", item.id).Scan(&status); err != nil || status != item.want {
			t.Fatalf("已开局房间状态变化：%d %q %v", item.id, status, err)
		}
		response := f.call(http.MethodGet, fmt.Sprintf("/api/rooms/%d", item.id), 0, nil)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "快照汤底") {
			t.Fatalf("已开局快照不可回看：%d %s", response.Code, response.Body.String())
		}
	}
	if response := f.call(http.MethodGet, fmt.Sprintf("/api/rooms/%d", waiting), 0, nil); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "\"status\":\"closed\"") {
		t.Fatalf("房间页未刷新为关闭状态：%d %s", response.Code, response.Body.String())
	}
	if response := f.call(http.MethodPost, "/api/rooms/join", 2, map[string]any{"code": waitingCode}); response.Code != http.StatusConflict {
		t.Fatalf("新成员仍能进入等待房：%d %s", response.Code, response.Body.String())
	}
	if response := f.call(http.MethodPost, "/api/rooms/join", 2, map[string]any{"code": activeCode}); response.Code != http.StatusNotFound {
		t.Fatalf("下架题目仍接受新成员：%d %s", response.Code, response.Body.String())
	}
	if response := f.call(http.MethodPost, "/api/rooms/join", 1, map[string]any{"code": activeCode}); response.Code != http.StatusOK {
		t.Fatalf("原有成员不能重连进行中房间：%d %s", response.Code, response.Body.String())
	}
	if response := f.call(http.MethodPost, fmt.Sprintf("/api/rooms/%d/actions", waiting), 0, map[string]any{"type": "start", "operationId": "delete-start-001"}); response.Code != http.StatusConflict {
		t.Fatalf("关闭房间仍能开局：%d %s", response.Code, response.Body.String())
	}
	if response := f.call(http.MethodPost, fmt.Sprintf("/api/rooms/%d/actions", active), 1, map[string]any{"type": "question", "body": "仍能继续推理", "operationId": "delete-question-001"}); response.Code != http.StatusOK {
		t.Fatalf("进行中房间不能继续：%d %s", response.Code, response.Body.String())
	}
	response := f.call(http.MethodGet, "/api/rooms", -1, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("读取大厅：%d %s", response.Code, response.Body.String())
	}
	var lobby []struct{ ID int64 }
	if err := json.Unmarshal(response.Body.Bytes(), &lobby); err != nil {
		t.Fatal(err)
	}
	for _, item := range lobby {
		if item.ID == waiting || item.ID == active {
			t.Fatalf("下架题目仍在大厅：房间 %d", item.ID)
		}
	}
	if response := f.call(http.MethodPost, "/api/rooms", 2, map[string]any{"soupId": soupID, "mode": "casual", "maxPlayers": 3, "public": true}); response.Code != http.StatusNotFound {
		t.Fatalf("删除后仍可建房：%d %s", response.Code, response.Body.String())
	}
	if response := f.call(http.MethodDelete, path, 0, nil); response.Code != http.StatusNotFound {
		t.Fatalf("重复删除结果异常：%d %s", response.Code, response.Body.String())
	}
	// 模拟修复前遗留的等待房，开局仍必须核对题目状态。
	stale, _ := f.room(soupID, "waiting")
	f.socket(stale)
	if response := f.call(http.MethodPost, fmt.Sprintf("/api/rooms/%d/actions", stale), 0,
		map[string]any{"type": "start", "operationId": "stale-start-001"}); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "题目已下架") {
		t.Fatalf("遗留等待房仍能开局：%d %s", response.Code, response.Body.String())
	}
}

func TestDeleteRacesWithCreateAndStart(t *testing.T) {
	f := newDeletionFixture(t, nil, nil)
	for attempt := 0; attempt < 8; attempt++ {
		soupID := f.soup()
		path := fmt.Sprintf("/api/soups/%d", soupID)
		start := make(chan struct{})
		deleted := make(chan *httptest.ResponseRecorder, 1)
		created := make(chan *httptest.ResponseRecorder, 1)
		go func() { <-start; deleted <- f.call(http.MethodDelete, path, 0, nil) }()
		go func() {
			<-start
			created <- f.call(http.MethodPost, "/api/rooms", 1, map[string]any{"soupId": soupID, "mode": "casual", "maxPlayers": 3, "public": true})
		}()
		close(start)
		deleteResult := waitResponse(t, deleted)
		createResult := waitResponse(t, created)
		if deleteResult.Code != http.StatusNoContent || createResult.Code != http.StatusCreated && createResult.Code != http.StatusNotFound {
			t.Fatalf("建房竞态第 %d 次：删除 %d，建房 %d %s", attempt, deleteResult.Code, createResult.Code, createResult.Body.String())
		}
		var waiting int
		if err := f.db.QueryRow(context.Background(), "SELECT count(*) FROM rooms WHERE soup_id=$1 AND status='waiting'", soupID).Scan(&waiting); err != nil || waiting != 0 {
			t.Fatalf("建房竞态留下等待房：%d %v", waiting, err)
		}
	}
	for attempt := 0; attempt < 6; attempt++ {
		soupID := f.soup()
		roomID, _ := f.room(soupID, "waiting")
		connection := f.socket(roomID)
		start := make(chan struct{})
		deleted := make(chan *httptest.ResponseRecorder, 1)
		started := make(chan *httptest.ResponseRecorder, 1)
		go func() { <-start; deleted <- f.call(http.MethodDelete, fmt.Sprintf("/api/soups/%d", soupID), 0, nil) }()
		go func() {
			<-start
			started <- f.call(http.MethodPost, fmt.Sprintf("/api/rooms/%d/actions", roomID), 0, map[string]any{"type": "start", "operationId": fmt.Sprintf("start-race-%03d", attempt)})
		}()
		close(start)
		deleteResult := waitResponse(t, deleted)
		startResult := waitResponse(t, started)
		if deleteResult.Code != http.StatusNoContent || startResult.Code != http.StatusOK && startResult.Code != http.StatusConflict {
			t.Fatalf("开局竞态第 %d 次：删除 %d，开局 %d %s", attempt, deleteResult.Code, startResult.Code, startResult.Body.String())
		}
		var status string
		if err := f.db.QueryRow(context.Background(), "SELECT status FROM rooms WHERE id=$1", roomID).Scan(&status); err != nil || status != "closed" && status != "active" {
			t.Fatalf("开局竞态留下错误状态：%q %v", status, err)
		}
		if startResult.Code == http.StatusOK && status != "active" || startResult.Code == http.StatusConflict && status != "closed" {
			t.Fatalf("开局响应与最终状态不符：%d %q", startResult.Code, status)
		}
		connection.CloseNow()
	}
}

func TestDeleteRetriesNewRoomLockedBySettings(t *testing.T) {
	barrier := &deletionBarrier{
		beforeSoup: make(chan struct{}), resumeSoup: make(chan struct{}),
		beforeSecond: make(chan struct{}), resumeSecond: make(chan struct{}),
	}
	defer func() {
		for _, release := range []chan struct{}{barrier.resumeSoup, barrier.resumeSecond} {
			select {
			case <-release:
			default:
				close(release)
			}
		}
	}()
	signal := &settingsSignal{atSoup: make(chan struct{})}
	f := newDeletionFixture(t, barrier, signal)
	soupID := f.soup()
	deleted := make(chan *httptest.ResponseRecorder, 1)
	go func() { deleted <- f.call(http.MethodDelete, fmt.Sprintf("/api/soups/%d", soupID), 0, nil) }()
	waitSignal(t, barrier.beforeSoup)
	// 第一次扫描已完成，此时建出的新房没有被删除事务持锁。
	created := f.call(http.MethodPost, "/api/rooms", 1, map[string]any{"soupId": soupID, "mode": "casual", "maxPlayers": 3, "public": true})
	if created.Code != http.StatusCreated {
		t.Fatalf("窗口内建房：%d %s", created.Code, created.Body.String())
	}
	var output struct{ ID int64 }
	if err := json.Unmarshal(created.Body.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	close(barrier.resumeSoup)
	waitSignal(t, barrier.beforeSecond)
	// 删除持有题目锁；设置请求先取得新房间锁，再等待题目锁。
	signal.armed.Store(true)
	settings := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		settings <- f.call(http.MethodPatch, fmt.Sprintf("/api/rooms/%d/settings", output.ID), 1,
			map[string]any{"soupId": soupID, "mode": "competitive", "maxPlayers": 4, "public": true})
	}()
	waitSignal(t, signal.atSoup)
	close(barrier.resumeSecond)
	deleteResult := waitResponse(t, deleted)
	settingResult := waitResponse(t, settings)
	if settingResult.Code != http.StatusNoContent || deleteResult.Code != http.StatusNoContent {
		t.Fatalf("新房设置竞态：设置 %d %s，删除 %d %s", settingResult.Code, settingResult.Body.String(), deleteResult.Code, deleteResult.Body.String())
	}
	if barrier.secondCalls.Load() < 2 {
		t.Fatalf("未走到回滚重试：二次扫描 %d 次", barrier.secondCalls.Load())
	}
	var status string
	var deletedAt, closedAt *time.Time
	if err := f.db.QueryRow(context.Background(), "SELECT r.status,r.closed_at,s.deleted_at FROM rooms r JOIN soups s ON s.id=r.soup_id WHERE r.id=$1", output.ID).Scan(&status, &closedAt, &deletedAt); err != nil || status != "closed" || closedAt == nil || deletedAt == nil {
		t.Fatalf("重试后未原子关闭新房：%q %v %v %v", status, closedAt, deletedAt, err)
	}
}
