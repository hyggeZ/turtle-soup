package forum_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"turtle-soup/internal/account"
	"turtle-soup/internal/forum"
	"turtle-soup/migrations"
)

// 新评论插入后，已取得的游标仍能读到更早的评论。
func TestCommentsCursorAcrossInsert(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("需要 TEST_DATABASE_URL")
	}
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	db, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := migrations.Apply(ctx, db); err != nil {
		t.Fatal(err)
	}
	var userID, soupID int64
	name := fmt.Sprintf("test_comment_page_%d", time.Now().UnixNano())
	if err := db.QueryRow(ctx, `INSERT INTO users(username,password_hash) VALUES($1,'test') RETURNING id`, name).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, userID) })
	if err := db.QueryRow(ctx, `INSERT INTO soups(author_id,title,surface,answer,genre,difficulty,horror)
		VALUES($1,'分页测试','分页测试汤面','分页测试汤底','本格',1,0) RETURNING id`, userID).Scan(&soupID); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `INSERT INTO comments(soup_id,author_id,body)
		SELECT $1,$2,'评论 ' || n::text FROM generate_series(1,101) n`, soupID, userID)
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	forum.NewHandler(forum.NewStore(db), account.NewHandler(account.NewPGStore(db), false)).Register(router)
	get := func(before string) []forum.Comment {
		t.Helper()
		path := fmt.Sprintf("/api/soups/%d/comments", soupID)
		if before != "" {
			path += "?before=" + before
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("评论分页返回 %d: %s", response.Code, response.Body.String())
		}
		var items []forum.Comment
		if err := json.Unmarshal(response.Body.Bytes(), &items); err != nil {
			t.Fatal(err)
		}
		return items
	}
	first := get("")
	if len(first) != 100 || first[0].ID <= first[99].ID {
		t.Fatalf("第一页数量或顺序错误: %d", len(first))
	}
	_, err = db.Exec(ctx, `INSERT INTO comments(soup_id,author_id,body) VALUES($1,$2,'新评论')`, soupID, userID)
	if err != nil {
		t.Fatal(err)
	}
	second := get(fmt.Sprint(first[99].ID))
	if len(second) != 1 || second[0].ID >= first[99].ID {
		t.Fatalf("插入后的第二页错误: %+v", second)
	}
	if fresh := get(""); fresh[0].Body != "新评论" {
		t.Fatalf("新评论未显示在首位: %+v", fresh[0])
	}
	for _, before := range []string{"0", "bad"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/soups/%d/comments?before=%s", soupID, before), nil))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("非法游标 %q 返回 %d", before, response.Code)
		}
	}
}
