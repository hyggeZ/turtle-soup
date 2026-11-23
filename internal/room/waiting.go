package room

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// LockWaitingBySoup 在调用方事务中按房间 ID 排序加锁。持有题目锁后补查时使用 NOWAIT。
func LockWaitingBySoup(ctx context.Context, tx pgx.Tx, soupID int64, nowait bool) error {
	query := `SELECT id FROM rooms WHERE soup_id=$1 AND status='waiting' ORDER BY id FOR UPDATE`
	if nowait {
		query += ` NOWAIT`
	}
	rows, err := tx.Query(ctx, query, soupID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return err
		}
	}
	return rows.Err()
}

// CloseWaitingBySoup 关闭已锁定的等待房间，调用方提交事务后可用返回的 ID 发通知。
func CloseWaitingBySoup(ctx context.Context, tx pgx.Tx, soupID int64) ([]int64, error) {
	rows, err := tx.Query(ctx, `UPDATE rooms SET status='closed',closed_at=NOW()
		WHERE soup_id=$1 AND status='waiting' RETURNING id`, soupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	closed := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		closed = append(closed, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return closed, nil
}
