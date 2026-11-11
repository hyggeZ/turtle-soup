package admin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"turtle-soup/internal/account"
	"turtle-soup/migrations"
)

// 后台账号列表要能读到前 100 条之外的旧账号。
func TestUsersCursorAcrossInsert(t *testing.T) {
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
	prefix := fmt.Sprintf("page%d", time.Now().UnixNano())
	t.Cleanup(func() { _, _ = db.Exec(context.Background(), `DELETE FROM users WHERE username LIKE $1`, prefix+"%") })
	_, err = db.Exec(ctx, `INSERT INTO users(username,password_hash)
		SELECT $1 || n::text,'test' FROM generate_series(1,101) n`, prefix)
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	NewHandler(db, account.NewHandler(adminSessionStore{}, false), nil).Register(router)
	token := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	type userRow struct {
		ID int64 `json:"id"`
	}
	get := func(before string) []userRow {
		t.Helper()
		path := "/api/admin/users?q=" + prefix
		if before != "" {
			path += "&before=" + before
		}
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.AddCookie(&http.Cookie{Name: "turtle_session", Value: token})
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("账号分页返回 %d: %s", response.Code, response.Body.String())
		}
		var items []userRow
		if err := json.Unmarshal(response.Body.Bytes(), &items); err != nil {
			t.Fatal(err)
		}
		return items
	}
	first := get("")
	if len(first) != 100 || first[0].ID <= first[99].ID {
		t.Fatalf("第一页数量或顺序错误: %d", len(first))
	}
	_, err = db.Exec(ctx, `INSERT INTO users(username,password_hash) VALUES($1,'test')`, prefix+"102")
	if err != nil {
		t.Fatal(err)
	}
	second := get(fmt.Sprint(first[99].ID))
	if len(second) != 1 || second[0].ID >= first[99].ID {
		t.Fatalf("插入后的第二页错误: %+v", second)
	}
	for _, before := range []string{"0", "bad"} {
		request := httptest.NewRequest(http.MethodGet, "/api/admin/users?before="+before, strings.NewReader(""))
		request.AddCookie(&http.Cookie{Name: "turtle_session", Value: token})
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("非法游标 %q 返回 %d", before, response.Code)
		}
	}
}
