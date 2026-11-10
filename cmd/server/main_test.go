package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"turtle-soup/internal/telemetry"
)

func TestHealth(t *testing.T) {
	monitor, err := telemetry.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer monitor.Close()
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	response := httptest.NewRecorder()

	newRouter(monitor).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("状态码应为 200，实际为 %d", response.Code)
	}
	if response.Body.String() != `{"status":"ok"}` {
		t.Fatalf("响应内容不正确：%s", response.Body.String())
	}
}

func TestRouterDoesNotTrustForwardedIPByDefault(t *testing.T) {
	monitor, err := telemetry.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer monitor.Close()
	router := newRouter(monitor)
	router.GET("/client-ip", func(c *gin.Context) { c.String(http.StatusOK, c.ClientIP()) })
	request := httptest.NewRequest(http.MethodGet, "/client-ip", nil)
	request.RemoteAddr = "10.0.0.5:1234"
	request.Header.Set("X-Forwarded-For", "203.0.113.6")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Body.String() != "10.0.0.5" {
		t.Fatalf("代理头伪造了客户端 IP：%s", response.Body.String())
	}
}
