package main

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// 使用隔离测试库，迁移锁由另一条连接持有。
func TestStartupMigrationDeadline(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("需要独立的 PostgreSQL 测试库")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.MaxConns, config.MinConns, config.MinIdleConns = 2, 0, 0
	db, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	lock, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback(ctx)
	if _, err := lock.Exec(ctx, "SELECT pg_advisory_xact_lock(20261111)"); err != nil {
		t.Fatal(err)
	}
	if err := applyStartupMigrations(ctx, db, 150*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("持锁期间应返回超时，实际为 %v", err)
	}
}
