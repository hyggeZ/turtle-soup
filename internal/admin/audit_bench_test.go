package admin_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"turtle-soup/internal/account"
	"turtle-soup/internal/admin"
)

type auditQueryCount struct{ count atomic.Int64 }

func (q *auditQueryCount) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	q.count.Add(1)
	return ctx
}

func (*auditQueryCount) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// 用相同的真实后台路由比较引入审计前后的写入成本。
func TestAdminAuditWriteCost(t *testing.T) {
	if os.Getenv("ADMIN_AUDIT_COMPARE") != "1" {
		t.Skip("设置 ADMIN_AUDIT_COMPARE=1 后测量")
	}
	f := newModerationTest(t)
	soup := f.soup("user", "操作审计基准题", "基准使用的汤底")
	tracer := &auditQueryCount{}
	config := f.db.Config()
	config.ConnConfig.Tracer = tracer
	db, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	f.router = gin.New()
	auth := account.NewHandler(account.NewPGStore(db), false)
	admin.NewHandler(db, auth, nil).Register(f.router)

	for _, task := range []struct {
		name, method, path string
		body               func(int) map[string]any
	}{
		{"create", http.MethodPost, "/api/admin/users", func(i int) map[string]any {
			return map[string]any{"username": fmt.Sprintf("audit_perf_%d", i), "password": "test-only-pw", "role": "user"}
		}},
		{"account_status", http.MethodPatch, fmt.Sprintf("/api/admin/users/%d", f.users["user"]), func(i int) map[string]any {
			return map[string]any{"disabled": i%2 == 0}
		}},
		{"soup_visibility", http.MethodPatch, fmt.Sprintf("/api/admin/soups/%d", soup), func(i int) map[string]any {
			return map[string]any{"hidden": i%2 == 0}
		}},
	} {
		const warm, count = 4, 20
		var queries int64
		samples := make([]float64, 0, count)
		for i := 0; i < warm+count; i++ {
			body := task.body(i)
			tracer.count.Store(0)
			start := time.Now()
			res := f.call(task.method, task.path, "super_admin", body)
			duration := time.Since(start)
			if res.Code != http.StatusOK && res.Code != http.StatusCreated {
				t.Fatalf("%s 测量失败：HTTP %d %s", task.name, res.Code, res.Body.String())
			}
			if i >= warm {
				queries += tracer.count.Load()
				samples = append(samples, float64(duration)/float64(time.Millisecond))
			}
		}
		sort.Float64s(samples)
		t.Logf("action=%s samples=%d dbq/op=%.2f p50=%.2fms p95=%.2fms", task.name, count, float64(queries)/count,
			(samples[count/2-1]+samples[count/2])/2, samples[(95*count+99)/100-1])
	}
}
