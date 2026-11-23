package telemetry

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics 聚合 API 请求、追踪 SQL 数及关键操作耗时，标签固定。
type Metrics struct {
	handler      http.Handler
	apiRequests  [4]prometheus.Counter
	dbQueries    prometheus.Counter
	joinOK       prometheus.Observer
	joinRejected prometheus.Observer
	joinError    prometheus.Observer
	decisionOK   prometheus.Observer
	decisionErr  prometheus.Observer
}

// NewMetrics 使用私有注册表，只开放此模块声明的指标。
func NewMetrics() *Metrics {
	reg := prometheus.NewRegistry()
	duration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "turtle_operation_duration_seconds",
		Help:    "Duration and count of room joins and AI decisions.",
		Buckets: []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 20, 30},
	}, []string{"operation", "outcome"})
	requests := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "turtle_http_requests_total",
		Help: "Completed matched API requests by HTTP status class.",
	}, []string{"status_class"})
	queries := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "turtle_http_db_queries_total",
		Help: "Traced database queries for completed matched API requests.",
	})
	reg.MustRegister(duration, requests, queries)

	// 提前绑定全部可用标签；从首次抓取起就能看到零值，观测时也无需查找标签。
	return &Metrics{
		handler: promhttp.HandlerFor(reg, promhttp.HandlerOpts{}),
		apiRequests: [4]prometheus.Counter{
			requests.WithLabelValues("2xx"), requests.WithLabelValues("3xx"),
			requests.WithLabelValues("4xx"), requests.WithLabelValues("5xx"),
		},
		dbQueries:    queries,
		joinOK:       duration.WithLabelValues("room_join", "ok"),
		joinRejected: duration.WithLabelValues("room_join", "rejected"),
		joinError:    duration.WithLabelValues("room_join", "error"),
		decisionOK:   duration.WithLabelValues("ai_decision", "ok"),
		decisionErr:  duration.WithLabelValues("ai_decision", "error"),
	}
}

// Handler 返回给回环监听使用的 Prometheus 抓取处理器。
func (m *Metrics) Handler() http.Handler { return m.handler }

// 已匹配的 API 请求按响应大类计数，长连接升级不进入 HTTP 指标。
func (m *Metrics) observeAPI(status, queries int) {
	class := status / 100
	if class < 2 || class > 5 {
		return
	}
	m.apiRequests[class-2].Inc()
	if queries > 0 {
		m.dbQueries.Add(float64(queries))
	}
}

// ObserveRoomJoin 区分成功、预期的客户端拒绝和其他异常响应。
func (m *Metrics) ObserveRoomJoin(status int, elapsed time.Duration) {
	if m == nil {
		return
	}
	switch {
	case status >= 200 && status < 300:
		m.joinOK.Observe(seconds(elapsed))
	case status >= 400 && status < 500:
		m.joinRejected.Observe(seconds(elapsed))
	default:
		m.joinError.Observe(seconds(elapsed))
	}
}

// ObserveDecision 只记录一次实际判定是否成功，错误内容不进入指标。
func (m *Metrics) ObserveDecision(failed bool, elapsed time.Duration) {
	if m == nil {
		return
	}
	if failed {
		m.decisionErr.Observe(seconds(elapsed))
		return
	}
	m.decisionOK.Observe(seconds(elapsed))
}

// 直方图的累计耗时需要单调递增，防止无效负值使抓取端误判为重置。
func seconds(elapsed time.Duration) float64 {
	return max(elapsed.Seconds(), 0)
}
