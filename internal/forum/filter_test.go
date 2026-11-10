package forum_test

import (
	"context"
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

// 先筛选再分页，保证较早发布的匹配题目仍能被选到。
func TestSoupFiltersAcrossPages(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("需要 TEST_DATABASE_URL")
	}
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	db, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := migrations.Apply(ctx, db); err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("test_filter_%d", time.Now().UnixNano())
	var author int64
	if err := db.QueryRow(ctx, `INSERT INTO users(username,password_hash) VALUES($1,'test') RETURNING id`, name).Scan(&author); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, author) })
	for i := 0; i < 50; i++ {
		genre, difficulty, horror := "变格", 4, 0
		if i%2 == 0 {
			genre, difficulty, horror = "本格", 2, 3
		}
		_, err := db.Exec(ctx, `INSERT INTO soups(author_id,title,surface,answer,genre,difficulty,horror)
			VALUES($1,$2,'筛选测试汤面','筛选测试汤底',$3,$4,$5)`, author, fmt.Sprintf("筛选题目%d", i), genre, difficulty, horror)
		if err != nil {
			t.Fatal(err)
		}
	}
	router := gin.New()
	forum.NewHandler(forum.NewStore(db), account.NewHandler(account.NewPGStore(db), false)).Register(router)
	list := func(page int, genre string, difficulty, horror int) []forum.Soup {
		t.Helper()
		query := url.Values{"author": {strconv.FormatInt(author, 10)}, "page": {strconv.Itoa(page)}}
		if genre != "" {
			query.Set("genre", genre)
		}
		if difficulty >= 0 {
			query.Set("difficulty", strconv.Itoa(difficulty))
		}
		if horror >= 0 {
			query.Set("horror", strconv.Itoa(horror))
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/soups?"+query.Encode(), nil))
		if response.Code != http.StatusOK {
			t.Fatalf("筛选失败：status=%d body=%s", response.Code, response.Body.String())
		}
		var items []forum.Soup
		if err := json.Unmarshal(response.Body.Bytes(), &items); err != nil {
			t.Fatal(err)
		}
		return items
	}
	first := list(1, "本格", 2, 3)
	second := list(2, "本格", 2, 3)
	if len(first) != 20 || len(second) != 5 {
		t.Fatalf("筛选后分页错误：第一页=%d，第二页=%d", len(first), len(second))
	}
	seen := map[int64]bool{}
	for _, item := range append(first, second...) {
		if item.Genre != "本格" || item.Difficulty != 2 || item.Horror != 3 || item.Answer != "" || seen[item.ID] {
			t.Fatalf("题目筛选、去重或汤底隐藏错误：%+v", item)
		}
		seen[item.ID] = true
	}
	if len(list(1, "变格", 4, 0)) != 20 || len(list(2, "变格", 4, 0)) != 5 {
		t.Fatal("恐怖程度 0 的筛选结果错误")
	}
	if len(list(1, "本格", -1, -1)) != 20 {
		t.Fatal("单独按类型筛选失败")
	}
	for _, input := range []string{"genre=其他", "difficulty=0", "difficulty=6", "horror=-1", "horror=6", "page=abc"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/soups?"+input, nil))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("非法筛选未被拒绝：%s => %d", input, response.Code)
		}
	}
}
