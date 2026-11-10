// Package migrations 管理启动时需要补建的数据表。
package migrations

import (
	"context"
	_ "embed"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed 001_auth.sql
var authSQL string

//go:embed 002_forum.sql
var forumSQL string

//go:embed 003_game.sql
var gameSQL string

//go:embed 004_roles.sql
var rolesSQL string

//go:embed 005_game_integrity.sql
var gameIntegritySQL string

//go:embed 006_agent.sql
var agentSQL string

//go:embed 007_comments.sql
var commentsSQL string

// Apply 按依赖顺序补建账号、社区和对局数据表。
func Apply(ctx context.Context, db *pgxpool.Pool) error {
	if _, err := db.Exec(ctx, authSQL); err != nil {
		return err
	}
	if _, err := db.Exec(ctx, forumSQL); err != nil {
		return err
	}
	if _, err := db.Exec(ctx, gameSQL); err != nil {
		return err
	}
	if _, err := db.Exec(ctx, rolesSQL); err != nil {
		return err
	}
	if _, err := db.Exec(ctx, gameIntegritySQL); err != nil {
		return err
	}
	if _, err := db.Exec(ctx, agentSQL); err != nil {
		return err
	}
	_, err := db.Exec(ctx, commentsSQL)
	return err
}
