package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"turtle-soup/internal/audit"
)

// 对账不执行启动迁移，也不需要超级管理员密码。
func runAudit(args []string, out io.Writer) (bool, error) {
	flags := flag.NewFlagSet("audit", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	days := flags.Int("days", 7, "核对最近多少天的 AI 配额")
	if flags.Parse(args) != nil || flags.NArg() != 0 || *days < 1 || *days > 31 {
		return false, errors.New("用法：turtle-admin audit [-days 1至31]")
	}
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return false, errors.New("请设置 DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	db, err := pgxpool.New(ctx, url)
	if err != nil {
		return false, errors.New("数据库地址无效")
	}
	defer db.Close()
	report, err := audit.Run(ctx, db, *days)
	if err != nil {
		return false, err
	}
	if err := json.NewEncoder(out).Encode(report); err != nil {
		return false, err
	}
	return report.HasIssues(), nil
}
