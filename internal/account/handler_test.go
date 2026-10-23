package account

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

type memoryStore struct {
	users    map[string]User
	sessions map[string]int64
}

func newMemoryStore() *memoryStore {
	return &memoryStore{users: map[string]User{}, sessions: map[string]int64{}}
}

func (s *memoryStore) CreateUser(_ context.Context, name, hash string) (User, error) {
	if _, exists := s.users[name]; exists {
		return User{}, ErrUsernameTaken
	}
	user := User{ID: int64(len(s.users) + 1), Username: name, PasswordHash: hash}
	s.users[name] = user
	return user, nil
}

func (s *memoryStore) FindUser(_ context.Context, name string) (User, error) {
	user, exists := s.users[name]
	if !exists {
		return User{}, ErrNotFound
	}
	return user, nil
}

func (s *memoryStore) CreateSession(_ context.Context, id int64, hash []byte, _ time.Time) error {
	s.sessions[string(hash)] = id
	return nil
}

func (s *memoryStore) FindSessionUser(_ context.Context, hash []byte) (User, error) {
	id, exists := s.sessions[string(hash)]
	if !exists {
		return User{}, ErrNotFound
	}
	for _, user := range s.users {
		if user.ID == id {
			return user, nil
		}
	}
	return User{}, ErrNotFound
}

func (s *memoryStore) DeleteSession(_ context.Context, hash []byte) error {
	delete(s.sessions, string(hash))
	return nil
}

func TestRegisterLoginAndLogout(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	NewHandler(newMemoryStore(), false).Register(router)

	post := func(path string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(payload))
		request.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			request.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}

	data := map[string]string{"username": "Alice", "password": "correct-password"}
	registered := post("/api/register", data, nil)
	if registered.Code != http.StatusCreated || !strings.Contains(registered.Body.String(), `"username":"alice"`) {
		t.Fatalf("注册失败: %d %s", registered.Code, registered.Body.String())
	}
	if strings.Contains(registered.Body.String(), "password") {
		t.Fatal("注册响应泄露了密码哈希")
	}

	wrong := post("/api/login", map[string]string{"username": "Alice", "password": "wrong-password"}, nil)
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("错误密码应被拒绝，实际状态码 %d", wrong.Code)
	}

	loggedIn := post("/api/login", data, nil)
	if loggedIn.Code != http.StatusOK || len(loggedIn.Result().Cookies()) != 1 {
		t.Fatalf("登录失败: %d %s", loggedIn.Code, loggedIn.Body.String())
	}
	cookie := loggedIn.Result().Cookies()[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("会话 Cookie 缺少安全属性")
	}

	meRequest := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	meRequest.AddCookie(cookie)
	me := httptest.NewRecorder()
	router.ServeHTTP(me, meRequest)
	if me.Code != http.StatusOK || !strings.Contains(me.Body.String(), `"id":1`) {
		t.Fatalf("无法恢复登录身份: %d %s", me.Code, me.Body.String())
	}

	left := post("/api/logout", nil, cookie)
	if left.Code != http.StatusNoContent {
		t.Fatalf("退出失败: %d", left.Code)
	}
	after := httptest.NewRecorder()
	router.ServeHTTP(after, meRequest)
	if after.Code != http.StatusUnauthorized {
		t.Fatalf("退出后旧会话仍可用: %d", after.Code)
	}
}
