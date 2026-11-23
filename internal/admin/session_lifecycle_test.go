package admin_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"turtle-soup/internal/account"
)

const testSessionName = "session_lifecycle"
const testSessionPassword = "fixture-old-password"

// 用真实登录创建会话，并从另一个请求验证原 Cookie 是否仍然有效。
func TestSessionLifecycleWithPostgres(t *testing.T) {
	t.Run("expired", func(t *testing.T) {
		s, user := newSessionLifecycleTest(t)
		cookie := loginTestSession(t, s, "first", testSessionPassword)
		checkTestSession(t, s, "first", http.StatusOK, user)

		// 直接移动这一条会话的过期时间，避免依赖等待或本机时钟。
		hash := sha256.Sum256([]byte(cookie.Value))
		changed, err := s.db.Exec(context.Background(), `UPDATE sessions
			SET expires_at=NOW()-INTERVAL '1 second' WHERE token_hash=$1`, hash[:])
		if err != nil || changed.RowsAffected() != 1 {
			t.Fatalf("设置测试会话过期失败：影响行数 %d，错误 %v", changed.RowsAffected(), err)
		}
		checkTestSession(t, s, "first", http.StatusUnauthorized, user)
	})

	t.Run("logout_one_device", func(t *testing.T) {
		s, user := newSessionLifecycleTest(t)
		first := loginTestSession(t, s, "first", testSessionPassword)
		loginTestSession(t, s, "second", testSessionPassword)
		checkTestSession(t, s, "first", http.StatusOK, user)
		checkTestSession(t, s, "second", http.StatusOK, user)

		res := s.call(http.MethodPost, "/api/logout", "first", nil)
		if res.Code != http.StatusNoContent {
			t.Fatalf("退出登录：HTTP %d %s", res.Code, res.Body.String())
		}
		cookies := res.Result().Cookies()
		if len(cookies) != 1 || cookies[0].Name != first.Name || cookies[0].Path != first.Path || cookies[0].Value != "" || cookies[0].MaxAge >= 0 {
			t.Fatal("退出时没有清除登录 Cookie")
		}
		checkTestSession(t, s, "first", http.StatusUnauthorized, user)
		checkTestSession(t, s, "second", http.StatusOK, user)
	})

	t.Run("password_change_all_devices", func(t *testing.T) {
		s, user := newSessionLifecycleTest(t)
		loginTestSession(t, s, "first", testSessionPassword)
		loginTestSession(t, s, "second", testSessionPassword)
		checkTestSession(t, s, "first", http.StatusOK, user)
		checkTestSession(t, s, "second", http.StatusOK, user)

		const newPassword = "fixture-new-password"
		path := fmt.Sprintf("/api/admin/users/%d", user)
		res := s.call(http.MethodPatch, path, "admin", map[string]string{"password": newPassword})
		if res.Code != http.StatusOK {
			t.Fatalf("管理员修改密码：HTTP %d %s", res.Code, res.Body.String())
		}
		checkTestSession(t, s, "first", http.StatusUnauthorized, user)
		checkTestSession(t, s, "second", http.StatusUnauthorized, user)
		oldLogin := s.call(http.MethodPost, "/api/login", "", sessionTestCredentials(testSessionPassword))
		if oldLogin.Code != http.StatusUnauthorized || len(oldLogin.Result().Cookies()) != 0 {
			t.Fatalf("旧密码仍可获取会话：HTTP %d %s", oldLogin.Code, oldLogin.Body.String())
		}
		loginTestSession(t, s, "fresh", newPassword)
		checkTestSession(t, s, "fresh", http.StatusOK, user)
		checkTestSession(t, s, "admin", http.StatusOK, s.users["admin"])
	})
}

func newSessionLifecycleTest(t *testing.T) (*moderationTest, int64) {
	t.Helper()
	s := newModerationTest(t)
	account.NewHandler(account.NewPGStore(s.db), false).Register(s.router)
	res := s.call(http.MethodPost, "/api/register", "", sessionTestCredentials(testSessionPassword))
	var user account.User
	if res.Code != http.StatusCreated || json.Unmarshal(res.Body.Bytes(), &user) != nil || user.ID < 1 {
		t.Fatalf("注册测试账号：HTTP %d %s", res.Code, res.Body.String())
	}
	return s, user.ID
}

func sessionTestCredentials(password string) map[string]string {
	return map[string]string{"username": testSessionName, "password": password}
}

func loginTestSession(t *testing.T, s *moderationTest, device, password string) *http.Cookie {
	t.Helper()
	res := s.call(http.MethodPost, "/api/login", "", sessionTestCredentials(password))
	if res.Code != http.StatusOK {
		t.Fatalf("设备 %s 登录：HTTP %d %s", device, res.Code, res.Body.String())
	}
	cookies := res.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "turtle_session" || cookies[0].Value == "" {
		t.Fatalf("设备 %s 缺少登录 Cookie", device)
	}
	s.cookie[device] = cookies[0]
	return cookies[0]
}

func checkTestSession(t *testing.T, s *moderationTest, device string, status int, userID int64) {
	t.Helper()
	res := s.call(http.MethodGet, "/api/me", device, nil)
	if res.Code != status {
		t.Fatalf("设备 %s 的会话应返回 HTTP %d，实际为 %d %s", device, status, res.Code, res.Body.String())
	}
	if status == http.StatusOK {
		var user account.User
		if json.Unmarshal(res.Body.Bytes(), &user) != nil || user.ID != userID {
			t.Fatalf("设备 %s 的会话未返回账号 %d：%s", device, userID, res.Body.String())
		}
	}
}
