package main

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"turtle-soup/internal/telemetry"
)

// 错误请求仍进入后台追踪，日志只保留服务生成的定位信息。
func TestRecoveryKeepsOnlyTraceMetadata(t *testing.T) {
	for _, mode := range []string{gin.DebugMode, gin.ReleaseMode} {
		t.Run(mode, func(t *testing.T) {
			var logs bytes.Buffer
			oldMode, oldGin, oldLog := gin.Mode(), gin.DefaultErrorWriter, log.Writer()
			gin.SetMode(mode)
			gin.DefaultErrorWriter = &logs
			log.SetOutput(&logs)
			t.Cleanup(func() {
				gin.SetMode(oldMode)
				gin.DefaultErrorWriter = oldGin
				log.SetOutput(oldLog)
			})

			monitor, err := telemetry.NewStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer monitor.Close()
			router := newRouter(monitor)
			router.GET("/api/check/:id", func(c *gin.Context) {
				panic("private-panic-marker")
			})
			request := httptest.NewRequest(http.MethodGet, "/api/check/private-path-marker?q=private-query-marker", nil)
			request.AddCookie(&http.Cookie{Name: "turtle_session", Value: "private-cookie-marker"})
			request.Header.Set("X-Fixture", "private-header-marker")
			request.Header.Set("X-Trace-ID", "private-trace-marker")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			leaked := 0
			for _, name := range []string{"path", "query", "cookie", "header", "panic", "trace"} {
				if strings.Contains(logs.String()+response.Body.String(), "private-"+name+"-marker") {
					leaked++
				}
			}
			trace := response.Header().Get("X-Trace-ID")
			records := monitor.Snapshot().Recent
			t.Logf("mode=%s leaked_markers=%d status=%d trace_records=%d", mode, leaked, response.Code, len(records))
			if leaked != 0 || response.Code != http.StatusInternalServerError || len(trace) != 32 ||
				!strings.Contains(logs.String(), trace) || len(records) != 1 {
				t.Fatal("故障响应或日志暴露了请求数据，或缺失追踪信息")
			}
			if record := records[0]; record.Route != "/api/check/:id" || record.Status != response.Code || record.TraceID != trace {
				t.Fatal("后台记录应保留路由模板、HTTP 状态和同一个追踪标识")
			}
		})
	}
}
