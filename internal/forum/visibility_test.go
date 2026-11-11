package forum_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
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
	"turtle-soup/internal/forum"
	"turtle-soup/migrations"
)

// 作者可看见自己被隐藏的题目并自行删除；其他人仍不能读取或互动。
func TestHiddenSoupAuthorVisibilityAndRelations(t *testing.T) {
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
	schema := "forum_visibility_" + strconv.FormatInt(time.Now().UnixNano(), 10)
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
	users := make([]int64, 3)
	tokens := make([]string, 3)
	for i := range users {
		role := "user"
		if i == 2 {
			role = "admin"
		}
		if err := db.QueryRow(ctx, `INSERT INTO users(username,password_hash,role,is_admin)
			VALUES($1,'test',$2,$3) RETURNING id`, fmt.Sprintf("visibility_%d", i), role, i == 2).Scan(&users[i]); err != nil {
			t.Fatal(err)
		}
		tokens[i] = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{byte(i + 1)}, 32))
		hash := sha256.Sum256([]byte(tokens[i]))
		if _, err := db.Exec(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at)
			VALUES($1,$2,NOW()+INTERVAL '1 hour')`, hash[:], users[i]); err != nil {
			t.Fatal(err)
		}
	}
	createSoup := func() int64 {
		t.Helper()
		var id int64
		err := db.QueryRow(ctx, `INSERT INTO soups(author_id,title,surface,answer,genre,difficulty,horror)
			VALUES($1,'测试题目','这是一段测试汤面','这是测试汤底','本格',2,1) RETURNING id`, users[0]).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	forum.NewHandler(forum.NewStore(db), account.NewHandler(account.NewPGStore(db), false)).Register(router)
	call := func(method, path string, who int, body []byte) *httptest.ResponseRecorder {
		requestCtx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		request := httptest.NewRequest(method, path, bytes.NewReader(body)).WithContext(requestCtx)
		if who >= 0 {
			request.AddCookie(&http.Cookie{Name: "turtle_session", Value: tokens[who]})
		}
		if body != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}
	first := createSoup()
	if _, err := db.Exec(ctx, `UPDATE soups SET hidden_at=NOW(),hidden_by=$1 WHERE id=$2`, users[2], first); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/soups/%d", first)
	for _, who := range []int{-1, 1} {
		if res := call(http.MethodGet, path, who, nil); res.Code != http.StatusNotFound {
			t.Fatalf("非作者读取隐藏题目：身份 %d，状态 %d", who, res.Code)
		}
	}
	if res := call(http.MethodGet, path, 0, nil); res.Code != http.StatusOK ||
		!strings.Contains(res.Body.String(), `"hidden":true`) || !strings.Contains(res.Body.String(), "这是测试汤底") {
		t.Fatalf("作者不能读取隐藏题目：%d %s", res.Code, res.Body.String())
	}
	list := "/api/soups?author=" + strconv.FormatInt(users[0], 10)
	for _, who := range []int{-1, 1} {
		if res := call(http.MethodGet, list, who, nil); res.Code != http.StatusOK || strings.Contains(res.Body.String(), "测试题目") {
			t.Fatalf("隐藏题目出现在他人列表：身份 %d，结果 %s", who, res.Body.String())
		}
	}
	if res := call(http.MethodGet, list, 0, nil); res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"hidden":true`) {
		t.Fatalf("作者列表未显示隐藏题目：%d %s", res.Code, res.Body.String())
	}
	edit := []byte(`{"title":"新标题","surface":"编辑后的完整汤面","answer":"编辑后的完整汤底","genre":"本格","difficulty":2,"horror":1}`)
	if res := call(http.MethodPatch, path, 0, edit); res.Code != http.StatusConflict {
		t.Fatalf("作者仍能编辑隐藏题目：%d %s", res.Code, res.Body.String())
	}
	for _, relation := range []string{"like", "bookmark"} {
		if res := call(http.MethodPut, path+"/"+relation, 0, nil); res.Code != http.StatusNotFound {
			t.Fatalf("作者仍能互动隐藏题目：%s %d", relation, res.Code)
		}
	}
	if res := call(http.MethodDelete, path, 0, nil); res.Code != http.StatusNoContent {
		t.Fatalf("作者不能删除隐藏题目：%d %s", res.Code, res.Body.String())
	}
	if res := call(http.MethodGet, path, 0, nil); res.Code != http.StatusNotFound {
		t.Fatalf("作者删除后仍可读取题目：%d", res.Code)
	}

	// 隐藏或删除提交前，互动请求可能已读取旧的公开状态；写入时必须再次在同一事务核验。
	for _, relation := range []string{"like", "bookmark"} {
		for _, state := range []string{"hidden", "deleted"} {
			t.Run(relation+"_"+state, func(t *testing.T) {
				id := createSoup()
				tx, err := db.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback(ctx)
				if state == "hidden" {
					_, err = tx.Exec(ctx, `UPDATE soups SET hidden_at=NOW(),hidden_by=$1 WHERE id=$2`, users[2], id)
				} else {
					_, err = tx.Exec(ctx, `UPDATE soups SET deleted_at=NOW() WHERE id=$1`, id)
				}
				if err != nil {
					t.Fatal(err)
				}
				result := make(chan *httptest.ResponseRecorder, 1)
				go func() {
					result <- call(http.MethodPut, fmt.Sprintf("/api/soups/%d/%s", id, relation), 1, nil)
				}()
				// 给请求时间读取事务提交前的公开状态，再完成下架。
				time.Sleep(100 * time.Millisecond)
				if err := tx.Commit(ctx); err != nil {
					t.Fatal(err)
				}
				select {
				case res := <-result:
					if res.Code != http.StatusNotFound {
						t.Fatalf("下架时关系写入成功：%d %s", res.Code, res.Body.String())
					}
				case <-time.After(5 * time.Second):
					t.Fatal("关系请求未在下架后结束")
				}
				table := "soup_likes"
				if relation == "bookmark" {
					table = "soup_bookmarks"
				}
				var count int
				if err := db.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE soup_id=$1`, id).Scan(&count); err != nil || count != 0 {
					t.Fatalf("下架后留下关系：%d %v", count, err)
				}
			})
		}
	}
	visible := createSoup()
	visiblePath := fmt.Sprintf("/api/soups/%d/like", visible)
	if res := call(http.MethodPut, visiblePath, 1, nil); res.Code != http.StatusNoContent {
		t.Fatalf("公开题目无法点赞：%d %s", res.Code, res.Body.String())
	}
	if res := call(http.MethodDelete, visiblePath, 1, nil); res.Code != http.StatusNoContent {
		t.Fatalf("公开题目无法取消点赞：%d %s", res.Code, res.Body.String())
	}
}
