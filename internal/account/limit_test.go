package account

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

func TestRequestLimiter(t *testing.T) {
	limiter := newRequestLimiter()
	now := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	limiter.now = func() time.Time { return now }
	for i := 0; i < 30; i++ {
		if !limiter.login("192.0.2.1", "king") {
			t.Fatal("登录额度提前用尽")
		}
	}
	if limiter.login("192.0.2.1", "king") || limiter.login("192.0.2.2", "king") || limiter.login("192.0.2.1", "other") {
		t.Fatal("IP 或账号额度未生效")
	}
	for i := 0; i < 10; i++ {
		if !limiter.register("192.0.2.1") {
			t.Fatal("注册额度提前用尽")
		}
	}
	if limiter.register("192.0.2.1") {
		t.Fatal("注册额度未生效")
	}
	now = now.Add(time.Hour)
	if !limiter.login("192.0.2.1", "king") || !limiter.register("192.0.2.1") {
		t.Fatal("窗口到期后额度未恢复")
	}
}

func TestRequestLimiterConcurrentAndBounded(t *testing.T) {
	limiter := newRequestLimiter()
	var group sync.WaitGroup
	results := make(chan bool, 100)
	for i := 0; i < 100; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			results <- limiter.login("198.51.100.1", "king")
		}()
	}
	group.Wait()
	close(results)
	accepted := 0
	for ok := range results {
		if ok {
			accepted++
		}
	}
	if accepted != 30 {
		t.Fatalf("并发下接受 %d 次，预期 30 次", accepted)
	}
	for i := 0; i < maxLimitEntries; i++ {
		key := string(rune(i + 0x1000))
		if !limiter.allow(limitRule{key: key, max: 1, window: time.Hour}) && len(limiter.entries) < maxLimitEntries {
			t.Fatal("内存上限前意外拒绝")
		}
	}
	if len(limiter.entries) > maxLimitEntries || limiter.allow(limitRule{key: "new-key", max: 1, window: time.Hour}) {
		t.Fatal("内存记录数未限制")
	}
}

func TestLoginErrorDoesNotRevealAccountAndSharesBudget(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	_ = router.SetTrustedProxies(nil)
	store := newMemoryStore()
	hash, err := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateUser(context.Background(), "king", string(hash)); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(store, false)
	handler.Register(router)

	call := func(path, name string) *httptest.ResponseRecorder {
		body, err := json.Marshal(map[string]string{"username": name, "password": "wrong-password"})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}
	known := call("/api/login", "king")
	unknown := call("/api/login", "unknown")
	if known.Code != http.StatusUnauthorized || unknown.Code != http.StatusUnauthorized || known.Body.String() != unknown.Body.String() {
		t.Fatalf("登录错误区分了账号是否存在：known=%d %s unknown=%d %s", known.Code, known.Body.String(), unknown.Code, unknown.Body.String())
	}
	for i := 0; i < 28; i++ {
		if response := call("/api/login", "unknown"); response.Code != http.StatusUnauthorized {
			t.Fatalf("第 %d 次登录状态码 %d", i+3, response.Code)
		}
	}
	if response := call("/api/admin/login", "unknown"); response.Code != http.StatusTooManyRequests {
		t.Fatalf("后台登录绕过额度：%d", response.Code)
	}
}
