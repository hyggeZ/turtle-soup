package account

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestSameOriginMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(SameOriginMiddleware())
	router.POST("/write", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	cases := []struct {
		name   string
		origin string
		site   string
		status int
	}{
		{name: "同源", origin: "http://127.0.0.1:5173", site: "same-origin", status: http.StatusNoContent},
		{name: "无 Origin 的命令行请求", status: http.StatusNoContent},
		{name: "跨域 Origin", origin: "http://evil.example", status: http.StatusForbidden},
		{name: "同站其他来源", origin: "http://127.0.0.1:5173", site: "same-site", status: http.StatusForbidden},
		{name: "畸形 Origin", origin: "null", status: http.StatusForbidden},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:5173/write", nil)
			if item.origin != "" {
				request.Header.Set("Origin", item.origin)
			}
			if item.site != "" {
				request.Header.Set("Sec-Fetch-Site", item.site)
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != item.status {
				t.Fatalf("状态码 = %d，预期 %d", response.Code, item.status)
			}
		})
	}
}
