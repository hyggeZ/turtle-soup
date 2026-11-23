package telemetry

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// 旧组仍用同一个 Store 记录请求和 SQL，但不向池暴露 AcquireTracer。
type queryTracerOnly struct{ pgx.QueryTracer }

type poolStartKey struct{}

type poolTraceFixture struct {
	pool   *pgxpool.Pool
	store  *Store
	router *gin.Engine
}

func newPoolTraceFixture(t *testing.T, traceAcquire bool) *poolTraceFixture {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("需要 TEST_DATABASE_URL")
	}
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal("无法解析 PostgreSQL 测试连接配置")
	}
	cfg.MinConns, cfg.MaxConns = 0, 1
	cfg.ConnConfig.Tracer = queryTracerOnly{store}
	if traceAcquire {
		cfg.ConnConfig.Tracer = store
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(store.Middleware())
	router.GET("/api/check", func(c *gin.Context) {
		ctx := c.Request.Context()
		if started, ok := ctx.Value(poolStartKey{}).(chan struct{}); ok {
			close(started)
		}
		var label string
		err := pool.QueryRow(ctx, `SELECT 'private_pool_fixture_sql'`).Scan(&label)
		switch {
		case errors.Is(err, context.Canceled):
			c.Status(http.StatusGatewayTimeout)
		case err != nil || label != "private_pool_fixture_sql":
			c.Status(http.StatusInternalServerError)
		default:
			c.Status(http.StatusNoContent)
		}
	})
	return &poolTraceFixture{pool: pool, store: store, router: router}
}

func (f *poolTraceFixture) request(ctx context.Context) *httptest.ResponseRecorder {
	res := httptest.NewRecorder()
	f.router.ServeHTTP(res, httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/check", nil))
	return res
}

// 持有唯一的连接，确认第二个 HTTP 请求进来后仍在等，再释放或取消。
func (f *poolTraceFixture) blockedRequest(t *testing.T, shouldCancel bool) *httptest.ResponseRecorder {
	t.Helper()
	ctx, deadline := context.WithTimeout(context.Background(), 5*time.Second)
	defer deadline()
	conn, err := f.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	started := make(chan struct{})
	requestCtx, cancel := context.WithCancelCause(context.WithValue(ctx, poolStartKey{}, started))
	defer cancel(nil)
	finished := make(chan *httptest.ResponseRecorder, 1)
	go func() { finished <- f.request(requestCtx) }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("测试请求没有进入查询处理器")
	}
	wait := time.NewTimer(35 * time.Millisecond)
	defer wait.Stop()
	select {
	case res := <-finished:
		t.Fatalf("连接仍被占用时 HTTP 请求提前完成：%d", res.Code)
	case <-wait.C:
	}
	if shouldCancel {
		cancel(errors.New("private_pool_fixture_cancel"))
	} else {
		conn.Release()
	}
	select {
	case res := <-finished:
		return res
	case <-ctx.Done():
		t.Fatal("测试请求未在释放或取消后结束")
		return nil
	}
}

// 正常查询、排队查询和排队取消均经过实际 Gin 与 PostgreSQL。
func TestPoolTraceWithPostgres(t *testing.T) {
	for _, variant := range []struct {
		name         string
		traceAcquire bool
	}{{"query_only", false}, {"query_and_acquire", true}} {
		t.Run(variant.name, func(t *testing.T) {
			f := newPoolTraceFixture(t, variant.traceAcquire)
			t.Run("normal", func(t *testing.T) {
				res := f.request(context.Background())
				checkPoolRecord(t, f, res, variant.traceAcquire, false, false)
			})
			t.Run("queued", func(t *testing.T) {
				before := f.pool.Stat().EmptyAcquireCount()
				res := f.blockedRequest(t, false)
				if f.pool.Stat().EmptyAcquireCount()-before != 1 {
					t.Fatal("未观察到实际的连接池排队")
				}
				checkPoolRecord(t, f, res, variant.traceAcquire, true, false)
			})
			t.Run("canceled", func(t *testing.T) {
				before := f.pool.Stat().CanceledAcquireCount()
				res := f.blockedRequest(t, true)
				if f.pool.Stat().CanceledAcquireCount()-before != 1 {
					t.Fatal("未观察到连接池取消请求")
				}
				checkPoolRecord(t, f, res, variant.traceAcquire, true, true)
			})
			data, err := os.ReadFile(filepath.Join(f.store.dir, "requests.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			for _, marker := range []string{"private_pool_fixture_sql", "private_pool_fixture_cancel", "context canceled"} {
				if strings.Contains(string(data), marker) {
					t.Fatal("日志记录了不应保存的 SQL 或错误正文")
				}
			}
		})
	}
}

func checkPoolRecord(t *testing.T, f *poolTraceFixture, res *httptest.ResponseRecorder, traceAcquire, queued, canceled bool) {
	t.Helper()
	record := f.store.Snapshot().Recent[0]
	if record.TraceID != res.Header().Get("X-Trace-ID") || record.Route != "/api/check" || record.Status != res.Code {
		t.Fatalf("请求未关联到对应的链路：%+v", record)
	}
	spans := record.Spans
	if traceAcquire {
		if len(spans) == 0 || spans[0].Name != "db.acquire" || spans[0].Failed != canceled {
			t.Fatalf("取连接阶段记录不符：%+v", spans)
		}
		acquire := spans[0]
		if acquire.StartMS < 0 || acquire.DurationMS < 0 || acquire.StartMS+acquire.DurationMS > record.DurationMS+0.001 {
			t.Fatalf("取连接阶段超出请求时间：%+v", record)
		}
		if queued && acquire.DurationMS < 15 {
			t.Fatalf("取连接阶段未覆盖排队时间：%+v", acquire)
		}
		spans = spans[1:]
		if len(spans) > 0 && spans[0].StartMS+0.001 < acquire.StartMS+acquire.DurationMS {
			t.Fatalf("SQL 查询早于取连接完成：%+v", record)
		}
		t.Logf("取连接 %.3f ms，SQL %.3f ms，HTTP %d，失败=%t", acquire.DurationMS, record.DBMS, res.Code, acquire.Failed)
	}
	if canceled {
		if res.Code != http.StatusGatewayTimeout || record.DBCount != 0 || record.DBMS != 0 || len(spans) != 0 {
			t.Fatalf("取消前没有执行 SQL，记录却为：%+v", record)
		}
		return
	}
	if res.Code != http.StatusNoContent || record.DBCount != 1 || len(spans) != 1 || spans[0].Name != "db.query" || spans[0].Failed || record.DBMS != spans[0].DurationMS {
		t.Fatalf("SQL 统计不符：%+v", record)
	}
}
