package migrations

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// 从空 schema 启动两次，验证部署时不依赖 Docker 的初始化脚本。
func TestApplyFromEmptySchema(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("未设置 TEST_DATABASE_URL")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()

	schema := "fresh_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")

	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	db, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	for i := 0; i < 2; i++ {
		if err := Apply(ctx, db); err != nil {
			t.Fatalf("第 %d 次初始化失败: %v", i+1, err)
		}
	}
	var users, rooms, games string
	err = db.QueryRow(ctx, `SELECT to_regclass('users'),to_regclass('rooms'),to_regclass('ai_games')`).Scan(&users, &rooms, &games)
	if err != nil || users == "" || rooms == "" || games == "" {
		t.Fatalf("核心数据表不完整: users=%q rooms=%q ai_games=%q err=%v", users, rooms, games, err)
	}
}
