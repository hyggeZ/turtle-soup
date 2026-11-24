package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"turtle-soup/internal/telemetry"
)

func readinessRouter(t *testing.T, ping func(context.Context) error) http.Handler {
	t.Helper()
	monitor, err := telemetry.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = monitor.Close() })
	router := newRouter(monitor)
	registerReadiness(router, ping)
	return router
}

func checkProbe(t *testing.T, router http.Handler, path string, status int) time.Duration {
	t.Helper()
	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	start := time.Now()
	router.ServeHTTP(res, req)
	elapsed := time.Since(start)
	if res.Code != status {
		t.Fatalf("探测 %s 的状态应为 %d，实际为 %d", path, status, res.Code)
	}
	if path == "/ready" && (res.Body.Len() != 0 || res.Header().Get("Cache-Control") != "no-store") {
		t.Fatal("就绪响应应为不可缓存的空正文")
	}
	return elapsed
}

func TestReadinessUsesRequestContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	router := readinessRouter(t, func(probeCtx context.Context) error {
		calls++
		if !errors.Is(probeCtx.Err(), context.Canceled) {
			t.Error("就绪检查须继承原请求取消信号")
		}
		return errors.New("fixture_private_failure")
	})
	checkProbe(t, router, "/health", http.StatusOK)
	if calls != 0 {
		t.Fatal("存活探测不应访问数据库")
	}
	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ready", nil).WithContext(ctx)
	router.ServeHTTP(res, req)
	if calls != 1 || res.Code != http.StatusServiceUnavailable || res.Body.Len() != 0 {
		t.Fatal("检查失败后应返回无错误细节的 503")
	}
}

// 只让测试服务自己的连接池断线。数据库进程和其他连接不受影响。
func TestReadinessPostgresRecovery(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("需要隔离的 PostgreSQL 测试库")
	}
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal("TEST_DATABASE_URL 无效")
	}
	const (
		online int32 = iota
		offline
		stalled
	)
	var mode, refused, paused atomic.Int32
	var once sync.Once
	unblock := make(chan struct{})
	releaseDial := func() { once.Do(func() { close(unblock) }) }
	dial := config.ConnConfig.DialFunc
	config.ConnConfig.DialFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
		switch mode.Load() {
		case offline:
			refused.Add(1)
			return nil, errors.New("测试拨号被拒")
		case stalled:
			paused.Add(1)
			select {
			case <-unblock:
				return nil, errors.New("结束测试拨号等待")
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		default:
			return dial(ctx, network, address)
		}
	}
	config.MaxConns, config.MinConns, config.MinIdleConns = 1, 0, 0
	db, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { releaseDial(); db.Close() })
	router := readinessRouter(t, db.Ping)
	first := checkProbe(t, router, "/ready", http.StatusOK)
	checkProbe(t, router, "/health", http.StatusOK)
	mode.Store(offline)
	db.Reset()
	fail := checkProbe(t, router, "/ready", http.StatusServiceUnavailable)
	checkProbe(t, router, "/health", http.StatusOK)
	mode.Store(stalled)
	wait := checkProbe(t, router, "/ready", http.StatusServiceUnavailable)
	if wait < readinessTimeout || wait > 3*time.Second || refused.Load() == 0 || paused.Load() == 0 {
		t.Fatalf("未经过预期的拨号拒绝与超时，超时请求用时 %s", wait)
	}
	checkProbe(t, router, "/health", http.StatusOK)
	mode.Store(online)
	releaseDial()
	db.Reset()
	recovered := checkProbe(t, router, "/ready", http.StatusOK)
	checkProbe(t, router, "/health", http.StatusOK)
	t.Logf("就绪探测 正常=%s 拒绝=%s 超时=%s 恢复=%s；四次存活检查均为 200", first, fail, wait, recovered)
}
