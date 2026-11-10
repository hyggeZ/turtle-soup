package room_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
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

type queryCounter struct{ count atomic.Int64 }

func (q *queryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	q.count.Add(1)
	return ctx
}

func (q *queryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// BenchmarkRoomSnapshot 统计主持人读取房间快照的耗时、分配和数据库查询次数。
func BenchmarkRoomSnapshot(b *testing.B) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		b.Skip("需要 TEST_DATABASE_URL")
	}
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(admin.Close)
	schema := fmt.Sprintf("bench_room_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE") })

	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		b.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	queries := &queryCounter{}
	cfg.ConnConfig.Tracer = queries
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(db.Close)
	if err := migrations.Apply(ctx, db); err != nil {
		b.Fatal(err)
	}

	var host, player, soupID, roomID int64
	if err := db.QueryRow(ctx, `INSERT INTO users(username,password_hash) VALUES('bench_host','test') RETURNING id`).Scan(&host); err != nil {
		b.Fatal(err)
	}
	if err := db.QueryRow(ctx, `INSERT INTO users(username,password_hash) VALUES('bench_player','test') RETURNING id`).Scan(&player); err != nil {
		b.Fatal(err)
	}
	if err := db.QueryRow(ctx, `INSERT INTO soups(author_id,title,surface,answer,genre,difficulty,horror)
		VALUES($1,'基准汤题','汤面','汤底','本格',2,0) RETURNING id`, host).Scan(&soupID); err != nil {
		b.Fatal(err)
	}
	if err := db.QueryRow(ctx, `INSERT INTO rooms(code,owner_id,host_id,soup_id,mode,max_players,public,soup_title,soup_surface,soup_answer,soup_genre,soup_difficulty,soup_horror)
		VALUES('0123456789ABCDEF0123456789ABCDEF',$1,$1,$2,'casual',4,TRUE,'基准汤题','汤面','汤底','本格',2,0) RETURNING id`, host, soupID).Scan(&roomID); err != nil {
		b.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO room_members(room_id,user_id,role,seen_answer) VALUES($1,$2,'host',TRUE),($1,$3,'player',FALSE)`, roomID, host, player); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if _, err := db.Exec(ctx, `INSERT INTO room_events(room_id,user_id,kind,body) VALUES($1,$2,'question',$3)`, roomID, player, fmt.Sprintf("问题 %d", i)); err != nil {
			b.Fatal(err)
		}
	}
	token := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	hash := sha256.Sum256([]byte(token))
	if _, err := db.Exec(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,NOW()+INTERVAL '1 hour')`, hash[:], host); err != nil {
		b.Fatal(err)
	}
	auth := account.NewHandler(account.NewPGStore(db), false)
	router := gin.New()
	room.NewHandler(db, auth).Register(router)
	path := fmt.Sprintf("/api/rooms/%d", roomID)
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.AddCookie(&http.Cookie{Name: "turtle_session", Value: token})

	queries.count.Store(0)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			b.Fatalf("读取快照失败：%d %s", response.Code, response.Body.String())
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(queries.count.Load())/float64(b.N), "dbq/op")
}
