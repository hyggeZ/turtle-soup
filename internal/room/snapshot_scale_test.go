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
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"turtle-soup/internal/account"
	"turtle-soup/internal/room"
	"turtle-soup/migrations"
)

// 仅在显式指定环境变量时运行，用独立 schema 测量长局快照的增长。
func TestSnapshotScale(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" || os.Getenv("SNAPSHOT_SCALE") != "1" {
		t.Skip("需要 TEST_DATABASE_URL 和 SNAPSHOT_SCALE=1")
	}
	gin.SetMode(gin.TestMode)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("scale_room_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		clean, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_, _ = admin.Exec(clean, "DROP SCHEMA "+schema+" CASCADE")
	}()
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	config.MaxConns = 1
	queries := &queryCounter{}
	config.ConnConfig.Tracer = queries
	db, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := migrations.Apply(ctx, db); err != nil {
		t.Fatal(err)
	}

	var host, player, soup int64
	for _, item := range []struct {
		name string
		id   *int64
	}{{"scale_host", &host}, {"scale_player", &player}} {
		if err := db.QueryRow(ctx, `INSERT INTO users(username,password_hash) VALUES($1,'test') RETURNING id`, item.name).Scan(item.id); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.QueryRow(ctx, `INSERT INTO soups(author_id,title,surface,answer,genre,difficulty,horror)
		VALUES($1,'测量题目','测量汤面','测量汤底','本格',2,0) RETURNING id`, host).Scan(&soup); err != nil {
		t.Fatal(err)
	}
	rooms := map[int]int64{}
	for _, count := range []int{100, 1000, 5000} {
		var id int64
		code := fmt.Sprintf("%032X", count)
		if err := db.QueryRow(ctx, `INSERT INTO rooms(code,owner_id,host_id,soup_id,mode,max_players,public,
			soup_title,soup_surface,soup_answer,soup_genre,soup_difficulty,soup_horror)
			VALUES($1,$2,$2,$3,'casual',4,TRUE,'测量题目','测量汤面','测量汤底','本格',2,0) RETURNING id`, code, host, soup).Scan(&id); err != nil {
			t.Fatal(err)
		}
		rooms[count] = id
		if _, err := db.Exec(ctx, `INSERT INTO room_members(room_id,user_id,role,seen_answer)
			VALUES($1,$2,'host',TRUE),($1,$3,'player',FALSE)`, id, host, player); err != nil {
			t.Fatal(err)
		}
		// 每个问题都配一个主持答复，恰好形成 count 条事件。
		if _, err := db.Exec(ctx, `WITH questions AS (
			INSERT INTO room_events(room_id,user_id,kind,body)
			SELECT $1,$2,'question',repeat('问题内容',12)||n FROM generate_series(1,$4::int) AS n
			RETURNING id
		) INSERT INTO room_events(room_id,user_id,kind,result,parent_id)
		SELECT $1,$3,'answer','是',id FROM questions`, id, player, host, count/2); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(ctx, `ANALYZE room_events`); err != nil {
		t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))
	hash := sha256.Sum256([]byte(token))
	if _, err := db.Exec(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at)
		VALUES($1,$2,NOW()+INTERVAL '1 hour')`, hash[:], host); err != nil {
		t.Fatal(err)
	}
	auth := account.NewHandler(account.NewPGStore(db), false)
	router := gin.New()
	room.NewHandler(db, auth).Register(router)
	get := func(path string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.AddCookie(&http.Cookie{Name: "turtle_session", Value: token})
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("快照失败：%d %s", response.Code, response.Body.String())
		}
		return response
	}
	cursors := map[int]int64{}
	for _, count := range []int{100, 1000, 5000} {
		var initial struct {
			EventCursor int64 `json:"eventCursor"`
		}
		if err := json.Unmarshal(get(fmt.Sprintf("/api/rooms/%d", rooms[count])).Body.Bytes(), &initial); err != nil || initial.EventCursor < 1 {
			t.Fatalf("初始游标无效：%v %+v", err, initial)
		}
		cursors[count] = initial.EventCursor
		// 初次进入之后又产生一次问答；比较同一数据库状态下的完整刷新与增量刷新。
		if _, err := db.Exec(ctx, `WITH question AS (
			INSERT INTO room_events(room_id,user_id,kind,body) VALUES($1,$2,'question','新问题') RETURNING id
		) INSERT INTO room_events(room_id,user_id,kind,result,parent_id)
		SELECT $1,$3,'answer','是',id FROM question`, rooms[count], player, host); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(ctx, `ANALYZE room_events`); err != nil {
		t.Fatal(err)
	}
	measure := func(count int, after int64) {
		path := fmt.Sprintf("/api/rooms/%d", rooms[count])
		label := "full"
		if after > 0 {
			path += fmt.Sprintf("?after=%d", after)
			label = "delta"
		}
		for i := 0; i < 5; i++ {
			get(path)
		}
		queries.count.Store(0)
		times := make([]float64, 30)
		responseBytes := 0
		for i := range times {
			start := time.Now()
			response := get(path)
			times[i] = float64(time.Since(start)) / float64(time.Millisecond)
			responseBytes = response.Body.Len()
		}
		sort.Float64s(times)
		t.Logf("mode=%s events=%d responseBytes=%d queries=%.1f/op p50=%.2fms p95=%.2fms", label, count+2, responseBytes, float64(queries.count.Load())/30, times[14], times[28])
		var sample struct {
			EventCursor int64             `json:"eventCursor"`
			Events      []json.RawMessage `json:"events"`
		}
		if err := json.Unmarshal(get(path).Body.Bytes(), &sample); err != nil {
			t.Fatal(err)
		}
		want := count + 2
		if after > 0 {
			want = 2
		}
		if len(sample.Events) != want || sample.EventCursor <= 0 {
			t.Fatalf("事件数量或游标错误：mode=%s count=%d cursor=%d", label, len(sample.Events), sample.EventCursor)
		}
		if count == 5000 {
			logEventQueryPlan(t, ctx, db, rooms[count], after, label)
		}
	}
	for _, count := range []int{100, 1000, 5000} {
		measure(count, 0)
		measure(count, cursors[count])
	}
}

func logEventQueryPlan(t *testing.T, ctx context.Context, db *pgxpool.Pool, roomID, after int64, label string) {
	t.Helper()
	query := `EXPLAIN (ANALYZE,BUFFERS)
		SELECT e.id,e.user_id,e.kind,e.body,e.result,e.parent_id,e.created_at,p.user_id,
		EXISTS(SELECT 1 FROM room_events reply WHERE reply.parent_id=e.id AND reply.kind='answer')
		FROM room_events e`
	args := []any{roomID}
	if after > 0 {
		query += ` JOIN (
			SELECT id FROM room_events WHERE room_id=$1 AND id>$2
			UNION
			SELECT parent_id FROM room_events
			WHERE room_id=$1 AND id>$2 AND kind='answer' AND parent_id IS NOT NULL
		) changed ON changed.id=e.id`
		args = append(args, after)
	}
	query += ` LEFT JOIN room_events p ON p.id=e.parent_id WHERE e.room_id=$1 ORDER BY e.id`
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(line, "SubPlan") || strings.Contains(line, "Scan") || strings.Contains(line, "Execution Time") || strings.Contains(line, "JIT") || strings.Contains(line, "Timing") {
			t.Logf("mode=%s plan: %s", label, strings.TrimSpace(line))
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
