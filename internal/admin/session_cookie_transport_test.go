package admin_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"
	"time"

	"turtle-soup/internal/account"
)

// 使用真实会话和同一个 Cookie Jar 对照 HTTPS、HTTP 的发送行为。
func TestSessionCookieTransportWithPostgres(t *testing.T) {
	for _, test := range []struct {
		name      string
		secure    bool
		plainCode int
	}{{"secure", true, http.StatusUnauthorized}, {"development", false, http.StatusOK}} {
		t.Run(test.name, func(t *testing.T) {
			f := newModerationTest(t)
			account.NewHandler(account.NewPGStore(f.db), test.secure).Register(f.router)
			https := httptest.NewTLSServer(f.router)
			defer https.Close()
			plain := httptest.NewServer(f.router)
			defer plain.Close()

			jar, err := cookiejar.New(nil)
			if err != nil {
				t.Fatal(err)
			}
			secureClient, plainClient := https.Client(), plain.Client()
			secureClient.Jar, plainClient.Jar = jar, jar
			secureClient.Timeout, plainClient.Timeout = 5*time.Second, 5*time.Second
			// httptest 将 example.com 的请求直接送到本机测试服务器。
			// 使用测试域名避免 Cookie Jar 把明文 localhost 当作安全来源。
			const secureURL = "https://session.example.com"
			const plainURL = "http://session.example.com"
			creds, err := json.Marshal(sessionTestCredentials(testSessionPassword))
			if err != nil {
				t.Fatal(err)
			}
			cookieSessionRequest(t, secureClient, http.MethodPost, secureURL+"/api/register", creds, http.StatusCreated)
			login := cookieSessionRequest(t, secureClient, http.MethodPost, secureURL+"/api/login", creds, http.StatusOK)
			if login.TLS == nil {
				t.Fatal("登录请求未建立 TLS 连接")
			}
			issued := checkSessionCookieFlags(t, login, test.secure)
			if issued.Value == "" || issued.MaxAge <= 0 {
				t.Fatal("登录应签发有有效期的会话 Cookie")
			}
			secureMe := cookieSessionRequest(t, secureClient, http.MethodGet, secureURL+"/api/me", nil, http.StatusOK)
			plainMe := cookieSessionRequest(t, plainClient, http.MethodGet, plainURL+"/api/me", nil, test.plainCode)
			if plainMe.TLS != nil {
				t.Fatal("明文对照意外使用 TLS")
			}
			logout := cookieSessionRequest(t, secureClient, http.MethodPost, secureURL+"/api/logout", nil, http.StatusNoContent)
			cleared := checkSessionCookieFlags(t, logout, test.secure)
			if cleared.Value != "" || cleared.MaxAge >= 0 {
				t.Fatal("退出应清除同名会话 Cookie")
			}
			if len(jar.Cookies(logout.Request.URL)) != 0 {
				t.Fatal("退出后 Cookie Jar 仍保留会话")
			}
			loggedOut := cookieSessionRequest(t, secureClient, http.MethodGet, secureURL+"/api/me", nil, http.StatusUnauthorized)
			t.Logf("Secure=%t HttpOnly=%t SameSite=Strict Path=%s 登录 Max-Age=%d 退出 Max-Age=%d；HTTPS /me=%d HTTP /me=%d 退出=%d 退出后 HTTPS /me=%d",
				issued.Secure, issued.HttpOnly, issued.Path, issued.MaxAge, cleared.MaxAge,
				secureMe.StatusCode, plainMe.StatusCode, logout.StatusCode, loggedOut.StatusCode)
		})
	}
}

func cookieSessionRequest(t *testing.T, client *http.Client, method, url string, data []byte, want int) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, url, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	res, err := client.Do(request)
	if err != nil {
		t.Fatalf("%s %s：%v", method, url, err)
	}
	defer res.Body.Close()
	if _, err := io.Copy(io.Discard, res.Body); err != nil {
		t.Fatalf("读取测试响应：%v", err)
	}
	if res.StatusCode != want {
		t.Fatalf("%s %s 应返回 HTTP %d，实际 %d", method, url, want, res.StatusCode)
	}
	return res
}

func checkSessionCookieFlags(t *testing.T, res *http.Response, secure bool) *http.Cookie {
	t.Helper()
	cookies := res.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("预期一个会话 Cookie，实际 %d", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != "turtle_session" || cookie.Path != "/" || cookie.Domain != "" ||
		cookie.Secure != secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("会话 Cookie 属性不符：name=%s path=%s domain=%s Secure=%t HttpOnly=%t SameSite=%v",
			cookie.Name, cookie.Path, cookie.Domain, cookie.Secure, cookie.HttpOnly, cookie.SameSite)
	}
	return cookie
}
