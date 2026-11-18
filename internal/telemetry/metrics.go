package telemetry

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics 只记录房间加入和 AI 判定的累计次数与耗时，不包含单次请求信息。
type Metrics struct {
	handler      http.Handler
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
	reg.MustRegister(duration)

	// 提前绑定全部可用标签；从首次抓取起就能看到零值，观测时也无需查找标签。
	return &Metrics{
		handler:      promhttp.HandlerFor(reg, promhttp.HandlerOpts{}),
		joinOK:       duration.WithLabelValues("room_join", "ok"),
		joinRejected: duration.WithLabelValues("room_join", "rejected"),
		joinError:    duration.WithLabelValues("room_join", "error"),
		decisionOK:   duration.WithLabelValues("ai_decision", "ok"),
		decisionErr:  duration.WithLabelValues("ai_decision", "error"),
	}
}

// Handler 返回给回环监听使用的 Prometheus 抓取处理器。
func (m *Metrics) Handler() http.Handler { return m.handler }

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
