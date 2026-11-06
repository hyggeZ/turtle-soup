// 本地命令初始化超级管理员；密码通过环境变量传入，不写入仓库。
package main

import (
	"context"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
	"turtle-soup/migrations"
)

func main() {
	url := os.Getenv("DATABASE_URL")
	password := os.Getenv("SUPER_ADMIN_PASSWORD")
	if url == "" || password == "" {
		log.Fatal("请设置 DATABASE_URL 和 SUPER_ADMIN_PASSWORD")
	}
	db, err := pgxpool.New(context.Background(), url)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	if err = migrations.Apply(context.Background(), db); err != nil {
		log.Fatal(err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		log.Fatal(err)
	}
	tx, err := db.Begin(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	var id int64
	err = tx.QueryRow(context.Background(), `
		INSERT INTO users(username,password_hash,role,is_admin,disabled)
		VALUES('king',$1,'super_admin',TRUE,FALSE)
		ON CONFLICT(username) DO UPDATE SET password_hash=$1,role='super_admin',is_admin=TRUE,disabled=FALSE
		RETURNING id`, string(hash)).Scan(&id)
	if err != nil {
		log.Fatal(err)
	}
	if _, err = tx.Exec(context.Background(), `DELETE FROM sessions WHERE user_id=$1`, id); err != nil {
		log.Fatal(err)
	}
	if err = tx.Commit(context.Background()); err != nil {
		log.Fatal(err)
	}
	log.Print("超级管理员 king 已设置；旧会话已失效")
}
