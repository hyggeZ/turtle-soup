package main

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

const readinessTimeout = 750 * time.Millisecond

// 启动迁移完成后注册。就绪检查只验证数据库连接，响应只包含状态码。
func registerReadiness(router *gin.Engine, ping func(context.Context) error) {
	router.GET("/ready", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		ctx, cancel := context.WithTimeout(c.Request.Context(), readinessTimeout)
		defer cancel()
		if ping(ctx) != nil {
			c.Status(http.StatusServiceUnavailable)
			return
		}
		c.Status(http.StatusOK)
	})
}
