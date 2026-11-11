package agent

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
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"turtle-soup/internal/account"
	"turtle-soup/migrations"
)

// 找回列表只显示本人最多五局进行中对局与最近二十局已结束对局，不携带汤底。
func TestListMineWithPostgres(t *testing.T) {
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
	schema := "solo_list_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if _, err := adminDB.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = adminDB.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })
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

	var users [2]int64
	var cookies [2]*http.Cookie
	for i := range users {
		name := fmt.Sprintf("solo_list_%d", i)
		if err := db.QueryRow(ctx, `INSERT INTO users(username,password_hash)
			VALUES($1,'test') RETURNING id`, name).Scan(&users[i]); err != nil {
			t.Fatal(err)
		}
		token := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{byte(i + 1)}, 32))
		hash := sha256.Sum256([]byte(token))
		if _, err := db.Exec(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at)
			VALUES($1,$2,NOW()+INTERVAL '1 hour')`, hash[:], users[i]); err != nil {
			t.Fatal(err)
		}
		cookies[i] = &http.Cookie{Name: "turtle_session", Value: token}
	}
	insertGame := func(user int64, status string, index int) int64 {
		t.Helper()
		var soupID, gameID int64
		err := db.QueryRow(ctx, `INSERT INTO soups(author_id,title,surface,answer,genre,difficulty,horror)
			VALUES($1,$2,'隐藏汤面','绝密汤底','本格',2,1) RETURNING id`, users[1], fmt.Sprintf("汤题 %d", index)).Scan(&soupID)
		if err != nil {
			t.Fatal(err)
		}
		err = db.QueryRow(ctx, `INSERT INTO ai_games(user_id,soup_id,title,surface,answer,status,finished_at)
			VALUES($1,$2,$3,'隐藏汤面','绝密汤底',$4,
			CASE WHEN $4='finished' THEN NOW() ELSE NULL END) RETURNING id`,
			user, soupID, fmt.Sprintf("对局 %d", index), status).Scan(&gameID)
		if err != nil {
			t.Fatal(err)
		}
		return gameID
	}
	active := make([]int64, 5)
	for i := range active {
		active[i] = insertGame(users[0], "active", i)
	}
	finished := make([]int64, 27)
	for i := range finished {
		finished[i] = insertGame(users[0], "finished", i+5)
	}
	other := insertGame(users[1], "active", 99)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	NewHandler(NewStore(db), account.NewHandler(account.NewPGStore(db), false), nil).Register(router)
	call := func(cookie *http.Cookie) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "/api/ai/solo/mine", nil)
		if cookie != nil {
			request.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}
	if response := call(nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("未登录读取对局列表：%d", response.Code)
	}
	response := call(cookies[0])
	if response.Code != http.StatusOK {
		t.Fatalf("本人列表读取失败：%d %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "绝密汤底") || strings.Contains(response.Body.String(), "隐藏汤面") ||
		strings.Contains(response.Body.String(), `"answer"`) || strings.Contains(response.Body.String(), `"surface"`) ||
		strings.Contains(response.Body.String(), `"soupId"`) || strings.Contains(response.Body.String(), `"events"`) {
		t.Fatal("对局概要泄露题目或事件内容")
	}
	var items []GameSummary
	if err := json.Unmarshal(response.Body.Bytes(), &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 25 {
		t.Fatalf("概要数量错误：%d", len(items))
	}
	for i := 0; i < 5; i++ {
		if items[i].ID != active[4-i] || items[i].Status != "active" || items[i].FinishedAt != nil {
			t.Fatalf("进行中对局排序错误：第 %d 条 %+v", i, items[i])
		}
	}
	for i := 0; i < 20; i++ {
		if items[i+5].ID != finished[26-i] || items[i+5].Status != "finished" || items[i+5].FinishedAt == nil {
			t.Fatalf("历史对局排序错误：第 %d 条 %+v", i+5, items[i+5])
		}
	}
	otherResponse := call(cookies[1])
	if otherResponse.Code != http.StatusOK {
		t.Fatalf("其他账号列表读取失败：%d", otherResponse.Code)
	}
	var otherItems []GameSummary
	if err := json.Unmarshal(otherResponse.Body.Bytes(), &otherItems); err != nil {
		t.Fatal(err)
	}
	if len(otherItems) != 1 || otherItems[0].ID != other {
		t.Fatalf("跨账号列表泄露：%+v", otherItems)
	}
	detail := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/ai/solo/"+strconv.FormatInt(other, 10), nil)
	request.AddCookie(cookies[0])
	router.ServeHTTP(detail, request)
	if detail.Code != http.StatusNotFound {
		t.Fatalf("跨账号读取对局详情：%d", detail.Code)
	}
}

func TestActiveGamesLimitMessage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	(&Handler{}).replyError(context, ErrActiveGamesLimit)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "5 局") {
		t.Fatalf("五局上限提示错误：%d %s", response.Code, response.Body.String())
	}
}
