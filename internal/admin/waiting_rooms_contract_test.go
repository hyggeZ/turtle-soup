package admin_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"turtle-soup/internal/account"
	"turtle-soup/internal/admin"
	"turtle-soup/internal/forum"
)

type waitingSQLKey struct{}
type waitingSQLTracer struct{}

func (waitingSQLTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if queries, ok := ctx.Value(waitingSQLKey{}).(*[]string); ok {
		*queries = append(*queries, data.SQL)
	}
	return ctx
}

func (waitingSQLTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// 删除与下架共用房间规则，同时保留各自的权限、审计和事务顺序。
func TestWaitingRoomsMutationContract(t *testing.T) {
	f := newModerationTest(t)
	queries := tracedWaitingRouter(t, f)
	warmups, samples := 0, 1
	if os.Getenv("WAITING_ROOMS_COMPARE") == "1" {
		warmups, samples = 2, 18
	}
	for _, entry := range []struct {
		name, method, path, role string
		body                     any
		status                   int
		order                    []string
	}{
		{"author_delete", http.MethodDelete, "/api/soups/%d", "user", nil, http.StatusNoContent,
			[]string{"session", "begin", "check_soup", "lock_waiting", "lock_soup", "lock_waiting_nowait", "write_soup", "close_waiting", "commit"}},
		{"admin_hide", http.MethodPatch, "/api/admin/soups/%d", "admin", map[string]bool{"hidden": true}, http.StatusOK,
			[]string{"session", "begin", "check_soup", "lock_waiting", "lock_soup", "write_soup", "lock_waiting_nowait", "close_waiting", "audit", "commit"}},
	} {
		t.Run(entry.name, func(t *testing.T) {
			times := make([]time.Duration, 0, samples)
			var signature [32]byte
			for i := range warmups + samples {
				soup := f.soup("user", "等待房规则测试题", "合成汤底")
				waiting := f.room(soup, "waiting")
				active := f.room(soup, "active")
				path := fmt.Sprintf(entry.path, soup)
				f.closed = f.closed[:0]
				*queries = (*queries)[:0]
				start := time.Now()
				res := f.call(entry.method, path, entry.role, entry.body)
				elapsed := time.Since(start)
				if res.Code != entry.status {
					t.Fatalf("变更题目：HTTP %d %s", res.Code, res.Body.String())
				}
				if entry.method == http.MethodPatch {
					var reply struct {
						ID          int64 `json:"id"`
						Hidden      bool  `json:"hidden"`
						ClosedRooms int   `json:"closedRooms"`
					}
					if json.Unmarshal(res.Body.Bytes(), &reply) != nil || reply.ID != soup || !reply.Hidden || reply.ClosedRooms != 1 {
						t.Fatalf("下架响应未报告正确的题目和关闭房间数：%s", res.Body.String())
					}
				}
				checkWaitingRoomStates(t, f, waiting, active)
				actual := waitingSQLSignature(t, *queries, entry.order)
				if i == 0 {
					signature = actual
				} else if actual != signature {
					t.Fatal("同一入口执行了不同的 SQL 序列")
				}
				if i >= warmups {
					times = append(times, elapsed)
				}
			}
			slices.Sort(times)
			p50 := times[(samples-1)/2]
			p95 := times[(95*samples+99)/100-1]
			t.Logf("entry=%s n=%d SQL=%d p50_ms=%.3f p95_ms=%.3f signature=%x order=%s", entry.name, samples,
				len(entry.order), float64(p50)/float64(time.Millisecond), float64(p95)/float64(time.Millisecond), signature, strings.Join(entry.order, ","))
		})
	}
}

func tracedWaitingRouter(t *testing.T, f *moderationTest) *[]string {
	t.Helper()
	config := f.db.Config()
	config.ConnConfig.Tracer = waitingSQLTracer{}
	db, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	router := gin.New()
	queries := make([]string, 0, 10)
	router.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), waitingSQLKey{}, &queries))
	})
	notify := func(id int64) { f.closed = append(f.closed, id) }
	auth := account.NewHandler(account.NewPGStore(db), false)
	moderation := admin.NewHandler(db, auth, nil)
	moderation.SetRoomNotifier(notify)
	moderation.Register(router)
	community := forum.NewHandler(forum.NewStore(db), auth)
	community.SetRoomNotifier(notify)
	community.Register(router)
	f.router = router
	return &queries
}

func checkWaitingRoomStates(t *testing.T, f *moderationTest, waiting, active int64) {
	t.Helper()
	if !slices.Equal(f.closed, []int64{waiting}) {
		t.Fatalf("只应通知等待房关闭，实际通知：%v", f.closed)
	}
	var waitingClosed, activeKept bool
	err := f.db.QueryRow(context.Background(), `SELECT
		EXISTS(SELECT 1 FROM rooms WHERE id=$1 AND status='closed' AND closed_at IS NOT NULL),
		EXISTS(SELECT 1 FROM rooms WHERE id=$2 AND status='active' AND closed_at IS NULL AND soup_answer='保留的汤底')`,
		waiting, active).Scan(&waitingClosed, &activeKept)
	if err != nil || !waitingClosed || !activeKept {
		t.Fatalf("等待房和进行中快照的状态不符：waitingClosed=%t activeKept=%t err=%v", waitingClosed, activeKept, err)
	}
}

func waitingSQLSignature(t *testing.T, queries, want []string) [32]byte {
	t.Helper()
	normalized := make([]string, 0, len(queries))
	actual := make([]string, 0, len(queries))
	for _, raw := range queries {
		sql := strings.Join(strings.Fields(raw), " ")
		normalized = append(normalized, sql)
		actual = append(actual, waitingSQLStep(sql))
	}
	if !slices.Equal(actual, want) {
		t.Fatalf("SQL 调用顺序不符：%v，应为 %v\n实际 SQL：%v", actual, want, normalized)
	}
	return sha256.Sum256([]byte(strings.Join(normalized, "\n")))
}

func waitingSQLStep(sql string) string {
	switch {
	case sql == "begin" || sql == "commit":
		return sql
	case strings.Contains(sql, "FROM sessions AS s JOIN users"):
		return "session"
	case strings.HasPrefix(sql, "SELECT id FROM rooms") && strings.HasSuffix(sql, "FOR UPDATE NOWAIT"):
		return "lock_waiting_nowait"
	case strings.HasPrefix(sql, "SELECT id FROM rooms") && strings.HasSuffix(sql, "FOR UPDATE"):
		return "lock_waiting"
	case strings.HasPrefix(sql, "SELECT") && strings.Contains(sql, "FROM soups") && strings.Contains(sql, "FOR UPDATE"):
		return "lock_soup"
	case strings.HasPrefix(sql, "SELECT") && strings.Contains(sql, "FROM soups"):
		return "check_soup"
	case strings.HasPrefix(sql, "UPDATE soups SET"):
		return "write_soup"
	case strings.HasPrefix(sql, "UPDATE rooms SET status='closed'"):
		return "close_waiting"
	case strings.HasPrefix(sql, "INSERT INTO admin_audit_logs"):
		return "audit"
	default:
		return "unknown"
	}
}
