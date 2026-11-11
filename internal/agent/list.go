package agent

import (
	"context"
	"time"
)

// GameSummary 仅用于找回入口，不携带汤面、汤底和对局事件。
type GameSummary struct {
	ID         int64      `json:"id"`
	Title      string     `json:"title"`
	Status     string     `json:"status"`
	CreatedAt  time.Time  `json:"createdAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

// ListMine 返回全部进行中的单人局和最近结束的 20 局，按各组 ID 倒序。
// 单账号同时进行的局数由 Create 限制为 5；列表始终有固定上限。
func (s *Store) ListMine(ctx context.Context, user int64) ([]GameSummary, error) {
	items := make([]GameSummary, 0, 25)
	for _, part := range []struct {
		status string
		limit  int
	}{{"active", 5}, {"finished", 20}} {
		rows, err := s.db.Query(ctx, `SELECT id,title,status,created_at,finished_at
			FROM ai_games WHERE user_id=$1 AND status=$2 ORDER BY id DESC LIMIT $3`, user, part.status, part.limit)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var item GameSummary
			if err := rows.Scan(&item.ID, &item.Title, &item.Status, &item.CreatedAt, &item.FinishedAt); err != nil {
				rows.Close()
				return nil, err
			}
			items = append(items, item)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return items, nil
}
