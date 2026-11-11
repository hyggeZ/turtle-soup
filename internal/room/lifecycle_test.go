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
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"turtle-soup/internal/account"
	"turtle-soup/internal/room"
	"turtle-soup/migrations"
)

// 在真实 PostgreSQL 中检查并发建房上限、关闭后的访问与越权操作。
func TestRoomCreationLimitAndClose(t *testing.T) {
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
	schema := "room_lifecycle_" + strconv.FormatInt(time.Now().UnixNano(), 10)
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

	users := make([]int64, 3)
	cookies := make([]*http.Cookie, len(users))
	for i := range users {
		name := fmt.Sprintf("room_user_%d", i)
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
	soups := make([]int64, 6)
	for i := range soups {
		if err := db.QueryRow(ctx, `INSERT INTO soups(author_id,title,surface,answer,genre,difficulty,horror)
			VALUES($1,$2,'测试汤面','测试汤底','本格',2,1) RETURNING id`, users[0], fmt.Sprintf("测试题 %d", i)).Scan(&soups[i]); err != nil {
			t.Fatal(err)
		}
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	room.NewHandler(db, account.NewHandler(account.NewPGStore(db), false)).Register(router)
	call := func(method, path string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
		data, _ := json.Marshal(body)
		req := httptest.NewRequest(method, path, bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		return res
	}
	create := func(soup int64) *httptest.ResponseRecorder {
		return call(http.MethodPost, "/api/rooms", map[string]any{
			"soupId": soup, "mode": "casual", "maxPlayers": 6, "public": true,
		}, cookies[0])
	}

	// 六个请求同时到达，只有五个能写入；不同题目排除题目行锁的偶然串行效果。
	start := make(chan struct{})
	results := make([]*httptest.ResponseRecorder, len(soups))
	var group sync.WaitGroup
	for i, soup := range soups {
		group.Add(1)
		go func(i int, soup int64) {
			defer group.Done()
			<-start
			results[i] = create(soup)
		}(i, soup)
	}
	close(start)
	group.Wait()
	type createdRoom struct {
		ID   int64  `json:"id"`
		Code string `json:"code"`
	}
	var selected createdRoom
	success, conflicts := 0, 0
	var rejectedSoup int64
	for i, res := range results {
		switch res.Code {
		case http.StatusCreated:
			success++
			if selected.ID == 0 {
				if err := json.Unmarshal(res.Body.Bytes(), &selected); err != nil {
					t.Fatal(err)
				}
			}
		case http.StatusConflict:
			conflicts++
			rejectedSoup = soups[i]
		default:
			t.Fatalf("并发建房返回异常：%d %s", res.Code, res.Body.String())
		}
	}
	if success != 5 || conflicts != 1 {
		t.Fatalf("并发建房上限失效：成功 %d，冲突 %d", success, conflicts)
	}
	var openCount int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM rooms WHERE owner_id=$1 AND status IN ('waiting','active','paused')`, users[0]).Scan(&openCount); err != nil || openCount != 5 {
		t.Fatalf("数据库中的未结束房间数错误：%d，%v", openCount, err)
	}
	path := fmt.Sprintf("/api/rooms/%d", selected.ID)
	if res := call(http.MethodPost, "/api/rooms/join", map[string]any{"code": selected.Code}, cookies[1]); res.Code != http.StatusOK {
		t.Fatalf("关闭前成员加入失败：%d %s", res.Code, res.Body.String())
	}
	if res := call(http.MethodPost, path+"/actions", map[string]any{"type": "host_request", "operationId": "request-host-role"}, cookies[1]); res.Code != http.StatusOK {
		t.Fatalf("申请主持失败：%d %s", res.Code, res.Body.String())
	}
	if res := call(http.MethodPost, path+"/actions", map[string]any{"type": "host_approve", "userId": users[1], "operationId": "approve-host-role"}, cookies[0]); res.Code != http.StatusOK {
		t.Fatalf("确认主持失败：%d %s", res.Code, res.Body.String())
	}
	act := func(cookie *http.Cookie, op string) *httptest.ResponseRecorder {
		return call(http.MethodPost, path+"/actions", map[string]any{"type": "close", "operationId": op}, cookie)
	}
	if res := act(cookies[1], "not-owner-close"); res.Code != http.StatusConflict {
		t.Fatalf("非房主的主持人关闭成功：%d %s", res.Code, res.Body.String())
	}
	if res := act(cookies[2], "not-member-close"); res.Code != http.StatusForbidden {
		t.Fatalf("非成员关闭成功：%d %s", res.Code, res.Body.String())
	}
	if res := act(cookies[0], "owner-close-room"); res.Code != http.StatusOK {
		t.Fatalf("房主关闭失败：%d %s", res.Code, res.Body.String())
	}
	if res := act(cookies[0], "owner-close-room"); res.Code != http.StatusOK || !bytes.Contains(res.Body.Bytes(), []byte(`"duplicate":true`)) {
		t.Fatalf("关闭重试没有复用原结果：%d %s", res.Code, res.Body.String())
	}
	var status string
	var closedAt *time.Time
	if err := db.QueryRow(ctx, `SELECT status,closed_at FROM rooms WHERE id=$1`, selected.ID).Scan(&status, &closedAt); err != nil || status != "closed" || closedAt == nil {
		t.Fatalf("关闭状态未保留：%s %v %v", status, closedAt, err)
	}
	if res := call(http.MethodGet, path, nil, cookies[1]); res.Code != http.StatusOK || !bytes.Contains(res.Body.Bytes(), []byte(`"status":"closed"`)) {
		t.Fatalf("已关闭房间不能回看：%d %s", res.Code, res.Body.String())
	}
	for _, cookie := range cookies[1:] {
		if res := call(http.MethodPost, "/api/rooms/join", map[string]any{"code": selected.Code}, cookie); res.Code != http.StatusConflict {
			t.Fatalf("关闭后仍能通过房间码加入：%d %s", res.Code, res.Body.String())
		}
	}
	if res := call(http.MethodPost, path+"/actions", map[string]any{"type": "start", "operationId": "start-after-close"}, cookies[0]); res.Code != http.StatusConflict {
		t.Fatalf("关闭后仍能开局：%d %s", res.Code, res.Body.String())
	}
	if res := act(cookies[0], "close-again-new-id"); res.Code != http.StatusConflict {
		t.Fatalf("关闭后新操作再次成功：%d %s", res.Code, res.Body.String())
	}
	if res := call(http.MethodPatch, path+"/settings", map[string]any{"soupId": soups[0], "mode": "casual", "maxPlayers": 6, "public": true}, cookies[0]); res.Code != http.StatusForbidden {
		t.Fatalf("关闭后仍可改设置：%d %s", res.Code, res.Body.String())
	}
	for _, item := range []struct {
		path string
		want bool
	}{{"/api/rooms", false}, {"/api/rooms/mine", true}} {
		res := call(http.MethodGet, item.path, nil, cookies[0])
		if res.Code != http.StatusOK {
			t.Fatalf("房间列表读取失败：%d %s", res.Code, res.Body.String())
		}
		found := bytes.Contains(res.Body.Bytes(), []byte(fmt.Sprintf(`"id":%d`, selected.ID)))
		if found != item.want {
			t.Fatalf("关闭房间列表可见性错误：%s found=%v", item.path, found)
		}
	}
	newRoom := create(rejectedSoup)
	if newRoom.Code != http.StatusCreated {
		t.Fatalf("关闭等待房后未释放建房名额：%d %s", newRoom.Code, newRoom.Body.String())
	}
	var latest createdRoom
	if err := json.Unmarshal(newRoom.Body.Bytes(), &latest); err != nil {
		t.Fatal(err)
	}
	// 历史关闭房间过多时，未结束房间仍应排在“我的房间”前面。
	_, err = db.Exec(ctx, `WITH history AS (
		INSERT INTO rooms(code,owner_id,host_id,soup_id,mode,max_players,public,status,
			soup_title,soup_surface,soup_answer,soup_genre,soup_difficulty,soup_horror)
		SELECT lpad(to_hex(n),32,'0'),$1,$1,$2,'casual',6,FALSE,'closed',
			'历史题','历史汤面','历史汤底','本格',2,1
		FROM generate_series(1,55) AS n RETURNING id
	) INSERT INTO room_members(room_id,user_id,role)
	SELECT id,$1,'host' FROM history`, users[0], soups[0])
	if err != nil {
		t.Fatal(err)
	}
	res := call(http.MethodGet, "/api/rooms/mine", nil, cookies[0])
	if res.Code != http.StatusOK {
		t.Fatalf("历史房间列表读取失败：%d %s", res.Code, res.Body.String())
	}
	var items []struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &items); err != nil || len(items) != 50 {
		t.Fatalf("历史房间列表长度错误：%d %v", len(items), err)
	}
	for i := 0; i < 5; i++ {
		if items[i].Status == "closed" {
			t.Fatalf("未结束房间被历史记录挤出：第 %d 条为已关闭", i)
		}
	}
	// 已结束房间的原成员仍可用房间码回看，避免关闭逻辑改变既有行为。
	if _, err := db.Exec(ctx, `UPDATE rooms SET status='finished' WHERE id=$1`, latest.ID); err != nil {
		t.Fatal(err)
	}
	if res := call(http.MethodPost, "/api/rooms/join", map[string]any{"code": latest.Code}, cookies[0]); res.Code != http.StatusOK {
		t.Fatalf("已结束房间的原成员不能回看：%d %s", res.Code, res.Body.String())
	}
}
