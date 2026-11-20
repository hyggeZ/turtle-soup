package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"net/netip"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"turtle-soup/internal/account"
	"turtle-soup/internal/forum"
	"turtle-soup/internal/room"
	"turtle-soup/internal/telemetry"
	"turtle-soup/migrations"
)

const (
	readLoadPoolConns = 16
	readLoadSoups     = 500
	readLoadPairs     = 5000
	readLoadPending   = 4
)

var readLoadDBName = regexp.MustCompile(`^turtle_149_[a-z0-9_]+$`)

// 限制每个连接地址，防止临时 schema 或负载请求误落到远程数据库。
func checkReadLoadDB(t *testing.T, cfg *pgx.ConnConfig) {
	t.Helper()
	if !readLoadDBName.MatchString(cfg.Database) || !isReadLoadLocal(cfg.Host) {
		t.Fatal("压测只允许本机的 turtle_149_<标签> 临时数据库")
	}
	for _, fallback := range cfg.Fallbacks {
		if !isReadLoadLocal(fallback.Host) {
			t.Fatal("压测拒绝使用非本机的 PostgreSQL 备用地址")
		}
	}
}

func isReadLoadLocal(host string) bool {
	if host == "localhost" || strings.HasPrefix(host, "/") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}

func newReadLoadFixture(t *testing.T) (*httptest.Server, *pgxpool.Pool, []readLoadCase, string) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Fatal("已开启压测，必须设置 TEST_DATABASE_URL")
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal("TEST_DATABASE_URL 无法解析")
	}
	checkReadLoadDB(t, cfg.ConnConfig)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgxpool.NewWithConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	schema := fmt.Sprintf("read_load_%d", time.Now().UnixNano())
	ident := pgx.Identifier{schema}.Sanitize()
	mustReadLoadExec(t, ctx, admin, "CREATE SCHEMA "+ident)
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, err := admin.Exec(cleanup, "DROP SCHEMA "+ident+" CASCADE"); err != nil {
			t.Error("清理压测 schema 失败", err)
		}
	})
	monitor, err := telemetry.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = monitor.Close() })
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.ConnConfig.Tracer = monitor
	cfg.MinConns, cfg.MaxConns = 0, readLoadPoolConns
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := migrations.Apply(ctx, db); err != nil {
		t.Fatal(err)
	}
	var pgVersion string
	if err := db.QueryRow(ctx, "SHOW server_version").Scan(&pgVersion); err != nil {
		t.Fatal(err)
	}
	cases := seedReadLoadFixture(t, ctx, db)
	gin.SetMode(gin.ReleaseMode)
	router := newRouter(monitor)
	auth := account.NewHandler(account.NewPGStore(db), false)
	forum.NewHandler(forum.NewStore(db), auth).Register(router)
	room.NewHandler(db, auth).Register(router)
	server := httptest.NewUnstartedServer(router)
	server.Config = newHTTPServer(router)
	server.Start()
	t.Cleanup(server.Close)
	return server, db, cases, pgVersion
}

func seedReadLoadFixture(t *testing.T, ctx context.Context, db *pgxpool.Pool) []readLoadCase {
	t.Helper()
	host := readLoadID(t, ctx, db, `INSERT INTO users(username,password_hash) VALUES('load_host','fixture') RETURNING id`)
	player := readLoadID(t, ctx, db, `INSERT INTO users(username,password_hash) VALUES('load_player','fixture') RETURNING id`)
	mustReadLoadExec(t, ctx, db, `INSERT INTO soups(author_id,title,surface,answer,genre,difficulty,horror)
		SELECT $1,'压测题目 '||n,'固定汤面','固定汤底','本格',2,0 FROM generate_series(1,$2::int) AS n`, host, readLoadSoups)
	soup := readLoadID(t, ctx, db, `SELECT min(id) FROM soups`)
	roomID := readLoadID(t, ctx, db, `INSERT INTO rooms(code,owner_id,host_id,soup_id,mode,max_players,public,status,
		soup_title,soup_surface,soup_answer,soup_genre,soup_difficulty,soup_horror)
		VALUES('0123456789ABCDEF0123456789ABCDEF',$1,$1,$2,'casual',4,TRUE,'active',
		'压测题目 1','固定汤面','固定汤底','本格',2,0) RETURNING id`, host, soup)
	mustReadLoadExec(t, ctx, db, `INSERT INTO room_members(room_id,user_id,role,seen_answer)
		VALUES($1,$2,'host',TRUE),($1,$3,'player',FALSE)`, roomID, host, player)
	mustReadLoadExec(t, ctx, db, `WITH questions AS (
		INSERT INTO room_events(room_id,user_id,kind,body)
		SELECT $1,$2,'question','历史问题 '||n FROM generate_series(1,$4::int) AS n RETURNING id
	) INSERT INTO room_events(room_id,user_id,kind,result,parent_id)
	SELECT $1,$3,'answer','是',id FROM questions`, roomID, player, host, readLoadPairs)
	mustReadLoadExec(t, ctx, db, `INSERT INTO room_events(room_id,user_id,kind,body) VALUES
		($1,$2,'question','待答问题一'),($1,$2,'question','待答问题二'),
		($1,$2,'restore','待判还原'),($1,$2,'answer_request','')`, roomID, player)
	cursor := readLoadID(t, ctx, db, `SELECT max(id) FROM room_events WHERE room_id=$1`, roomID)
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString(random[:])
	hash := sha256.Sum256([]byte(token))
	mustReadLoadExec(t, ctx, db, `INSERT INTO sessions(token_hash,user_id,expires_at)
		VALUES($1,$2,NOW()+INTERVAL '2 hours')`, hash[:], host)
	for _, table := range []string{"soups", "rooms", "room_members", "room_events", "sessions", "users"} {
		mustReadLoadExec(t, ctx, db, "VACUUM ANALYZE "+table)
	}
	return []readLoadCase{
		{name: "public_soups", path: "/api/soups", verify: verifyReadLoadSoups},
		{name: "host_long_pending", path: fmt.Sprintf("/api/rooms/%d?history=2&after=%d&tasks=1", roomID, cursor),
			cookie: token, verify: verifyReadLoadRoom},
	}
}

func verifyReadLoadSoups(body io.Reader) error {
	var items []struct{ ID int64 }
	if err := json.NewDecoder(body).Decode(&items); err != nil {
		return err
	}
	if len(items) != 20 || items[0].ID <= 0 {
		return fmt.Errorf("题目列表未返回 20 条有效数据，收到 %d 条", len(items))
	}
	return nil
}

func verifyReadLoadRoom(body io.Reader) error {
	var snapshot struct {
		MyRole string
		Status string
		Events []json.RawMessage
		Tasks  struct{ Items []json.RawMessage }
	}
	if err := json.NewDecoder(body).Decode(&snapshot); err != nil {
		return err
	}
	if snapshot.MyRole != "host" || snapshot.Status != "active" || len(snapshot.Events) != 0 || len(snapshot.Tasks.Items) != readLoadPending {
		return fmt.Errorf("长局未返回主持身份、零增量事件与 %d 项待办", readLoadPending)
	}
	return nil
}

func mustReadLoadExec(t *testing.T, ctx context.Context, db *pgxpool.Pool, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		t.Fatal(err)
	}
}

func readLoadID(t *testing.T, ctx context.Context, db *pgxpool.Pool, query string, args ...any) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(ctx, query, args...).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
