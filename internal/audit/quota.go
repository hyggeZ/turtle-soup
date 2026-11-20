package audit

import (
	"context"

	"github.com/jackc/pgx/v5"
)

type QuotaFinding struct {
	Day         string `json:"day"`
	GlobalCalls int64  `json:"globalCalls"`
	UserCalls   int64  `json:"userCalls"`
	Issue       string `json:"issue"`
}

// 使用配额写入时相同的数据库日期，包含今天及之前 days-1 天。
// 硬删除账号会移除用户用量，因此全站合计较高时只记录待复核。
func checkQuota(ctx context.Context, tx pgx.Tx, days int) ([]QuotaFinding, error) {
	rows, err := tx.Query(ctx, `WITH user_usage AS (
		SELECT usage_day, SUM(calls) AS calls FROM ai_daily_usage
		WHERE usage_day BETWEEN CURRENT_DATE-($1::int-1) AND CURRENT_DATE GROUP BY usage_day
	), global_usage AS (
		SELECT usage_day, calls FROM ai_global_usage
		WHERE usage_day BETWEEN CURRENT_DATE-($1::int-1) AND CURRENT_DATE
	)
	SELECT to_char(COALESCE(g.usage_day,u.usage_day),'YYYY-MM-DD'), COALESCE(g.calls,0), COALESCE(u.calls,0)
	FROM global_usage g FULL JOIN user_usage u USING (usage_day)
	WHERE COALESCE(g.calls,0) <> COALESCE(u.calls,0)
	ORDER BY COALESCE(g.usage_day,u.usage_day) DESC`, days)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]QuotaFinding, 0)
	for rows.Next() {
		var item QuotaFinding
		if err := rows.Scan(&item.Day, &item.GlobalCalls, &item.UserCalls); err != nil {
			return nil, err
		}
		item.Issue = "global_surplus"
		if item.GlobalCalls < item.UserCalls {
			item.Issue = "global_deficit"
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
