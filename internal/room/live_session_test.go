package room

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
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
	"turtle-soup/migrations"
)

type heartbeatTimeKey struct{}

// 只统计业务连接的 SQL；建表、造数据和断言使用另一连接池。
type heartbeatQueries struct {
	count atomic.Int64
	nanos atomic.Int64
}

type testSessionStore struct {
	account.Store
	fail atomic.Bool
}

func (s *testSessionStore) FindSessionUser(ctx context.Context, hash []byte) (account.User, error) {
	if s.fail.Load() {
		return account.User{}, errors.New("模拟数据库查询失败")
	}
	return s.Store.FindSessionUser(ctx, hash)
}

func (q *heartbeatQueries) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, heartbeatTimeKey{}, time.Now())
}

func (q *heartbeatQueries) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	q.count.Add(1)
	if start, ok := ctx.Value(heartbeatTimeKey{}).(time.Time); ok {
		q.nanos.Add(time.Since(start).Nanoseconds())
	}
}

type liveSessionTest struct {
	t      *testing.T
	db     *pgxpool.Pool
	app    *pgxpool.Pool
	hub    *liveHub
	stats  *heartbeatQueries
	store  *testSessionStore
	server *httptest.Server
	host   int64
	room   int64
	ticks  sync.Map // 会话令牌 -> 该连接专用的手动心跳
}

func newLiveSessionTest(t *testing.T) *liveSessionTest {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("需要 TEST_DATABASE_URL")
	}
	gin.SetMode(gin.TestMode)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	schema := fmt.Sprintf("live_session_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA "+schema+" CASCADE")
	})
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	db, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := migrations.Apply(ctx, db); err != nil {
		t.Fatal(err)
	}
	stats := &heartbeatQueries{}
	config.ConnConfig.Tracer = stats
	app, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	fixture := &liveSessionTest{t: t, db: db, app: app, stats: stats}
	if err := db.QueryRow(ctx, `INSERT INTO users(username,password_hash) VALUES('host','test') RETURNING id`).Scan(&fixture.host); err != nil {
		t.Fatal(err)
	}
	var soup int64
	if err := db.QueryRow(ctx, `INSERT INTO soups(author_id,title,surface,answer,genre,difficulty,horror)
		VALUES($1,'心跳测试','汤面','汤底','本格',2,0) RETURNING id`, fixture.host).Scan(&soup); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `INSERT INTO rooms(code,owner_id,host_id,soup_id,mode,max_players,status)
		VALUES('LIVE_SESSION_TEST',$1,$1,$2,'casual',2,'active') RETURNING id`, fixture.host, soup).Scan(&fixture.room); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO room_members(room_id,user_id,role) VALUES($1,$2,'host')`, fixture.room, fixture.host); err != nil {
		t.Fatal(err)
	}
	fixture.store = &testSessionStore{Store: account.NewPGStore(app)}
	auth := account.NewHandler(fixture.store, false)
	handler := NewHandler(app, auth)
	fixture.hub = handler.liveHub
	router := gin.New()
	router.GET("/api/rooms/:id/ws", func(c *gin.Context) {
		cookie, err := c.Request.Cookie("turtle_session")
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录"})
			return
		}
		value, ok := fixture.ticks.Load(cookie.Value)
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "缺少测试心跳"})
			return
		}
		handler.liveWithTicks(c, value.(chan time.Time))
	})
	fixture.server = httptest.NewServer(router)
	t.Cleanup(fixture.server.Close)
	return fixture
}

func (f *liveSessionTest) open() (*websocket.Conn, context.Context, chan time.Time, []byte) {
	f.t.Helper()
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		f.t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	hash := sha256.Sum256([]byte(token))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := f.db.Exec(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at)
		VALUES($1,$2,NOW()+INTERVAL '1 hour')`, hash[:], f.host); err != nil {
		f.t.Fatal(err)
	}
	ticks := make(chan time.Time)
	f.ticks.Store(token, ticks)
	header := http.Header{}
	header.Set("Origin", f.server.URL)
	header.Set("Cookie", (&http.Cookie{Name: "turtle_session", Value: token}).String())
	url := "ws" + strings.TrimPrefix(f.server.URL, "http") + fmt.Sprintf("/api/rooms/%d/ws", f.room)
	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { conn.CloseNow(); f.ticks.Delete(token) })
	closed := conn.CloseRead(context.Background())
	return conn, closed, ticks, hash[:]
}

func (f *liveSessionTest) waitQueries(want int64) {
	f.t.Helper()
	deadline := time.After(3 * time.Second)
	for f.stats.count.Load() < want {
		select {
		case <-deadline:
			f.t.Fatalf("等待业务 SQL：当前 %d，期望至少 %d", f.stats.count.Load(), want)
		case <-time.After(time.Millisecond):
		}
	}
}

