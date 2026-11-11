// 服务入口连接数据库，并注册各业务模块的接口。
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"turtle-soup/internal/account"
	"turtle-soup/internal/admin"
	"turtle-soup/internal/agent"
	"turtle-soup/internal/forum"
	"turtle-soup/internal/room"
	"turtle-soup/internal/telemetry"
	"turtle-soup/migrations"
)

func main() {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		log.Fatal("请先设置 DATABASE_URL")
	}

	logDir := os.Getenv("LOG_DIR")
	if logDir == "" {
		logDir = ".local/logs"
	}
	monitor, err := telemetry.NewStore(logDir)
	if err != nil {
		log.Fatal("初始化日志失败: ", err)
	}
	defer monitor.Close()
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		log.Fatal("数据库配置无效: ", err)
	}
	config.ConnConfig.Tracer = monitor

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	db, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	if err := db.Ping(ctx); err != nil {
		log.Fatal("连接 PostgreSQL 失败: ", err)
	}
	if err := migrations.Apply(context.Background(), db); err != nil {
		log.Fatal("初始化数据表失败: ", err)
	}
	recoverCtx, stopRecover := context.WithTimeout(context.Background(), 5*time.Second)
	if err := room.RecoverActive(recoverCtx, db); err != nil {
		stopRecover()
		log.Fatal("恢复房间状态失败: ", err)
	}
	stopRecover()

	router := newRouter(monitor)
	// 默认只信任 TCP 对端地址；部署在反向代理后时显式列出代理 IP 或 CIDR。
	if raw := strings.TrimSpace(os.Getenv("TRUSTED_PROXIES")); raw != "" {
		proxies := strings.Split(raw, ",")
		for i := range proxies {
			proxies[i] = strings.TrimSpace(proxies[i])
		}
		if err := router.SetTrustedProxies(proxies); err != nil {
			log.Fatal("TRUSTED_PROXIES 无效: ", err)
		}
	}
	auth := account.NewHandler(account.NewPGStore(db), os.Getenv("COOKIE_SECURE") == "true")
	auth.Register(router)
	forum.NewHandler(forum.NewStore(db), auth).Register(router)
	admin.NewHandler(db, auth, monitor).Register(router)
	room.NewHandler(db, auth).Register(router)
	agent.NewHandler(agent.NewStore(db), auth, agent.NewQwenFromEnv()).Register(router)
	server := newHTTPServer(router)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	shutdownDone := make(chan struct{})
	go func() {
		<-signals
		defer close(shutdownDone)
		pausedAt := time.Now()
		// 先停止接收新请求，再持久化停机信号时刻，避免倒计时吞掉停机时间。
		shutdownCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Print("停止接收请求失败: ", err)
			_ = server.Close()
		}
		stop()
		pauseCtx, stopPause := context.WithTimeout(context.Background(), 5*time.Second)
		if err := room.PauseActive(pauseCtx, db, pausedAt); err != nil {
			log.Print("暂停进行中的房间失败: ", err)
		}
		stopPause()
	}()
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	<-shutdownDone
}

func listenAddress() string {
	if addr := strings.TrimSpace(os.Getenv("SERVER_ADDR")); addr != "" {
		return addr
	}
	return "127.0.0.1:18080"
}

// 请求体很小，整次读取限定 15 秒，避免客户端缓慢发送数据长期占用连接。
// WebSocket 升级后由连接自身管理读写时限，不设置 HTTP 写超时。
func newHTTPServer(handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              listenAddress(),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}
}

func newRouter(monitor *telemetry.Store) *gin.Engine {
	router := gin.New()
	// 默认不信任客户端伪造的代理 IP 头。
	if err := router.SetTrustedProxies(nil); err != nil {
		panic(err)
	}
	router.Use(monitor.Middleware(), account.SameOriginMiddleware(), gin.Recovery())
	router.GET("/health", func(ctx *gin.Context) {
		ctx.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	return router
}
