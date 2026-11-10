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
