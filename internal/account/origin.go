package account

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
)

// SameOriginMiddleware 限制浏览器从其他来源发起带会话的写操作。
// SameSite Cookie 仍保留；这里补充校验 Origin 和浏览器的 Fetch Metadata。
func SameOriginMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead || c.Request.Method == http.MethodOptions {
			c.Next()
			return
		}
		if site := c.GetHeader("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "请求来源不允许"})
			return
		}
		origin := c.GetHeader("Origin")
		if origin == "" {
			c.Next()
			return
		}
		parsed, err := url.Parse(origin)
		if err != nil || !validOrigin(parsed, c.Request.Host) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "请求来源不允许"})
			return
		}
		c.Next()
	}
}

func validOrigin(origin *url.URL, host string) bool {
	if origin.Scheme != "http" && origin.Scheme != "https" {
		return false
	}
	if origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
		return false
	}
	return strings.EqualFold(origin.Host, host)
}
