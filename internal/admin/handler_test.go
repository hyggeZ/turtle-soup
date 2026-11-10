package admin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"turtle-soup/internal/account"
)

type adminSessionStore struct{}

func (adminSessionStore) CreateUser(context.Context, string, string) (account.User, error) {
	return account.User{}, nil
}
func (adminSessionStore) FindUser(context.Context, string) (account.User, error) {
	return account.User{}, nil
}
func (adminSessionStore) CreateSession(context.Context, int64, []byte, time.Time) error { return nil }
func (adminSessionStore) FindSessionUser(context.Context, []byte) (account.User, error) {
	return account.User{ID: 1, Username: "king", Role: "super_admin", IsAdmin: true}, nil
}
func (adminSessionStore) DeleteSession(context.Context, []byte) error { return nil }

// 畸形的大输入应在访问数据库前被拒绝。
func TestInputLimits(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	NewHandler(nil, account.NewHandler(adminSessionStore{}, false), nil).Register(router)
	token := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, sha256.Size))
	cases := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/admin/users?q=" + strings.Repeat("x", 51), ""},
		{http.MethodPost, "/api/admin/users", `{"username":"` + strings.Repeat("x", 5000) + `"}`},
		{http.MethodPatch, "/api/admin/users/2", `{"password":"` + strings.Repeat("x", 5000) + `"}`},
	}
	for _, item := range cases {
		request := httptest.NewRequest(item.method, item.path, strings.NewReader(item.body))
		request.Header.Set("Content-Type", "application/json")
		request.AddCookie(&http.Cookie{Name: "turtle_session", Value: token})
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s %s 返回 %d", item.method, item.path[:min(40, len(item.path))], response.Code)
		}
	}
}
