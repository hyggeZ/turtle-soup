package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http/httptest"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"turtle-soup/internal/account"
	"turtle-soup/internal/room"
	"turtle-soup/internal/telemetry"
	"turtle-soup/migrations"
)

const roomReplayAnswer = "合成汤底：守门人发现时钟停了。"

type roomReplayCase struct {
	id           int64
	host, player string
}

// 只允许数值回环地址的专用库，包括连接失败后可能使用的备用地址。
func openRoomReplayFixture(t *testing.T) (*httptest.Server, *pgxpool.Pool, string) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Fatal("回放需要 TEST_DATABASE_URL")
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal("TEST_DATABASE_URL 无法解析")
	}
	local := func(host string) bool {
		addr, err := netip.ParseAddr(host)
		return err == nil && addr.IsLoopback()
	}
	if cfg.ConnConfig.Database != "turtle_e2e" || !local(cfg.ConnConfig.Host) {
		t.Fatal("回放只允许数值回环地址上的 turtle_e2e 测试库")
	}
	for _, fallback := range cfg.ConnConfig.Fallbacks {
		if !local(fallback.Host) {
			t.Fatal("回放拒绝非回环的 PostgreSQL 备用地址")
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgxpool.NewWithConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal("无法建立回放管理连接池")
	}
	t.Cleanup(admin.Close)
	schema := fmt.Sprintf("room_replay_%d", time.Now().UnixNano())
	ident := pgx.Identifier{schema}.Sanitize()
	mustReadLoadExec(t, ctx, admin, "CREATE SCHEMA "+ident)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+ident+" CASCADE"); err != nil {
			t.Error("清理回放 schema 失败", err)
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
		t.Fatal("无法建立回放业务连接池")
	}
	t.Cleanup(db.Close)
	if err := migrations.Apply(ctx, db); err != nil {
		t.Fatal(err)
	}
	var pgVersion string
	if err := db.QueryRow(ctx, "SHOW server_version").Scan(&pgVersion); err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.ReleaseMode)
	router := newRouter(monitor)
	auth := account.NewHandler(account.NewPGStore(db), false)
	room.NewHandler(db, auth).Register(router)
	server := httptest.NewUnstartedServer(router)
	server.Config = newHTTPServer(router)
	server.Start()
	t.Cleanup(server.Close)
	return server, db, pgVersion
}

// 每局有自己的合成账号、题目与私密房间。登录、发题和开局耗时不进入测量。
func seedRoomReplay(t *testing.T, db *pgxpool.Pool, label string, count int) []roomReplayCase {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	rooms := make([]roomReplayCase, count)
	for i := range rooms {
		name := fmt.Sprintf("replay_%s_%02d", label, i)
		host, hostToken := newRoomReplayUser(t, ctx, db, name+"_host")
		player, playerToken := newRoomReplayUser(t, ctx, db, name+"_player")
		soup := readLoadID(t, ctx, db, `INSERT INTO soups(author_id,title,surface,answer,genre,difficulty,horror)
			VALUES($1,'时钟','老车站突然安静下来。',$2,'本格',2,0) RETURNING id`, host, roomReplayAnswer)
		id := readLoadID(t, ctx, db, `INSERT INTO rooms(code,owner_id,host_id,soup_id,mode,max_players,status,
			soup_title,soup_surface,soup_answer,soup_genre,soup_difficulty,soup_horror)
			SELECT $1,$2,$2,id,'casual',2,'active',title,surface,answer,genre,difficulty,horror FROM soups WHERE id=$3
			RETURNING id`, name, host, soup)
		mustReadLoadExec(t, ctx, db, `INSERT INTO room_members(room_id,user_id,role,seen_answer)
			VALUES($1,$2,'host',TRUE),($1,$3,'player',FALSE)`, id, host, player)
		rooms[i] = roomReplayCase{id: id, host: hostToken, player: playerToken}
	}
	return rooms
}

func newRoomReplayUser(t *testing.T, ctx context.Context, db *pgxpool.Pool, name string) (int64, string) {
	t.Helper()
	id := readLoadID(t, ctx, db, `INSERT INTO users(username,password_hash) VALUES($1,'fixture') RETURNING id`, name)
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal("无法生成合成测试会话")
	}
	token := base64.RawURLEncoding.EncodeToString(random[:])
	hash := sha256.Sum256([]byte(token))
	mustReadLoadExec(t, ctx, db, `INSERT INTO sessions(token_hash,user_id,expires_at)
		VALUES($1,$2,NOW()+INTERVAL '1 hour')`, hash[:], id)
	return id, token
}

// 响应合同通过后，再在未计时区间核对数据库里的实际事件和操作数。
func checkRoomReplayRows(t *testing.T, db *pgxpool.Pool, rooms []roomReplayCase) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for i, room := range rooms {
		var events, actions int
		err := db.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM room_events WHERE room_id=$1),
			(SELECT count(*) FROM game_operations WHERE room_id=$1)`, room.id).Scan(&events, &actions)
		if err != nil {
			t.Error("核对回放落库失败", err)
			continue
		}
		if events != 2 || actions != 2 {
			t.Errorf("合成房间 #%d 最终应有 2 条事件和 2 条操作，实际为 %d 和 %d", i+1, events, actions)
		}
	}
}
