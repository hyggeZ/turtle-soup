package forum

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// PublishLimitError 带上数据库计算的最短等待时间，供接口返回 429 和 Retry-After。
type PublishLimitError struct {
	RetrySeconds int
}

func (e *PublishLimitError) Error() string { return "发布过于频繁" }

func lockPublisher(ctx context.Context, tx pgx.Tx, user int64) error {
	var id int64
	return tx.QueryRow(ctx, `SELECT id FROM users WHERE id=$1 FOR NO KEY UPDATE`, user).Scan(&id)
}

// publicationWait 取限额内最早的一条，计算从现在到它过期还需等待多久。
// 表名只允许这里的两个固定值，不能由 HTTP 请求传入。
func publicationWait(ctx context.Context, tx pgx.Tx, table string, user int64, seconds, max int) (int, error) {
	if table != "soups" && table != "comments" {
		return 0, errors.New("无效的发布类型")
	}
	query := `SELECT GREATEST(1, CEIL(EXTRACT(EPOCH FROM
		(created_at + ($2::bigint * INTERVAL '1 second') - statement_timestamp())))::integer)
		FROM ` + table + `
		WHERE author_id=$1 AND created_at > statement_timestamp()-($2::bigint * INTERVAL '1 second')
		ORDER BY created_at DESC,id DESC OFFSET $3 LIMIT 1`
	var wait int
	err := tx.QueryRow(ctx, query, user, seconds, max-1).Scan(&wait)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return wait, err
}
