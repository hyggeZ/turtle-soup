// Package migrations 管理启动时需要补建的数据表。
package migrations

import (
	"context"
	_ "embed"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed 002_forum.sql
var forumSQL string

// Apply 仅补建论坛相关表；账号表由 compose 首次启动时创建。
func Apply(ctx context.Context, db *pgxpool.Pool) error {
	_, err := db.Exec(ctx, forumSQL)
	return err
}
