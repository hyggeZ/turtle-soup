package room_test

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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"turtle-soup/internal/account"
	"turtle-soup/internal/room"
	"turtle-soup/migrations"
)

type roleQueryKey struct{}

// roleQueryBarrier 在快照读完旧主持身份后暂停请求，重现主持交接的并发时序。
type roleQueryBarrier struct {
	reached chan struct{}
	release chan struct{}
	used    atomic.Bool
}

func (b *roleQueryBarrier) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.Contains(data.SQL, "SELECT r.code,r.mode,r.status") {
		return context.WithValue(ctx, roleQueryKey{}, true)
	}
	return ctx
}

func (b *roleQueryBarrier) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	if ctx.Value(roleQueryKey{}) != true || !b.used.CompareAndSwap(false, true) {
		return
	}
	close(b.reached)
	select {
	case <-b.release:
	case <-ctx.Done():
	}
}

func TestOldHostSnapshotDoesNotSeeNewPrivateRestore(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("需要 TEST_DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("room_privacy_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		clean, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_, _ = admin.Exec(clean, "DROP SCHEMA "+schema+" CASCADE")
	}()

	barrier := &roleQueryBarrier{reached: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(barrier.release) }) }
	defer release()
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	config.ConnConfig.Tracer = barrier
	config.MaxConns = 6
	db, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := migrations.Apply(ctx, db); err != nil {
		t.Fatal(err)
	}

	users := make([]int64, 3)
	cookies := make([]*http.Cookie, len(users))
	for i := range users {
		name := fmt.Sprintf("privacy_user_%d", i)
		if err := db.QueryRow(ctx, `INSERT INTO users(username,password_hash) VALUES($1,'test') RETURNING id`, name).Scan(&users[i]); err != nil {
			t.Fatal(err)
		}
		token := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{byte(i + 1)}, 32))
		hash := sha256.Sum256([]byte(token))
		if _, err := db.Exec(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,NOW()+INTERVAL '1 hour')`, hash[:], users[i]); err != nil {
			t.Fatal(err)
		}
		cookies[i] = &http.Cookie{Name: "turtle_session", Value: token}
	}
	var soupID, roomID int64
	if err := db.QueryRow(ctx, `INSERT INTO soups(author_id,title,surface,answer,genre,difficulty,horror)
		VALUES($1,'权限竞态题','汤面','汤底','本格',2,0) RETURNING id`, users[0]).Scan(&soupID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `INSERT INTO rooms(code,owner_id,host_id,soup_id,mode,max_players,public,
		soup_title,soup_surface,soup_answer,soup_genre,soup_difficulty,soup_horror)
		VALUES('ABABABABABABABABABABABABABABABAB',$1,$1,$2,'casual',3,TRUE,
		'权限竞态题','汤面','汤底','本格',2,0) RETURNING id`, users[0], soupID).Scan(&roomID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO room_members(room_id,user_id,role,seen_answer)
		VALUES($1,$2,'host',TRUE),($1,$3,'player',FALSE),($1,$4,'player',FALSE)`, roomID, users[0], users[1], users[2]); err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	room.NewHandler(db, account.NewHandler(account.NewPGStore(db), false)).Register(router)
	path := fmt.Sprintf("/api/rooms/%d", roomID)
	call := func(method, url string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
		data, _ := json.Marshal(body)
		req := httptest.NewRequest(method, url, bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(cookie)
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		return res
	}
	action := func(user int, kind, operation string, target int64, body string) {
		t.Helper()
		res := call(http.MethodPost, path+"/actions", map[string]any{
			"type": kind, "operationId": operation, "userId": target, "body": body,
		}, cookies[user])
		if res.Code != http.StatusOK {
			t.Fatalf("%s 失败：%d %s", kind, res.Code, res.Body.String())
		}
	}
	action(1, "host_request", "request_host_01", 0, "")

	// 旧主持的快照停在身份查询之后，后续操作通过真实接口完成。
	viewDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { viewDone <- call(http.MethodGet, path, nil, cookies[0]) }()
	select {
	case <-barrier.reached:
	case <-time.After(5 * time.Second):
		t.Fatal("快照没有到达身份查询屏障")
	}
	action(0, "host_approve", "approve_host_01", users[1], "")
	action(0, "start", "start_game_01", 0, "")
	secret := "新主持才可看的私密还原"
	action(2, "restore", "private_restore_01", 0, secret)
	release()
	var oldView *httptest.ResponseRecorder
	select {
	case oldView = <-viewDone:
	case <-time.After(5 * time.Second):
		t.Fatal("旧主持快照没有完成")
	}
	if oldView.Code != http.StatusOK || strings.Contains(oldView.Body.String(), secret) {
		t.Fatalf("旧主持的在途快照泄露了交接后的私密还原：%d %s", oldView.Code, oldView.Body.String())
	}
	oldFresh := call(http.MethodGet, path, nil, cookies[0])
	if oldFresh.Code != http.StatusOK || strings.Contains(oldFresh.Body.String(), secret) || !strings.Contains(oldFresh.Body.String(), `"myRole":"spectator"`) {
		t.Fatalf("旧主持交接后的快照权限错误：%d %s", oldFresh.Code, oldFresh.Body.String())
	}
	newFresh := call(http.MethodGet, path, nil, cookies[1])
	if newFresh.Code != http.StatusOK || !strings.Contains(newFresh.Body.String(), secret) {
		t.Fatalf("新主持未看到私密还原：%d %s", newFresh.Code, newFresh.Body.String())
	}
}
