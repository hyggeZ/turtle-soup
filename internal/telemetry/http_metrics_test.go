package telemetry

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

// 真实 Gin 中间件和 pgx 回调分别产出请求状态与 SQL 计数。
func TestHTTPMetricsMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	m := NewMetrics()
	s.SetMetrics(m)
	router := gin.New()
	router.Use(s.Middleware())
	fixture := func(status, queries int, decision bool) gin.HandlerFunc {
		return func(c *gin.Context) {
			ctx := c.Request.Context()
			for i := range queries {
				q := s.TraceQueryStart(ctx, nil, pgx.TraceQueryStartData{SQL: "SELECT private_sql", Args: []any{"private_argument"}})
				var result pgx.TraceQueryEndData
				if i == 1 {
					result.Err = errors.New("private_query_error")
				}
				s.TraceQueryEnd(q, nil, result)
			}
			if decision {
				BeginResult(ctx, "ai.decision")(errors.New("private_model_error"))
			}
			c.Status(status)
		}
	}
	router.GET("/api/soups", fixture(200, 2, false))
	router.GET("/api/soups/:id", fixture(304, 0, false))
	router.POST("/api/rooms/join", fixture(403, 1, false))
	router.POST("/api/ai/solo/:id/actions", fixture(502, 3, true))
	router.GET("/api/rooms/:id/ws", fixture(101, 5, false))
	router.GET("/health", fixture(200, 6, false))
	router.GET("/api", fixture(200, 7, false))
	router.NoRoute(fixture(404, 8, false))
	before := scrapeMetrics(t, m)
	for _, sample := range []struct {
		method, path string
		status, db   int
	}{
		{"GET", "/api/soups?keyword=private_query", 200, 2},
		{"GET", "/api/soups/48611023", 304, 0},
		{"POST", "/api/rooms/join", 403, 1},
		{"POST", "/api/ai/solo/48611023/actions", 502, 3},
		{"GET", "/api/rooms/48611023/ws", 101, 5},
		{"GET", "/health", 200, 6},
		{"GET", "/api", 200, 7},
		{"GET", "/api/missing/48611023", 404, 8},
	} {
		req := httptest.NewRequest(sample.method, sample.path, nil)
		req.AddCookie(&http.Cookie{Name: "turtle_session", Value: "private_cookie"})
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		latest := s.Snapshot().Recent[0]
		if res.Code != sample.status || latest.DBCount != sample.db || latest.TraceID != res.Header().Get("X-Trace-ID") {
			t.Fatalf("请求夹具未走预期链路：%s / HTTP %d / SQL %d", sample.method, res.Code, latest.DBCount)
		}
	}
	after := scrapeMetrics(t, m)
	zeros, counts := httpCounterLines(before), httpCounterLines(after)
	t.Logf("首次抓取 %v", zeros)
	t.Logf("执行请求后抓取 %v", counts)
	if want := []string{
		`turtle_http_db_queries_total 0`,
		`turtle_http_requests_total{status_class="2xx"} 0`,
		`turtle_http_requests_total{status_class="3xx"} 0`,
		`turtle_http_requests_total{status_class="4xx"} 0`,
		`turtle_http_requests_total{status_class="5xx"} 0`,
	}; !slices.Equal(zeros, want) {
		t.Errorf("首次抓取需要固定五条零值指标，实际为 %v", zeros)
	}
	if want := []string{
		`turtle_http_db_queries_total 6`,
		`turtle_http_requests_total{status_class="2xx"} 1`,
		`turtle_http_requests_total{status_class="3xx"} 1`,
		`turtle_http_requests_total{status_class="4xx"} 1`,
		`turtle_http_requests_total{status_class="5xx"} 1`,
	}; !slices.Equal(counts, want) {
		t.Errorf("匹配的 API 应只累计固定状态组及其 SQL，实际为 %v", counts)
	}
	checkMetricLines(t, after,
		"# TYPE turtle_http_requests_total counter",
		"# TYPE turtle_http_db_queries_total counter",
		`turtle_operation_duration_seconds_count{operation="room_join",outcome="rejected"} 1`,
		`turtle_operation_duration_seconds_count{operation="ai_decision",outcome="error"} 1`,
	)
	for _, private := range []string{"private_", "48611023", "/api/", "trace_id=", "user_id="} {
		if strings.Contains(after, private) {
			t.Errorf("抓取暴露了请求信息：%s", private)
		}
	}
}

func httpCounterLines(body string) []string {
	var lines []string
	for line := range strings.SplitSeq(body, "\n") {
		if strings.HasPrefix(line, "turtle_http_") {
			lines = append(lines, line)
		}
	}
	slices.Sort(lines)
	return lines
}

// 复用真实观测入口，只计已有请求链路复用字段后的进程内成本。
func BenchmarkHTTPMetricsObserve(b *testing.B) {
	for _, sample := range []struct {
		name   string
		record Record
	}{
		{"get_soups", Record{Method: http.MethodGet, Route: "/api/soups", Status: http.StatusOK, DBCount: 1}},
		{"post_room_join", Record{Method: http.MethodPost, Route: "/api/rooms/join", Status: http.StatusOK, DBCount: 4}},
	} {
		b.Run(sample.name, func(b *testing.B) {
			s := &Store{metrics: NewMetrics()}
			b.ReportAllocs()
			for b.Loop() {
				s.observeRequest(sample.record, time.Millisecond)
			}
		})
	}
}
