package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"turtle-soup/internal/telemetry"
)

func TestMetricsUsePrivateListener(t *testing.T) {
	collector, server, err := startMetrics("")
	if err != nil || collector != nil || server != nil {
		t.Fatal("未配置的指标监听应关闭")
	}
	for _, addr := range []string{":18085", "0.0.0.0:18085", "[::]:18085", "192.0.2.1:18085"} {
		if _, _, err := startMetrics(addr); err == nil {
			t.Fatalf("应拒绝非回环监听 %q", addr)
		}
	}
	collector, server, err = startMetrics("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	collector.ObserveRoomJoin(http.StatusOK, 7*time.Millisecond)
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	res, err := client.Get("http://" + server.Addr + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil || res.StatusCode != http.StatusOK || !strings.Contains(string(body), "turtle_operation_duration_seconds_count") {
		t.Fatalf("抓取回环指标失败：status=%d err=%v", res.StatusCode, err)
	}
}

func TestMetricsReuseRequestTrace(t *testing.T) {
	monitor, err := telemetry.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer monitor.Close()
	metrics := telemetry.NewMetrics()
	monitor.SetMetrics(metrics)
	router := newRouter(monitor)
	router.POST("/api/rooms/join", func(c *gin.Context) {
		c.Status(http.StatusConflict)
	})
	router.POST("/api/ai/solo/:id/actions", func(c *gin.Context) {
		if c.Param("id") == "private-game" {
			end := telemetry.BeginResult(c.Request.Context(), "ai.decision")
			end(io.ErrUnexpectedEOF)
			c.Status(http.StatusBadGateway)
			return
		}
		c.Status(http.StatusOK)
	})
	for _, path := range []string{"/api/rooms/join", "/api/ai/solo/private-game/actions", "/api/ai/solo/duplicate/actions"} {
		router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, path, nil))
	}
	res := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := res.Body.String()
	for _, line := range []string{
		`turtle_operation_duration_seconds_count{operation="room_join",outcome="rejected"} 1`,
		`turtle_operation_duration_seconds_count{operation="ai_decision",outcome="error"} 1`,
		`turtle_operation_duration_seconds_count{operation="ai_decision",outcome="ok"} 0`,
	} {
		if !strings.Contains(body, line) {
			t.Errorf("指标缺少 %s", line)
		}
	}
	if strings.Contains(body, "private-game") || strings.Contains(body, "duplicate") {
		t.Fatal("指标包含对局信息")
	}
	public := httptest.NewRecorder()
	router.ServeHTTP(public, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if public.Code != http.StatusNotFound {
		t.Fatalf("业务端口不应暴露指标：%d", public.Code)
	}
}
