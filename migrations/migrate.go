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

//go:embed 008_soup_manage.sql
var soupManageSQL string

// Apply 只执行尚未应用的脚本；锁避免多进程同时改表。
func Apply(ctx context.Context, db *pgxpool.Pool) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(20261111)`); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`)
	if err != nil {
		return err
	}
	scripts := []string{authSQL, forumSQL, gameSQL, rolesSQL, gameIntegritySQL, agentSQL, commentsSQL, soupManageSQL}
	for i, script := range scripts {
		version := i + 1
		var applied bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, version).Scan(&applied); err != nil {
			return err
		}
		if applied {
			continue
		}
		if _, err := tx.Exec(ctx, script); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES($1)`, version); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
