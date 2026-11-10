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
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"turtle-soup/internal/account"
	"turtle-soup/internal/room"
	"turtle-soup/migrations"
)

// 在独立数据库 schema 中检查我的房间与公开大厅的访问边界。
func TestMyRoomsOnlyShowsMembership(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("需要 TEST_DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := "mine_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_, _ = admin.Exec(cleanupCtx, "DROP SCHEMA "+schema+" CASCADE")
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

	var owner, other, soup int64
	for _, item := range []struct {
		name string
		id   *int64
	}{{"owner", &owner}, {"other", &other}} {
		if err := db.QueryRow(ctx, `INSERT INTO users(username,password_hash) VALUES($1,'test') RETURNING id`, item.name).Scan(item.id); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.QueryRow(ctx, `INSERT INTO soups(author_id,title,surface,answer,genre,difficulty,horror)
		VALUES($1,'测试题','测试汤面','只可由有权用户读取的汤底','本格',2,0) RETURNING id`, owner).Scan(&soup); err != nil {
		t.Fatal(err)
	}
	serial := 0
	addRoom := func(host int64, status string, public bool, member int64) int64 {
		t.Helper()
		serial++
		code := fmt.Sprintf("%032X", serial)
		var id int64
		err := db.QueryRow(ctx, `INSERT INTO rooms(code,owner_id,host_id,soup_id,mode,max_players,public,status,
			soup_title,soup_surface,soup_answer,soup_genre,soup_difficulty,soup_horror)
			VALUES($1,$2,$2,$3,'casual',6,$4,$5,'测试题','测试汤面','只可由有权用户读取的汤底','本格',2,0)
			RETURNING id`, code, host, soup, public, status).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		if member != 0 {
			if _, err := db.Exec(ctx, `INSERT INTO room_members(room_id,user_id,role) VALUES($1,$2,'host')`, id, member); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	oldest := addRoom(owner, "waiting", false, owner)
	for i := 0; i < 50; i++ {
		addRoom(owner, "waiting", false, owner)
	}
	newest := addRoom(owner, "paused", false, owner)
	foreign := addRoom(other, "waiting", false, other)
	public := addRoom(other, "waiting", true, other)
	finished := addRoom(owner, "finished", false, owner)

	makeCookie := func(user int64, fill byte) *http.Cookie {
		t.Helper()
		token := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{fill}, 32))
		hash := sha256.Sum256([]byte(token))
		if _, err := db.Exec(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at)
			VALUES($1,$2,NOW()+INTERVAL '1 hour')`, hash[:], user); err != nil {
			t.Fatal(err)
		}
		return &http.Cookie{Name: "turtle_session", Value: token}
	}
	ownerCookie := makeCookie(owner, 1)
	otherCookie := makeCookie(other, 2)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	room.NewHandler(db, account.NewHandler(account.NewPGStore(db), false)).Register(router)
	get := func(path string, cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		if cookie != nil {
			request.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}
	if response := get("/api/rooms/mine", nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("未登录获取我的房间：%d %s", response.Code, response.Body.String())
	}
	response := get("/api/rooms/mine", ownerCookie)
	if response.Code != http.StatusOK {
		t.Fatalf("本人列表读取失败：%d %s", response.Code, response.Body.String())
	}
	var own []map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &own); err != nil || len(own) != 50 {
		t.Fatalf("本人列表未限制 50 条：count=%d err=%v", len(own), err)
	}
	seen := make(map[int64]bool)
	for _, item := range own {
		var id int64
		if err := json.Unmarshal(item["id"], &id); err != nil {
			t.Fatal(err)
		}
		seen[id] = true
		for _, private := range []string{"code", "answer", "events", "surface"} {
			if _, exists := item[private]; exists {
				t.Fatalf("我的房间列表包含私有字段 %s", private)
			}
		}
	}
	if !seen[newest] || seen[oldest] || seen[foreign] || seen[public] || seen[finished] {
		t.Fatalf("本人列表的成员或状态过滤错误：%v", seen)
	}
	var state string
	if err := json.Unmarshal(own[0]["status"], &state); err != nil || state != "paused" {
		t.Fatalf("暂停房间未进入我的列表：%s %v", state, err)
	}
	response = get("/api/rooms/mine", otherCookie)
	if response.Code != http.StatusOK {
		t.Fatalf("其他账号列表读取失败：%d", response.Code)
	}
	var others []map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &others); err != nil || len(others) != 2 {
		t.Fatalf("其他账号列表不正确：count=%d err=%v", len(others), err)
	}
	for _, item := range others {
		var id int64
		if err := json.Unmarshal(item["id"], &id); err != nil || id == newest || id == oldest {
			t.Fatalf("其他账号枚举了私密房间：id=%d err=%v", id, err)
		}
	}
	response = get("/api/rooms", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("公开大厅读取失败：%d", response.Code)
	}
	var lobby []map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &lobby); err != nil || len(lobby) != 1 {
		t.Fatalf("公开大厅泄露私密房间：count=%d err=%v", len(lobby), err)
	}
	var lobbyID int64
	if err := json.Unmarshal(lobby[0]["id"], &lobbyID); err != nil || lobbyID != public {
		t.Fatalf("公开大厅内容错误：id=%d err=%v", lobbyID, err)
	}
	if response := get(fmt.Sprintf("/api/rooms/%d", newest), otherCookie); response.Code != http.StatusNotFound {
		t.Fatalf("其他账号读取私密房间成功：%d %s", response.Code, response.Body.String())
	}
}
