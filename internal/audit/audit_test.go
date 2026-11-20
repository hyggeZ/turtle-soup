package audit

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"turtle-soup/migrations"
)

// 每个测试都使用新建 schema，应用与服务相同的迁移。
func newAuditDB(t testing.TB) (context.Context, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("需要 TEST_DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	schema := fmt.Sprintf("audit_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.Exec(clean, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	db, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := migrations.Apply(ctx, db); err != nil {
		t.Fatal(err)
	}
	return ctx, db
}

func auditExec(t testing.TB, ctx context.Context, db *pgxpool.Pool, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		t.Fatal(err)
	}
}

// 先记录整张配额表，再确认扫描没有操作原有行。
func quotaSnapshot(t testing.TB, ctx context.Context, db *pgxpool.Pool) string {
	t.Helper()
	var snapshot string
	err := db.QueryRow(ctx, `SELECT jsonb_build_array(
		(SELECT jsonb_agg(g ORDER BY g.usage_day) FROM ai_global_usage g),
		(SELECT jsonb_agg(a ORDER BY a.usage_day,a.user_id) FROM ai_daily_usage a)
	)::text`).Scan(&snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestAuditQuotaWithPostgres(t *testing.T) {
	ctx, db := newAuditDB(t)
	auditExec(t, ctx, db, `INSERT INTO users(username,password_hash,disabled)
		VALUES('current','test',false),('disabled','test',true),('removed','test',false)`)
	auditExec(t, ctx, db, `INSERT INTO ai_daily_usage(user_id,usage_day,calls)
		SELECT u.id,CURRENT_DATE-v.ago,v.calls FROM users u JOIN (VALUES
			('current',1,3), ('disabled',1,5),
			('current',2,4), ('removed',2,6),
			('current',3,3), ('current',4,4), ('current',8,1)
		) v(name,ago,calls) ON u.username=v.name`)
	auditExec(t, ctx, db, `INSERT INTO ai_global_usage(usage_day,calls)
		SELECT CURRENT_DATE-v.ago,v.calls FROM (VALUES(1,8),(2,10),(3,2),(5,5)) v(ago,calls)`)
	// 硬删除会级联清理用户配额，仍需保留当天已消耗的全站计数。
	auditExec(t, ctx, db, `DELETE FROM users WHERE username='removed'`)
	before := quotaSnapshot(t, ctx, db)
	report, err := Run(ctx, db, 7)
	if err != nil {
		t.Fatal(err)
	}
	want := make([]QuotaFinding, 0, 4)
	for _, item := range []struct {
		ago   int
		quota QuotaFinding
	}{
		{2, QuotaFinding{GlobalCalls: 10, UserCalls: 4, Issue: "global_surplus"}},
		{3, QuotaFinding{GlobalCalls: 2, UserCalls: 3, Issue: "global_deficit"}},
		{4, QuotaFinding{GlobalCalls: 0, UserCalls: 4, Issue: "global_deficit"}},
		{5, QuotaFinding{GlobalCalls: 5, UserCalls: 0, Issue: "global_surplus"}},
	} {
		if err := db.QueryRow(ctx, `SELECT to_char(CURRENT_DATE-$1::int,'YYYY-MM-DD')`, item.ago).Scan(&item.quota.Day); err != nil {
			t.Fatal(err)
		}
		want = append(want, item.quota)
	}
	got := slices.Clone(report.Quota)
	slices.SortFunc(got, compareQuotaDay)
	slices.SortFunc(want, compareQuotaDay)
	if !report.HasIssues() || report.RoomIssueCount != 0 || !slices.Equal(got, want) {
		t.Fatalf("配额对账结果错误，得到 %+v，预期 %+v", report, want)
	}
	if after := quotaSnapshot(t, ctx, db); after != before {
		t.Fatalf("对账改动了表内数据：原数据 %s，对账后 %s", before, after)
	}
	// 显式修复夹具的两条低计数，只剩可解释的全站差额。
	auditExec(t, ctx, db, `UPDATE ai_global_usage SET calls=3 WHERE usage_day=CURRENT_DATE-3`)
	auditExec(t, ctx, db, `INSERT INTO ai_global_usage(usage_day,calls) VALUES(CURRENT_DATE-4,4)`)
	report, err = Run(ctx, db, 7)
	if err != nil || report.HasIssues() || len(report.Quota) != 2 {
		t.Fatalf("全站差额只应列出供复核：%+v，错误 %v", report, err)
	}
}

func compareQuotaDay(a, b QuotaFinding) int {
	return strings.Compare(a.Day, b.Day)
}

// TestAuditScale 以同一规模的数据比较手工逐日核对和批量对账。
func TestAuditScale(t *testing.T) {
	if os.Getenv("AUDIT_SCALE") != "1" {
		t.Skip("设置 AUDIT_SCALE=1 后测量")
	}
	ctx, db := newAuditDB(t)
	auditExec(t, ctx, db, `INSERT INTO users(username,password_hash)
		SELECT 'audit_scale_'||n,'test' FROM generate_series(1,250) n`)
	auditExec(t, ctx, db, `INSERT INTO ai_daily_usage(user_id,usage_day,calls)
		SELECT u.id,CURRENT_DATE-d,1 FROM users u CROSS JOIN generate_series(0,159) d`)
	auditExec(t, ctx, db, `INSERT INTO ai_global_usage(usage_day,calls)
		SELECT CURRENT_DATE-d,CASE d WHEN 4 THEN 251 WHEN 5 THEN 249 ELSE 250 END
		FROM generate_series(0,159) d`)
	auditExec(t, ctx, db, `INSERT INTO soups(author_id,title,surface,answer,genre,difficulty,horror)
		SELECT min(id),'测试题目','测试汤面','测试汤底','本格',2,1 FROM users`)
	auditExec(t, ctx, db, `INSERT INTO soup_reveals(soup_id,user_id)
		SELECT s.id,u.id FROM users u CROSS JOIN soups s`)
	auditExec(t, ctx, db, `WITH inserted AS (
		INSERT INTO rooms(code,owner_id,host_id,soup_id,mode,max_players,soup_title,soup_surface,soup_answer,soup_genre,soup_difficulty,soup_horror)
		SELECT lpad(to_hex((u.id-1)*4+n),32,'0'),u.id,u.id,s.id,'casual',5,s.title,s.surface,s.answer,s.genre,s.difficulty,s.horror
		FROM users u CROSS JOIN soups s CROSS JOIN generate_series(1,4) n RETURNING id,host_id
	)
	INSERT INTO room_members(room_id,user_id,role,seen_answer) SELECT id,host_id,'host',true FROM inserted`)
	auditExec(t, ctx, db, `ANALYZE ai_daily_usage; ANALYZE ai_global_usage; ANALYZE rooms; ANALYZE room_members`)
	const days = 31
	baseline := func() []QuotaFinding { return manualQuota(t, ctx, db, days) }
	candidate := func() []QuotaFinding {
		report, err := Run(ctx, db, days)
		if err != nil || report.RoomIssueCount != 0 {
			t.Fatalf("测试库应有 1,000 个健康房间：%+v，错误 %v", report, err)
		}
		return report.Quota
	}
	const samples = 25
	times := [2][]time.Duration{}
	methods := [2]func() []QuotaFinding{baseline, candidate}
	for i := range samples + 3 {
		var results [2][]QuotaFinding
		for _, method := range []int{i % 2, 1 - i%2} {
			start := time.Now()
			results[method] = methods[method]()
			if i >= 3 {
				times[method] = append(times[method], time.Since(start))
			}
		}
		slices.SortFunc(results[0], compareQuotaDay)
		slices.SortFunc(results[1], compareQuotaDay)
		if len(results[1]) != 2 || !slices.Equal(results[0], results[1]) {
			t.Fatalf("手工与批量对账结果不同：%+v 和 %+v", results[0], results[1])
		}
	}
	for i, name := range []string{"manual_quota", "batch_quota_and_rooms"} {
		slices.Sort(times[i])
		t.Logf("%s days=%d samples=%d p50=%s p95=%s", name, days, samples, times[i][samples/2], times[i][(95*samples+99)/100-1])
	}
}

// 手工路径按日拆成两条 SQL，与批量路径一样使用一致的只读快照。
func manualQuota(t testing.TB, ctx context.Context, db *pgxpool.Pool, days int) []QuotaFinding {
	t.Helper()
	tx, err := db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var found []QuotaFinding
	for ago := range days {
		var item QuotaFinding
		err := tx.QueryRow(ctx, `SELECT to_char(CURRENT_DATE-$1::int,'YYYY-MM-DD'),
			COALESCE((SELECT calls FROM ai_global_usage WHERE usage_day=CURRENT_DATE-$1::int),0)
		`, ago).Scan(&item.Day, &item.GlobalCalls)
		if err != nil {
			t.Fatal(err)
		}
		err = tx.QueryRow(ctx, `SELECT COALESCE(SUM(calls),0) FROM ai_daily_usage
			WHERE usage_day=CURRENT_DATE-$1::int`, ago).Scan(&item.UserCalls)
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case item.GlobalCalls > item.UserCalls:
			item.Issue = "global_surplus"
		case item.GlobalCalls < item.UserCalls:
			item.Issue = "global_deficit"
		default:
			continue
		}
		found = append(found, item)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return found
}
