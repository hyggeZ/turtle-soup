package admin

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type aiMetric struct {
	Model         string  `json:"model"`
	PromptVersion string  `json:"promptVersion"`
	Calls         int64   `json:"calls"`
	Successes     int64   `json:"successes"`
	InvalidOutput int64   `json:"invalidOutput"`
	ProviderError int64   `json:"providerError"`
	AvgMS         float64 `json:"avgMs"`
	P95MS         float64 `json:"p95Ms"`
}

// 只统计模型版本、耗时与错误类型，不读取玩家问题或汤底。
func (h *Handler) aiMetrics(c *gin.Context) {
	if _, ok := h.operator(c); !ok {
		return
	}
	rows, err := h.db.Query(c.Request.Context(), `
		SELECT model,prompt_version,COUNT(*),
			COUNT(*) FILTER (WHERE success),
			COUNT(*) FILTER (WHERE error_kind='invalid_output'),
			COUNT(*) FILTER (WHERE error_kind='provider_error'),
			AVG(latency_ms),
			percentile_cont(0.95) WITHIN GROUP (ORDER BY latency_ms)
		FROM ai_attempts WHERE created_at >= NOW()-INTERVAL '7 days'
		GROUP BY model,prompt_version ORDER BY COUNT(*) DESC LIMIT 20`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取模型指标失败"})
		return
	}
	defer rows.Close()
	items := make([]aiMetric, 0)
	for rows.Next() {
		var item aiMetric
		if err := rows.Scan(&item.Model, &item.PromptVersion, &item.Calls,
			&item.Successes, &item.InvalidOutput, &item.ProviderError, &item.AvgMS, &item.P95MS); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "读取模型指标失败"})
			return
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取模型指标失败"})
		return
	}
	c.JSON(http.StatusOK, items)
}
