// 管理员资格只能由持有数据库连接的本地命令授予。
package main

import (
	"context"
	"flag"
	"github.com/jackc/pgx/v5/pgxpool"
	"log"
	"os"
	"turtle-soup/migrations"
)

func main() {
	name := flag.String("username", "", "要设为管理员的用户名")
	flag.Parse()
	if *name == "" {
		log.Fatal("请用 -username 指定已有账号")
	}
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		log.Fatal("请先设置 DATABASE_URL")
	}
	db, err := pgxpool.New(context.Background(), url)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	if err = migrations.Apply(context.Background(), db); err != nil {
		log.Fatal(err)
	}
	tag, err := db.Exec(context.Background(), `UPDATE users SET is_admin=TRUE WHERE username=$1`, *name)
	if err != nil {
		log.Fatal(err)
	}
	if tag.RowsAffected() != 1 {
		log.Fatal("账号不存在")
	}
	log.Printf("已将 %s 设为管理员", *name)
}
