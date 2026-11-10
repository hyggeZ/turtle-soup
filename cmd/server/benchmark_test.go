package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"turtle-soup/internal/telemetry"
)

// BenchmarkHealth 记录 HTTP 中间件对最小请求的额外开销。
func BenchmarkHealth(b *testing.B) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard
	monitor, err := telemetry.NewStore(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer monitor.Close()
	router := newRouter(monitor)
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			b.Fatal(response.Code)
		}
	}
}
