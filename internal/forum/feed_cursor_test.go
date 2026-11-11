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
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"turtle-soup/internal/account"
	"turtle-soup/internal/forum"
	"turtle-soup/migrations"
)

// 新题插入和相同发布时间都不能让下一页漏题或重复。
func TestSoupCursorAcrossInsertAndSameTime(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("需要 TEST_DATABASE_URL")
	}
	ctx := context.Background()
	adminDB, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(adminDB.Close)
	schema := "feed_cursor_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if _, err := adminDB.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = adminDB.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })
	config, err := pgxpool.ParseConfig(databaseURL)
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
	var author, other int64
	for i, id := range []*int64{&author, &other} {
		name := fmt.Sprintf("feed_cursor_%d_%d", time.Now().UnixNano(), i)
		if err := db.QueryRow(ctx, `INSERT INTO users(username,password_hash) VALUES($1,'test') RETURNING id`, name).Scan(id); err != nil {
			t.Fatal(err)
		}
	}
	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	ids := make([]int64, 0, 42)
	for i := 0; i < 42; i++ {
		created := base.Add(-time.Duration(i/3) * time.Second)
		var id int64
		err := db.QueryRow(ctx, `INSERT INTO soups(author_id,title,surface,answer,genre,difficulty,horror,created_at)
			VALUES($1,$2,'测试汤面','测试汤底','本格',2,1,$3) RETURNING id`, author, fmt.Sprintf("游标题目 %d", i), created).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	forum.NewHandler(forum.NewStore(db), account.NewHandler(account.NewPGStore(db), false)).Register(router)
	list := func(values url.Values, token string) []forum.Soup {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/api/soups?"+values.Encode(), nil)
		if token != "" {
			request.AddCookie(&http.Cookie{Name: "turtle_session", Value: token})
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("读取题目失败：%d %s", response.Code, response.Body.String())
		}
		var items []forum.Soup
		if err := json.Unmarshal(response.Body.Bytes(), &items); err != nil {
			t.Fatal(err)
		}
		return items
	}
	values := url.Values{"author": {strconv.FormatInt(author, 10)}}
	first := list(values, "")
	if len(first) != 20 {
		t.Fatalf("首批数量：%d", len(first))
	}
	var newer int64
	if err := db.QueryRow(ctx, `INSERT INTO soups(author_id,title,surface,answer,genre,difficulty,horror,created_at)
		VALUES($1,'新题','测试汤面','测试汤底','本格',2,1,$2) RETURNING id`, author, base.Add(time.Second)).Scan(&newer); err != nil {
		t.Fatal(err)
	}
	seen := make(map[int64]bool)
	all := make([]forum.Soup, 0, 42)
	for _, item := range first {
		seen[item.ID] = true
		all = append(all, item)
	}
	last := first[len(first)-1]
	for page := 0; page < 3; page++ {
		values.Set("before", last.CreatedAt.Format(time.RFC3339Nano))
		values.Set("beforeId", strconv.FormatInt(last.ID, 10))
		next := list(values, "")
		for _, item := range next {
			if seen[item.ID] || item.ID == newer || item.Answer != "" {
				t.Fatalf("游标重复、新题混入或泄露汤底：%+v", item)
			}
			seen[item.ID] = true
			all = append(all, item)
		}
		if len(next) < 20 {
			break
		}
		last = next[len(next)-1]
	}
	if len(all) != 42 {
		t.Fatalf("跨页漏题：拿到 %d，预期 42", len(all))
	}
	for i, item := range all {
		// 每三道题时间相同，ID 倒序是同一时间的第二排序键。
		want := ids[(i/3)*3+2-i%3]
		if item.ID != want {
			t.Fatalf("第 %d 道排序错误：得到 %d，预期 %d", i, item.ID, want)
		}
	}

	// 作者本人在个人列表能管理隐藏题，其他人的作者列表依旧只含公开题。
	hidden := ids[10]
	if _, err := db.Exec(ctx, `UPDATE soups SET hidden_at=NOW(),hidden_by=$1 WHERE id=$2`, other, hidden); err != nil {
		t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	hash := sha256.Sum256([]byte(token))
	if _, err := db.Exec(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at)
		VALUES($1,$2,NOW()+INTERVAL '1 hour')`, hash[:], author); err != nil {
		t.Fatal(err)
	}
	values.Del("before")
	values.Del("beforeId")
	ownerItems := list(values, token)
	publicItems := list(values, "")
	ownerHasHidden := false
	for _, item := range ownerItems {
		if item.ID == hidden && item.Hidden {
			ownerHasHidden = true
		}
	}
	for _, item := range publicItems {
		if item.ID == hidden {
			t.Fatal("隐藏题出现在他人的作者列表")
		}
	}
	if !ownerHasHidden {
		t.Fatal("作者的隐藏题未出现在游标列表")
	}

	for _, input := range []string{
		"before=2026-11-01T00%3A00%3A00Z", "beforeId=1", "before=&beforeId=", "page=", "before=bad&beforeId=1",
		"before=2026-11-01T00%3A00%3A00Z&beforeId=0", "before=2026-11-01T00%3A00%3A00Z&beforeId=1&page=2",
	} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/soups?"+input, nil))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("非法游标未拒绝：%s => %d", input, response.Code)
		}
	}
}
