package forum_test

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
	"turtle-soup/internal/forum"
	"turtle-soup/migrations"
)

// 同一账号的并发请求不能越过发题、评论限额；隐藏题目也不能继续收评论。
func TestPublishLimitsAcrossConcurrentRequests(t *testing.T) {
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
	schema := "publish_limit_" + strconv.FormatInt(time.Now().UnixNano(), 10)
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
	config.MaxConns = 12
	db, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := migrations.Apply(ctx, db); err != nil {
		t.Fatal(err)
	}
	// 第二个独立连接池模拟另一台服务实例，额度不能依赖进程内状态。
	other, err := pgxpool.NewWithConfig(ctx, config.Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	users := make([]int64, 2)
	tokens := make([]string, 2)
	for i := range users {
		name := fmt.Sprintf("publisher_%d", i)
		if err := db.QueryRow(ctx, `INSERT INTO users(username,password_hash) VALUES($1,'test') RETURNING id`, name).Scan(&users[i]); err != nil {
			t.Fatal(err)
		}
		tokens[i] = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{byte(i + 1)}, 32))
		hash := sha256.Sum256([]byte(tokens[i]))
		if _, err := db.Exec(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,NOW()+INTERVAL '1 hour')`, hash[:], users[i]); err != nil {
			t.Fatal(err)
		}
	}
	gin.SetMode(gin.TestMode)
	routers := []*gin.Engine{gin.New(), gin.New()}
	forum.NewHandler(forum.NewStore(db), account.NewHandler(account.NewPGStore(db), false)).Register(routers[0])
	forum.NewHandler(forum.NewStore(other), account.NewHandler(account.NewPGStore(other), false)).Register(routers[1])
	call := func(path string, user int, body any, instance int) *httptest.ResponseRecorder {
		data, _ := json.Marshal(body)
		request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(data))
		request.Header.Set("Content-Type", "application/json")
		request.AddCookie(&http.Cookie{Name: "turtle_session", Value: tokens[user]})
		response := httptest.NewRecorder()
		routers[instance].ServeHTTP(response, request)
		return response
	}
	checkLimited := func(response *httptest.ResponseRecorder, maxSeconds int) int {
		t.Helper()
		seconds, err := strconv.Atoi(response.Header().Get("Retry-After"))
		if response.Code != http.StatusTooManyRequests || err != nil || seconds < 1 || seconds > maxSeconds ||
			!bytes.Contains(response.Body.Bytes(), []byte("后重试")) {
			t.Fatalf("限额响应不完整：%d %s retry=%d err=%v", response.Code, response.Body.String(), seconds, err)
		}
		return seconds
	}

	// 十一个请求同时发题；数据库用户行锁应只允许十个提交。
	start := make(chan struct{})
	results := make([]*httptest.ResponseRecorder, 11)
	var group sync.WaitGroup
	for i := range results {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			<-start
			results[i] = call("/api/soups", 0, map[string]any{
				"title": fmt.Sprintf("测试汤 %d", i), "surface": "这里是一段足够长的测试汤面。",
				"answer": "这里是一段足够长的测试汤底。", "genre": "本格", "difficulty": 2, "horror": 0,
			}, i%2)
		}(i)
	}
	close(start)
	group.Wait()
	created, limited := 0, 0
	for _, response := range results {
		switch response.Code {
		case http.StatusCreated:
			created++
		case http.StatusTooManyRequests:
			limited++
			checkLimited(response, 3600)
		default:
			t.Fatalf("并发发题返回 %d: %s", response.Code, response.Body.String())
		}
	}
	if created != 10 || limited != 1 {
		t.Fatalf("并发发题额度失效：成功 %d，限额 %d", created, limited)
	}
	var soupID int64
	if err := db.QueryRow(ctx, `INSERT INTO soups(author_id,title,surface,answer,genre,difficulty,horror)
		VALUES($1,'评论测试题','评论测试汤面','评论测试汤底','本格',2,0) RETURNING id`, users[1]).Scan(&soupID); err != nil {
		t.Fatal(err)
	}
	commentPath := fmt.Sprintf("/api/soups/%d/comments", soupID)
	comment := map[string]any{"body": "正常评论", "spoiler": false}
	start = make(chan struct{})
	results = make([]*httptest.ResponseRecorder, 31)
	for i := range results {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			<-start
			results[i] = call(commentPath, 1, comment, i%2)
		}(i)
	}
	close(start)
	group.Wait()
	created, limited = 0, 0
	for _, response := range results {
		switch response.Code {
		case http.StatusCreated:
			created++
		case http.StatusTooManyRequests:
			limited++
			checkLimited(response, 300)
		default:
			t.Fatalf("并发评论返回 %d: %s", response.Code, response.Body.String())
		}
	}
	if created != 30 || limited != 1 {
		t.Fatalf("并发评论额度失效：成功 %d，限额 %d", created, limited)
	}
	// 十分钟前的评论不占用五分钟额度，但仍会触发每小时额度。
	if _, err := db.Exec(ctx, `INSERT INTO comments(soup_id,author_id,body,created_at)
		SELECT $1,$2,'旧评论',NOW()-INTERVAL '10 minutes' FROM generate_series(1,90)`, soupID, users[1]); err != nil {
		t.Fatal(err)
	}
	if wait := checkLimited(call(commentPath, 1, comment, 0), 3600); wait <= 300 {
		t.Fatalf("每小时评论额度未生效：等待 %d 秒", wait)
	}
	if _, err := db.Exec(ctx, `UPDATE soups SET hidden_at=NOW(),hidden_by=$1 WHERE id=$2`, users[0], soupID); err != nil {
		t.Fatal(err)
	}
	if response := call(commentPath, 1, comment, 1); response.Code != http.StatusNotFound {
		t.Fatalf("隐藏题目仍可发表评论：%d %s", response.Code, response.Body.String())
	}
}
