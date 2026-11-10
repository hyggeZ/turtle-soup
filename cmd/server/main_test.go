package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
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
