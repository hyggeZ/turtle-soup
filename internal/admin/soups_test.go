package admin_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"turtle-soup/internal/account"
	"turtle-soup/internal/admin"
	"turtle-soup/internal/agent"
	"turtle-soup/internal/forum"
	"turtle-soup/internal/room"
	"turtle-soup/migrations"
)

type moderationTest struct {
	t      *testing.T
	db     *pgxpool.Pool
	router *gin.Engine
	users  map[string]int64
	cookie map[string]*http.Cookie
	closed []int64
	codes  int64
}

func newModerationTest(t *testing.T) *moderationTest {
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
	schema := "moderation_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if _, err := adminDB.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = adminDB.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
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
	state := &moderationTest{t: t, db: db, users: make(map[string]int64), cookie: make(map[string]*http.Cookie)}
	for index, role := range []string{"user", "admin", "super_admin"} {
		name := "moderation_" + role
		var id int64
		err := db.QueryRow(ctx, `INSERT INTO users(username,password_hash,role,is_admin)
			VALUES($1,'test',$2,$3) RETURNING id`, name, role, role != "user").Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		state.users[role] = id
		token := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{byte(index + 1)}, 32))
		hash := sha256.Sum256([]byte(token))
		if _, err := db.Exec(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at)
			VALUES($1,$2,NOW()+INTERVAL '1 hour')`, hash[:], id); err != nil {
			t.Fatal(err)
		}
		state.cookie[role] = &http.Cookie{Name: "turtle_session", Value: token}
	}
	gin.SetMode(gin.TestMode)
	state.router = gin.New()
	auth := account.NewHandler(account.NewPGStore(db), false)
	rooms := room.NewHandler(db, auth)
	admins := admin.NewHandler(db, auth, nil)
	admins.SetRoomNotifier(func(id int64) { state.closed = append(state.closed, id) })
	admins.Register(state.router)
	rooms.Register(state.router)
	forum.NewHandler(forum.NewStore(db), auth).Register(state.router)
	return state
}

func (s *moderationTest) call(method, path, role string, body any) *httptest.ResponseRecorder {
	s.t.Helper()
	var data []byte
	if body != nil {
		data, _ = json.Marshal(body)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(data))
	request.Header.Set("Content-Type", "application/json")
	if cookie := s.cookie[role]; cookie != nil {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	s.router.ServeHTTP(response, request)
	return response
}

func (s *moderationTest) soup(role, title, answer string) int64 {
	s.t.Helper()
	var id int64
	err := s.db.QueryRow(context.Background(), `INSERT INTO soups(author_id,title,surface,answer,genre,difficulty,horror)
		VALUES($1,$2,'这是一道待审核的测试题目',$3,'本格',2,1) RETURNING id`, s.users[role], title, answer).Scan(&id)
	if err != nil {
		s.t.Fatal(err)
	}
	return id
}

func (s *moderationTest) room(soup int64, status string) int64 {
	s.t.Helper()
	s.codes++
	code := fmt.Sprintf("%032x", s.codes)
	var id int64
	err := s.db.QueryRow(context.Background(), `INSERT INTO rooms(code,owner_id,host_id,soup_id,mode,max_players,public,status,
		soup_title,soup_surface,soup_answer,soup_genre,soup_difficulty,soup_horror)
		VALUES($1,$2,$2,$3,'casual',6,TRUE,$4,'旧标题','旧汤面','保留的汤底','本格',2,1) RETURNING id`,
		code, s.users["user"], soup, status).Scan(&id)
	if err != nil {
		s.t.Fatal(err)
	}
	_, err = s.db.Exec(context.Background(), `INSERT INTO room_members(room_id,user_id,role,seen_answer)
		VALUES($1,$2,'host',TRUE)`, id, s.users["user"])
	if err != nil {
		s.t.Fatal(err)
	}
	return id
}

func soupIDs(t *testing.T, response *httptest.ResponseRecorder) map[int64]string {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("题目列表状态 %d: %s", response.Code, response.Body.String())
	}
	var items []struct {
		ID     int64  `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &items); err != nil {
		t.Fatal(err)
	}
	result := make(map[int64]string)
	for _, item := range items {
		result[item.ID] = item.Status
	}
	return result
}

