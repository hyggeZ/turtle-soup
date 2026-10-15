package main

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func main() {
	router := newRouter()
	if err := router.Run(":18080"); err != nil {
		panic(err)
	}
}

func newRouter() *gin.Engine {
	router := gin.New()
	router.Use(gin.Logger(), gin.Recovery())
	router.GET("/health", func(ctx *gin.Context) {
		ctx.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	return router
}
