// Package migrations 管理启动时需要补建的数据表。
package migrations

import (
	"context"
	_ "embed"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed 002_forum.sql
var forumSQL string

//go:embed 003_game.sql
var gameSQL string

// Apply 补建汤题和房间表；账号基础表由 compose 首次启动时创建。
func Apply(ctx context.Context, db *pgxpool.Pool) error {
	if _, err := db.Exec(ctx, forumSQL); err != nil {
		return err
	}
	_, err := db.Exec(ctx, gameSQL)
	return err
}
