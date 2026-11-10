package forum_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
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
	"turtle-soup/internal/forum"
	"turtle-soup/migrations"
)

// 公开主页允许匿名读取；资料仅本人修改，账号权限和凭据不会被资料接口改变。
func TestPublicProfileAndOwnerEdit(t *testing.T) {
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
	stamp := time.Now().UnixNano()
	var ids [2]int64
	for i := range ids {
		name := fmt.Sprintf("test_profile_%d_%d", stamp, i)
		if err := db.QueryRow(ctx, `INSERT INTO users(username,password_hash) VALUES($1,'test-hash') RETURNING id`, name).Scan(&ids[i]); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, id := range ids {
			_, _ = db.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, id)
		}
	})
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(token))
	if _, err := db.Exec(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,NOW()+INTERVAL '1 hour')`, hash[:], ids[0]); err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: "turtle_session", Value: token}
	router := gin.New()
	forum.NewHandler(forum.NewStore(db), account.NewHandler(account.NewPGStore(db), false)).Register(router)
	path := fmt.Sprintf("/api/users/%d", ids[0])
	otherPath := fmt.Sprintf("/api/users/%d/profile", ids[1])
	call := func(method, target, body string, login bool) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, target, bytes.NewBufferString(body))
		request.Header.Set("Content-Type", "application/json")
		if login {
			request.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}

	if res := call(http.MethodGet, path, "", false); res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"displayName":""`) {
		t.Fatalf("匿名读取公开主页失败：%d %s", res.Code, res.Body.String())
	}
	if res := call(http.MethodPatch, path+"/profile", `{"displayName":"访客"}`, false); res.Code != http.StatusUnauthorized {
		t.Fatalf("匿名修改资料未被拒绝：%d", res.Code)
	}
	if res := call(http.MethodPatch, otherPath, `{"displayName":"越权"}`, true); res.Code != http.StatusForbidden {
		t.Fatalf("修改他人资料未被拒绝：%d", res.Code)
	}
	if res := call(http.MethodPatch, path+"/profile", `{"displayName":" 小海 ","bio":" 喜欢推理 "}`, true); res.Code != http.StatusNoContent {
		t.Fatalf("本人修改资料失败：%d %s", res.Code, res.Body.String())
	}
	res := call(http.MethodGet, path, "", false)
	var profile forum.Profile
	if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &profile) != nil || profile.DisplayName != "小海" || profile.Bio != "喜欢推理" {
		t.Fatalf("公开资料未更新：%d %s", res.Code, res.Body.String())
	}
	if res := call(http.MethodPatch, path+"/profile", `{"bio":"更新简介"}`, true); res.Code != http.StatusNoContent {
		t.Fatalf("仅修改简介失败：%d", res.Code)
	}
	for _, body := range []string{
		`{"role":"super_admin"}`,
		`{"displayName":"正常","role":"super_admin"}`,
		`{"displayName":"` + strings.Repeat("名", 41) + `"}`,
		`{"bio":"` + strings.Repeat("介", 301) + `"}`,
	} {
		if res := call(http.MethodPatch, path+"/profile", body, true); res.Code != http.StatusBadRequest {
			t.Fatalf("非法资料输入未被拒绝：%s => %d", body, res.Code)
		}
	}
	var username, role, password, displayName, bio string
	if err := db.QueryRow(ctx, `SELECT username,role,password_hash,display_name,bio FROM users WHERE id=$1`, ids[0]).
		Scan(&username, &role, &password, &displayName, &bio); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(username, "test_profile_") || role != "user" || password != "test-hash" || displayName != "小海" || bio != "更新简介" {
		t.Fatalf("公开资料或账号字段被错误修改：%q %q %q %q %q", username, role, password, displayName, bio)
	}
}
