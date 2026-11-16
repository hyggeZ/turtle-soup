package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
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
	span := snapshot.Recent[0].Spans[0]
	if span.Name != "test.stage" || span.StartMS < 0 || span.DurationMS < 0 || span.StartMS+span.DurationMS > snapshot.Recent[0].DurationMS+1 {
		t.Fatalf("阶段时间超出请求范围：%+v", span)
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

// 失败只写标记；断开请求后保留追踪上下文的任务也归属于同一条链路。
func TestFailedStageAndDetachedContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	router := gin.New()
	router.Use(store.Middleware())
	router.GET("/failed", func(c *gin.Context) {
		end := BeginResult(c.Request.Context(), "ai.model")
		end(errors.New("private model error"))
		queryCtx := store.TraceQueryStart(c.Request.Context(), nil, pgx.TraceQueryStartData{SQL: "private query"})
		store.TraceQueryEnd(queryCtx, nil, pgx.TraceQueryEndData{Err: errors.New("private database error")})
		requestCtx, cancel := context.WithCancel(c.Request.Context())
		cancel()
		persistCtx := context.WithoutCancel(requestCtx)
		if persistCtx.Err() != nil {
			t.Fatal("断线后的落库上下文仍被取消")
		}
		Begin(persistCtx, "ai.persist")()
		c.Status(http.StatusInternalServerError)
	})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/failed", nil))
	record := store.Snapshot().Recent[0]
	if record.TraceID != response.Header().Get("X-Trace-ID") || record.DBCount != 1 || len(record.Spans) != 3 {
		t.Fatalf("请求、数据库与断线提交没有关联：%+v", record)
	}
	if !record.Spans[0].Failed || !record.Spans[1].Failed || record.Spans[2].Failed || record.Spans[2].Name != "ai.persist" {
		t.Fatalf("阶段失败状态错误：%+v", record.Spans)
	}
	data, err := os.ReadFile(filepath.Join(dir, "requests.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "private") {
		t.Fatal("日志包含模型或数据库错误详情")
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