func (f *liveSessionTest) status() string {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var status string
	if err := f.db.QueryRow(ctx, `SELECT status FROM rooms WHERE id=$1`, f.room).Scan(&status); err != nil {
		f.t.Fatal(err)
	}
	return status
}

func waitSocketClosed(t *testing.T, closed context.Context) {
	t.Helper()
	select {
	case <-closed.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("会话失效后 WebSocket 未断开")
	}
}

func TestLiveSessionRevocationWithPostgres(t *testing.T) {
	f := newLiveSessionTest(t)
	first, firstClosed, firstTicks, firstHash := f.open()
	_, secondClosed, secondTicks, secondHash := f.open()
	f.waitQueries(6) // 每次连接：登录、成员检查、主持恢复检查各一次。
	before := f.stats.count.Load()
	queryTime := f.stats.nanos.Load()
	poolBefore := f.app.Stat().TotalConns()
	start := time.Now()
	select {
	case firstTicks <- start:
	case <-time.After(3 * time.Second):
		t.Fatal("正常心跳未运行")
	}
	f.waitQueries(before + 1)
	if got := f.stats.count.Load() - before; got != 1 {
		t.Fatalf("正常心跳应只增加一次会话查询，实际 %d", got)
	}
	t.Logf("正常心跳：1 次 SQL，查询 %.3f ms，查询完成耗时 %.3f ms；连接池总连接 %d -> %d",
		float64(f.stats.nanos.Load()-queryTime)/1e6, float64(time.Since(start).Nanoseconds())/1e6,
		poolBefore, f.app.Stat().TotalConns())
	select {
	case <-firstClosed.Done():
		t.Fatal("有效会话在正常心跳后被断开")
	default:
	}
	pingCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	if err := first.Ping(pingCtx); err != nil {
		t.Fatalf("正常心跳后连接不可用：%v", err)
	}
	cancel()
	// 多次心跳验证每轮恰好一次 SQL，并记录稳定状态下的平均开销。
	before = f.stats.count.Load()
	queryTime = f.stats.nanos.Load()
	start = time.Now()
	for i := int64(1); i <= 20; i++ {
		select {
		case firstTicks <- time.Now():
		case <-time.After(3 * time.Second):
			t.Fatal("连续心跳未运行")
		}
		f.waitQueries(before + i)
		if got := f.stats.count.Load(); got != before+i {
			t.Fatalf("第 %d 次心跳后累计 SQL=%d，期望 %d", i, got, before+i)
		}
	}
	t.Logf("连续 20 次心跳：%.3f ms/次查询，%.3f ms/次触发到查询完成；连接池总连接 %d -> %d",
		float64(f.stats.nanos.Load()-queryTime)/20e6, float64(time.Since(start).Nanoseconds())/20e6,
		poolBefore, f.app.Stat().TotalConns())
	ctx, stop := context.WithTimeout(context.Background(), 3*time.Second)
	if _, err := f.db.Exec(ctx, `DELETE FROM sessions WHERE token_hash=$1`, firstHash); err != nil {
		t.Fatal(err)
	}
	stop()
	firstTicks <- time.Now()
	waitSocketClosed(t, firstClosed)
	if f.status() != "active" {
		t.Fatal("另一有效设备在线时，对局被错误暂停")
	}
	select {
	case <-secondClosed.Done():
		t.Fatal("另一有效设备被错误断开")
	default:
	}
	ctx, stop = context.WithTimeout(context.Background(), 3*time.Second)
	if _, err := f.db.Exec(ctx, `DELETE FROM sessions WHERE token_hash=$1`, secondHash); err != nil {
		t.Fatal(err)
	}
	stop()
	secondTicks <- time.Now()
	waitSocketClosed(t, secondClosed)
	if f.status() != "paused" || f.hub.hasUser(f.room, f.host) {
		t.Fatal("最后一个主持会话撤销后，房间未暂停或仍被计为在线")
	}
}

func TestLiveDisabledUserWithPostgres(t *testing.T) {
	f := newLiveSessionTest(t)
	_, closed, ticks, _ := f.open()
	f.waitQueries(3)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	if _, err := f.db.Exec(ctx, `UPDATE users SET disabled=TRUE WHERE id=$1`, f.host); err != nil {
		t.Fatal(err)
	}
	cancel()
	ticks <- time.Now()
	waitSocketClosed(t, closed)
	if f.status() != "paused" {
		t.Fatal("账号停用后的主持连接断开，但房间未暂停")
	}
}

func TestLiveSessionQueryFailureWithPostgres(t *testing.T) {
	f := newLiveSessionTest(t)
	_, closed, ticks, _ := f.open()
	f.waitQueries(3)
	f.store.fail.Store(true)
	ticks <- time.Now()
	waitSocketClosed(t, closed)
	if f.status() != "paused" || f.hub.hasUser(f.room, f.host) {
		t.Fatal("会话查询失败后未关闭连接并暂停对局")
	}
}
