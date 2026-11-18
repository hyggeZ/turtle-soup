package telemetry

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func scrapeMetrics(t *testing.T, m *Metrics) string {
	t.Helper()
	response := httptest.NewRecorder()
	m.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if response.Code != http.StatusOK || !strings.HasPrefix(response.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("抓取响应异常：%d %s", response.Code, response.Header().Get("Content-Type"))
	}
	return response.Body.String()
}

func checkMetricLines(t *testing.T, body string, want ...string) {
	t.Helper()
	for _, line := range want {
		if !strings.Contains(body, line+"\n") {
			t.Errorf("抓取结果缺少 %s", line)
		}
	}
}

// 验证抓取格式、累计值和固定标签，不暴露进程级指标。
func TestMetricsScrape(t *testing.T) {
	m := NewMetrics()
	before := scrapeMetrics(t, m)
	checkMetricLines(t, before,
		"# TYPE turtle_operation_duration_seconds histogram",
		`turtle_operation_duration_seconds_count{operation="ai_decision",outcome="error"} 0`,
		`turtle_operation_duration_seconds_count{operation="ai_decision",outcome="ok"} 0`,
		`turtle_operation_duration_seconds_count{operation="room_join",outcome="error"} 0`,
		`turtle_operation_duration_seconds_count{operation="room_join",outcome="ok"} 0`,
		`turtle_operation_duration_seconds_count{operation="room_join",outcome="rejected"} 0`,
	)

	m.ObserveRoomJoin(http.StatusOK, 125*time.Millisecond)
	m.ObserveRoomJoin(http.StatusCreated, 500*time.Millisecond)
	m.ObserveRoomJoin(http.StatusTooManyRequests, time.Millisecond)
	m.ObserveRoomJoin(http.StatusServiceUnavailable, 2*time.Second)
	m.ObserveDecision(false, time.Second)
	m.ObserveDecision(true, 3*time.Second)

	body := scrapeMetrics(t, m)
	checkMetricLines(t, body,
		`turtle_operation_duration_seconds_bucket{operation="room_join",outcome="ok",le="0.25"} 1`,
		`turtle_operation_duration_seconds_bucket{operation="room_join",outcome="ok",le="0.5"} 2`,
		`turtle_operation_duration_seconds_sum{operation="room_join",outcome="ok"} 0.625`,
		`turtle_operation_duration_seconds_count{operation="room_join",outcome="ok"} 2`,
		`turtle_operation_duration_seconds_count{operation="room_join",outcome="rejected"} 1`,
		`turtle_operation_duration_seconds_count{operation="room_join",outcome="error"} 1`,
		`turtle_operation_duration_seconds_count{operation="ai_decision",outcome="ok"} 1`,
		`turtle_operation_duration_seconds_count{operation="ai_decision",outcome="error"} 1`,
	)
	for _, hidden := range []string{"go_goroutines", "process_cpu_seconds", `operation="ai_decision",outcome="rejected"`} {
		if strings.Contains(body, hidden) {
			t.Errorf("抓取暴露非预期数据：%s", hidden)
		}
	}

	if after := scrapeMetrics(t, NewMetrics()); before != after {
		t.Error("独立注册表不应继承其他采集器的指标")
	}
}

// 抓取与并行观测可以同时进行，所有完成的观测必须保留。
func TestMetricsConcurrent(t *testing.T) {
	const workers = 8
	const perWorker = 50
	m := NewMetrics()
	var group sync.WaitGroup
	for range workers {
		group.Go(func() {
			for range perWorker {
				m.ObserveRoomJoin(http.StatusOK, time.Millisecond)
				m.ObserveDecision(false, time.Second)
			}
		})
	}
	for range 5 {
		scrapeMetrics(t, m)
	}
	group.Wait()
	count := workers * perWorker
	checkMetricLines(t, scrapeMetrics(t, m),
		fmt.Sprintf(`turtle_operation_duration_seconds_count{operation="room_join",outcome="ok"} %d`, count),
		fmt.Sprintf(`turtle_operation_duration_seconds_count{operation="ai_decision",outcome="ok"} %d`, count),
	)
}