func TestSoupModerationPermissionAndSnapshots(t *testing.T) {
	s := newModerationTest(t)
	ctx := context.Background()
	userSoup := s.soup("user", "普通用户的汤", "不能从后台列表看到的秘密汤底")
	adminSoup := s.soup("admin", "管理员的汤", "管理员汤底")
	superSoup := s.soup("super_admin", "超级管理员的汤", "超级管理员汤底")
	waiting := s.room(userSoup, "waiting")
	active := s.room(userSoup, "active")
	ai := agent.NewStore(s.db)
	aiID, err := ai.Create(ctx, s.users["admin"], userSoup)
	if err != nil {
		t.Fatal(err)
	}

	if res := s.call(http.MethodGet, "/api/admin/soups", "", nil); res.Code != http.StatusUnauthorized {
		t.Fatalf("匿名进入后台：%d", res.Code)
	}
	if res := s.call(http.MethodGet, "/api/admin/soups", "user", nil); res.Code != http.StatusForbidden {
		t.Fatalf("普通用户进入后台：%d", res.Code)
	}
	adminList := s.call(http.MethodGet, "/api/admin/soups", "admin", nil)
	visible := soupIDs(t, adminList)
	if visible[userSoup] != "visible" || visible[adminSoup] != "" || visible[superSoup] != "" {
		t.Fatalf("普通管理员列表越权：%v", visible)
	}
	superList := s.call(http.MethodGet, "/api/admin/soups", "super_admin", nil)
	visible = soupIDs(t, superList)
	if visible[userSoup] != "visible" || visible[adminSoup] != "visible" || visible[superSoup] != "" {
		t.Fatalf("超级管理员列表越权：%v", visible)
	}
	if strings.Contains(superList.Body.String(), "秘密汤底") || strings.Contains(superList.Body.String(), "管理员汤底") {
		t.Fatal("后台题目列表泄露汤底")
	}
	path := fmt.Sprintf("/api/admin/soups/%d", adminSoup)
	if res := s.call(http.MethodPatch, path, "admin", map[string]bool{"hidden": true}); res.Code != http.StatusNotFound {
		t.Fatalf("普通管理员隐藏同级作品：%d", res.Code)
	}
	if res := s.call(http.MethodPatch, path, "super_admin", map[string]bool{"hidden": true}); res.Code != http.StatusOK {
		t.Fatalf("超级管理员隐藏管理员作品：%d %s", res.Code, res.Body.String())
	}

	path = fmt.Sprintf("/api/admin/soups/%d", userSoup)
	response := s.call(http.MethodPatch, path, "admin", map[string]bool{"hidden": true})
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"closedRooms":1`) {
		t.Fatalf("隐藏题目失败：%d %s", response.Code, response.Body.String())
	}
	if len(s.closed) != 1 || s.closed[0] != waiting {
		t.Fatalf("实时关闭通知错误：%v", s.closed)
	}
	var waitingStatus, activeStatus string
	var closedAt *time.Time
	err = s.db.QueryRow(ctx, `SELECT status,closed_at FROM rooms WHERE id=$1`, waiting).Scan(&waitingStatus, &closedAt)
	if err != nil || waitingStatus != "closed" || closedAt == nil {
		t.Fatalf("等待房未关闭：%s %v %v", waitingStatus, closedAt, err)
	}
	err = s.db.QueryRow(ctx, `SELECT status FROM rooms WHERE id=$1`, active).Scan(&activeStatus)
	if err != nil || activeStatus != "active" {
		t.Fatalf("进行中房间被影响：%s %v", activeStatus, err)
	}
	if res := s.call(http.MethodGet, fmt.Sprintf("/api/rooms/%d", waiting), "user", nil); res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"status":"closed"`) {
		t.Fatalf("房间关闭状态不可见：%d %s", res.Code, res.Body.String())
	}
	if res := s.call(http.MethodGet, fmt.Sprintf("/api/rooms/%d", active), "user", nil); res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"status":"active"`) {
		t.Fatalf("已开局快照不可读：%d %s", res.Code, res.Body.String())
	}
	var savedAnswer string
	if err := s.db.QueryRow(ctx, `SELECT answer FROM ai_games WHERE id=$1`, aiID).Scan(&savedAnswer); err != nil || savedAnswer != "不能从后台列表看到的秘密汤底" {
		t.Fatalf("AI 旧局快照被改写：%q %v", savedAnswer, err)
	}
	if _, err := ai.Create(ctx, s.users["super_admin"], userSoup); !errors.Is(err, agent.ErrNotFound) {
		t.Fatalf("隐藏题目仍能开始单人局：%v", err)
	}
	if res := s.call(http.MethodGet, fmt.Sprintf("/api/soups/%d", userSoup), "", nil); res.Code != http.StatusNotFound {
		t.Fatalf("隐藏题目详情仍公开：%d", res.Code)
	}
	if res := s.call(http.MethodGet, "/api/soups", "", nil); res.Code != http.StatusOK || strings.Contains(res.Body.String(), "普通用户的汤") {
		t.Fatalf("隐藏题目仍出现在广场：%d %s", res.Code, res.Body.String())
	}
	if res := s.call(http.MethodGet, fmt.Sprintf("/api/soups/%d/comments", userSoup), "", nil); res.Code != http.StatusNotFound {
		t.Fatalf("隐藏题目评论仍公开：%d", res.Code)
	}
	if res := s.call(http.MethodPost, fmt.Sprintf("/api/soups/%d/comments", userSoup), "user", map[string]string{"body": "这是隐藏后提交的评论"}); res.Code != http.StatusNotFound {
		t.Fatalf("隐藏题目仍可评论：%d %s", res.Code, res.Body.String())
	}
	if res := s.call(http.MethodPost, fmt.Sprintf("/api/soups/%d/answer", userSoup), "admin", nil); res.Code != http.StatusNotFound {
		t.Fatalf("隐藏题目仍能公开汤底：%d %s", res.Code, res.Body.String())
	}
	if res := s.call(http.MethodPost, "/api/rooms", "user", map[string]any{"soupId": userSoup, "mode": "casual", "maxPlayers": 6, "public": true, "hostSelf": true}); res.Code != http.StatusNotFound {
		t.Fatalf("隐藏题目仍能建房：%d %s", res.Code, res.Body.String())
	}
	edit := map[string]any{"title": "编辑后标题", "surface": "完整的测试汤面", "answer": "完整的测试汤底", "genre": "本格", "difficulty": 2, "horror": 1}
	if res := s.call(http.MethodPatch, fmt.Sprintf("/api/soups/%d", userSoup), "user", edit); res.Code != http.StatusConflict {
		t.Fatalf("隐藏题目编辑未清晰拒绝：%d %s", res.Code, res.Body.String())
	}
	_, err = forum.NewStore(s.db).Update(ctx, s.users["user"], userSoup,
		"编辑后标题", "完整的测试汤面", "完整的测试汤底", "本格", 2, 1)
	if !errors.Is(err, forum.ErrModerated) {
		t.Fatalf("隐藏题目可被作者编辑：%v", err)
	}
	visible = soupIDs(t, s.call(http.MethodGet, "/api/admin/soups?status=hidden", "admin", nil))
	if visible[userSoup] != "hidden" || visible[adminSoup] != "" {
		t.Fatalf("后台隐藏状态筛选错误：%v", visible)
	}
	if res := s.call(http.MethodPatch, path, "admin", map[string]bool{"hidden": false}); res.Code != http.StatusOK {
		t.Fatalf("恢复题目失败：%d %s", res.Code, res.Body.String())
	}
	if res := s.call(http.MethodGet, fmt.Sprintf("/api/soups/%d", userSoup), "", nil); res.Code != http.StatusOK {
		t.Fatalf("恢复后题目不可见：%d", res.Code)
	}
	err = s.db.QueryRow(ctx, `SELECT status FROM rooms WHERE id=$1`, waiting).Scan(&waitingStatus)
	if err != nil || waitingStatus != "closed" {
		t.Fatal("恢复题目不应自动重开已关闭房间")
	}
	if res := s.call(http.MethodPatch, path, "admin", map[string]bool{"hidden": true}); res.Code != http.StatusOK {
		t.Fatalf("再次隐藏失败：%d", res.Code)
	}
	if res := s.call(http.MethodDelete, fmt.Sprintf("/api/soups/%d", userSoup), "user", nil); res.Code != http.StatusNoContent {
		t.Fatalf("作者不能删除已隐藏题目：%d %s", res.Code, res.Body.String())
	}
	if res := s.call(http.MethodPatch, path, "super_admin", map[string]bool{"hidden": false}); res.Code != http.StatusConflict {
		t.Fatalf("后台恢复作者已删除题目：%d", res.Code)
	}
	visible = soupIDs(t, s.call(http.MethodGet, "/api/admin/soups?status=deleted", "admin", nil))
	if visible[userSoup] != "deleted" {
		t.Fatalf("作者删除状态未显示：%v", visible)
	}
}

// 并发隐藏与建房必须串行：最终不能留下可开局的隐藏题目等待房。
func TestHideSoupWhileCreatingRooms(t *testing.T) {
	s := newModerationTest(t)
	for index := 0; index < 10; index++ {
		soupID := s.soup("user", fmt.Sprintf("竞态题目 %d", index), "竞态汤底")
		start := make(chan struct{})
		var create, hide *httptest.ResponseRecorder
		var group sync.WaitGroup
		group.Add(2)
		go func() {
			defer group.Done()
			<-start
			create = s.call(http.MethodPost, "/api/rooms", "user", map[string]any{
				"soupId": soupID, "mode": "casual", "maxPlayers": 6, "public": true, "hostSelf": true,
			})
		}()
		go func() {
			defer group.Done()
			<-start
			hide = s.call(http.MethodPatch, fmt.Sprintf("/api/admin/soups/%d", soupID), "admin", map[string]bool{"hidden": true})
		}()
		close(start)
		group.Wait()
		if hide.Code != http.StatusOK || create.Code != http.StatusCreated && create.Code != http.StatusNotFound {
			t.Fatalf("并发操作返回异常：建房 %d，隐藏 %d", create.Code, hide.Code)
		}
		var waiting int
		if err := s.db.QueryRow(context.Background(), `SELECT count(*) FROM rooms WHERE soup_id=$1 AND status='waiting'`, soupID).Scan(&waiting); err != nil || waiting != 0 {
			t.Fatalf("隐藏后仍留有等待房：第 %d 次，数量 %d，错误 %v", index, waiting, err)
		}
	}
}
