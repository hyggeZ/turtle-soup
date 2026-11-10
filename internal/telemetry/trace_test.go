package telemetry

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// 验证日志能关联请求，且不会保存 URL 参数或请求体中的敏感值。
func TestRequestTrace(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.Use(store.Middleware())
	router.POST("/api/check/:id", func(c *gin.Context) {
		defer Begin(c.Request.Context(), "test.stage")()
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodPost, "/api/check/42?secret=private", strings.NewReader(`{"password":"hidden"}`))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if len(response.Header().Get("X-Trace-ID")) != 32 {
		t.Fatal("缺少追踪 ID")
	}
	snapshot := store.Snapshot()
	if len(snapshot.Recent) != 1 || snapshot.Recent[0].Route != "/api/check/:id" {
		t.Fatal("未记录路由模板")
	}
	if len(snapshot.Recent[0].Spans) != 1 {
		t.Fatal("未记录阶段耗时")
	}
	data, err := os.ReadFile(filepath.Join(dir, "requests.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "private") || strings.Contains(string(data), "hidden") {
		t.Fatal("日志包含敏感输入")
	}
	var record Record
	if err = json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if len(reopened.Snapshot().Recent) != 1 {
		t.Fatal("重启后未恢复近期请求")
	}
}

func TestWebSocketDurationIsNotHTTPLatency(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	router := gin.New()
	router.Use(store.Middleware())
	router.GET("/socket", func(c *gin.Context) { c.Status(http.StatusSwitchingProtocols) })
	router.GET("/health", func(c *gin.Context) { c.Status(http.StatusOK) })
	for _, path := range []string{"/socket", "/health"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	}
	snapshot := store.Snapshot()
	if snapshot.RecentCount != 2 || snapshot.Connections != 1 || len(snapshot.Routes) != 1 || snapshot.Routes[0].Route != "/health" {
		t.Fatalf("WebSocket 时长混入接口统计：%+v", snapshot)
	}
}
