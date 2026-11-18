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
	var users, rooms, games, waitingIndex, answerIndex, replyIndex string
	err = db.QueryRow(ctx, `SELECT to_regclass('users'),to_regclass('rooms'),to_regclass('ai_games'),
		to_regclass('rooms_soup_waiting_idx'),to_regclass('room_events_answer_parent_idx'),to_regclass('room_events_reply_parent_idx')`).
		Scan(&users, &rooms, &games, &waitingIndex, &answerIndex, &replyIndex)
	if err != nil || users == "" || rooms == "" || games == "" || waitingIndex == "" || answerIndex == "" || replyIndex == "" {
		t.Fatalf("核心表或索引不完整: users=%q rooms=%q ai_games=%q waiting=%q answer=%q reply=%q err=%v", users, rooms, games, waitingIndex, answerIndex, replyIndex, err)
	}
	var count int
	if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil || count != 17 {
		t.Fatalf("迁移版本记录不完整: count=%d err=%v", count, err)
	}
}

// 从前八版数据库升级，并重复执行以验证新迁移不会重复应用。
func TestApplyFromVersionEight(t *testing.T) {
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
	schema := "old_" + strconv.FormatInt(time.Now().UnixNano(), 10)
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
	oldScripts := []string{authSQL, forumSQL, gameSQL, rolesSQL, gameIntegritySQL, agentSQL, commentsSQL, soupManageSQL}
	for i, script := range oldScripts {
		if _, err := db.Exec(ctx, script); err != nil {
			t.Fatalf("旧版迁移 %d 失败：%v", i+1, err)
		}
	}
	var host, player int64
	for i, calls := range []int{17, 23} {
		var user int64
		name := schema + "_" + strconv.Itoa(i)
		if err := db.QueryRow(ctx, `INSERT INTO users(username,password_hash) VALUES($1,'test') RETURNING id`, name).Scan(&user); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			host = user
		} else {
			player = user
		}
		if _, err := db.Exec(ctx, `INSERT INTO ai_daily_usage(user_id,usage_day,calls) VALUES($1,CURRENT_DATE,$2)`, user, calls); err != nil {
			t.Fatal(err)
		}
	}
	// 旧库中已有问答时，新增索引不能改变事件或阻断升级。
	var soup, room, question int64
	if err := db.QueryRow(ctx, `INSERT INTO soups(author_id,title,surface,answer,genre,difficulty,horror)
		VALUES($1,'旧房间题','汤面','汤底','本格',2,0) RETURNING id`, host).Scan(&soup); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `INSERT INTO rooms(code,owner_id,host_id,soup_id,mode,max_players,public)
		VALUES('BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB',$1,$1,$2,'casual',4,TRUE) RETURNING id`, host, soup).Scan(&room); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `INSERT INTO room_events(room_id,user_id,kind,body)
		VALUES($1,$2,'question','原有问题') RETURNING id`, room, player).Scan(&question); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO room_events(room_id,user_id,kind,result,parent_id)
		VALUES($1,$2,'answer','是',$3)`, room, host, question); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO schema_migrations(version) SELECT generate_series(1,8)`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := Apply(ctx, db); err != nil {
			t.Fatalf("第 %d 次升级失败：%v", i+1, err)
		}
	}
	var count int
	var table, answerIndex, replyIndex string
	if err := db.QueryRow(ctx, `SELECT COUNT(*), to_regclass('ai_global_usage'),
		to_regclass('room_events_answer_parent_idx'),to_regclass('room_events_reply_parent_idx') FROM schema_migrations`).
		Scan(&count, &table, &answerIndex, &replyIndex); err != nil || count != 17 || table == "" || answerIndex == "" || replyIndex == "" {
		t.Fatalf("升级结果错误：版本数=%d，新表=%q，回答索引=%q，回复索引=%q，错误=%v", count, table, answerIndex, replyIndex, err)
	}
	var events int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM room_events WHERE room_id=$1`, room).Scan(&events); err != nil || events != 2 {
		t.Fatalf("升级后旧房间问答不完整：events=%d err=%v", events, err)
	}
	var calls int
	if err := db.QueryRow(ctx, `SELECT calls FROM ai_global_usage WHERE usage_day=CURRENT_DATE`).Scan(&calls); err != nil || calls != 40 {
		t.Fatalf("旧版当日用量回填错误：calls=%d，错误=%v", calls, err)
	}
	var displayName, bio string
	if err := db.QueryRow(ctx, `SELECT display_name,bio FROM users WHERE username=$1`, schema+"_0").Scan(&displayName, &bio); err != nil || displayName != "" || bio != "" {
		t.Fatalf("旧账号公开资料迁移错误：展示名=%q，简介=%q，错误=%v", displayName, bio, err)
	}
}
