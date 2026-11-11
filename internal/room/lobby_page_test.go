package room_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"turtle-soup/internal/account"
	"turtle-soup/internal/room"
	"turtle-soup/migrations"
)

// 公开大厅超过 50 间时仍可读到旧房间，新房插入不会扰动下一页。
func TestLobbyCursorAcrossInsert(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("需要 TEST_DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := "lobby_page_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA "+schema+" CASCADE")
	}()
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	db, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := migrations.Apply(ctx, db); err != nil {
		t.Fatal(err)
	}
	var owner, soup int64
	if err := db.QueryRow(ctx, `INSERT INTO users(username,password_hash)
		VALUES('lobby_owner','test') RETURNING id`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `INSERT INTO soups(author_id,title,surface,answer,genre,difficulty,horror)
		VALUES($1,'测试题','汤面','汤底','本格',2,0) RETURNING id`, owner).Scan(&soup); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `INSERT INTO rooms(code,owner_id,host_id,soup_id,mode,max_players,public,status,
		soup_title,soup_surface,soup_answer,soup_genre,soup_difficulty,soup_horror)
		SELECT lpad(to_hex(n),32,'0'),$1,$1,$2,'casual',6,TRUE,'waiting',
			'测试题','汤面','汤底','本格',2,0
		FROM generate_series(1,51) AS n`, owner, soup)
	if err != nil {
		t.Fatal(err)
	}
	// 私密房间和已结束房间不应出现在任何一页。
	_, err = db.Exec(ctx, `INSERT INTO rooms(code,owner_id,host_id,soup_id,mode,max_players,public,status,
		soup_title,soup_surface,soup_answer,soup_genre,soup_difficulty,soup_horror)
		VALUES
		('000000000000000000000000000003E9',$1,$1,$2,'casual',6,FALSE,'waiting','测试题','汤面','汤底','本格',2,0),
		('000000000000000000000000000003EA',$1,$1,$2,'casual',6,TRUE,'finished','测试题','汤面','汤底','本格',2,0)`, owner, soup)
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	room.NewHandler(db, account.NewHandler(account.NewPGStore(db), false)).Register(router)
	type item struct {
		ID     int64  `json:"id"`
		Status string `json:"status"`
	}
	get := func(before string) []item {
		t.Helper()
		path := "/api/rooms"
		if before != "" {
			path += "?before=" + before
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("大厅分页返回 %d: %s", response.Code, response.Body.String())
		}
		var rooms []item
		if err := json.Unmarshal(response.Body.Bytes(), &rooms); err != nil {
			t.Fatal(err)
		}
		return rooms
	}
	first := get("")
	if len(first) != 50 || first[0].ID <= first[49].ID {
		t.Fatalf("第一页数量或顺序错误：%d", len(first))
	}
	var newID int64
	err = db.QueryRow(ctx, `INSERT INTO rooms(code,owner_id,host_id,soup_id,mode,max_players,public,status,
		soup_title,soup_surface,soup_answer,soup_genre,soup_difficulty,soup_horror)
		VALUES('00000000000000000000000000000034',$1,$1,$2,'casual',6,TRUE,'waiting',
		'测试题','汤面','汤底','本格',2,0) RETURNING id`, owner, soup).Scan(&newID)
	if err != nil {
		t.Fatal(err)
	}
	second := get(fmt.Sprint(first[49].ID))
	if len(second) != 1 || second[0].ID >= first[49].ID {
		t.Fatalf("插入新房后的第二页错误：%+v", second)
	}
	seen := make(map[int64]bool)
	for _, row := range append(first, second...) {
		if seen[row.ID] || row.Status != "waiting" {
			t.Fatalf("重复或不公开的房间进入列表：%+v", row)
		}
		seen[row.ID] = true
	}
	if len(seen) != 51 || seen[newID] || get("")[0].ID != newID {
		t.Fatal("翻页遗漏旧房间，或新房间未出现在刷新后的第一页")
	}
	for _, before := range []string{"0", "-1", "bad"} {
		response := httptest.NewRecorder()
		path := "/api/rooms?before=" + before
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("非法游标 %q 返回 %d", before, response.Code)
		}
	}
}
