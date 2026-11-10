package room

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"turtle-soup/migrations"
)

// 在独立 schema 中模拟正常停机和异常退出，验证状态与竞答倒计时。
func TestRoomRecoveryWithPostgres(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("需要 TEST_DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := "recovery_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_, _ = admin.Exec(cleanupCtx, "DROP SCHEMA "+schema+" CASCADE")
	}()
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
	if err := migrations.Apply(ctx, db); err != nil {
		t.Fatal(err)
	}
	var host, player, soup, id int64
	if err := db.QueryRow(ctx, `INSERT INTO users(username,password_hash) VALUES('host','test') RETURNING id`).Scan(&host); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `INSERT INTO users(username,password_hash) VALUES('player','test') RETURNING id`).Scan(&player); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `INSERT INTO soups(author_id,title,surface,answer,genre,difficulty,horror)
		VALUES($1,'测试题','测试汤面','测试汤底','本格',2,0) RETURNING id`, host).Scan(&soup); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `INSERT INTO rooms(code,owner_id,host_id,soup_id,mode,max_players,status,turn_user_id,turn_deadline)
		VALUES('RECOVERY_TEST', $1,$1,$2,'competitive',2,'active',$3,NOW()+INTERVAL '30 seconds') RETURNING id`, host, soup, player).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO room_members(room_id,user_id,role) VALUES($1,$2,'host'),($1,$3,'player')`, id, host, player); err != nil {
		t.Fatal(err)
	}
	var original time.Time
	if err := db.QueryRow(ctx, `SELECT turn_deadline FROM rooms WHERE id=$1`, id).Scan(&original); err != nil {
		t.Fatal(err)
	}
	if err := PauseActive(ctx, db, time.Now()); err != nil {
		t.Fatal(err)
	}
	var status string
	var paused, deadline time.Time
	if err := db.QueryRow(ctx, `SELECT status,paused_at,turn_deadline FROM rooms WHERE id=$1`, id).Scan(&status, &paused, &deadline); err != nil {
		t.Fatal(err)
	}
	if status != "paused" || paused.IsZero() || !deadline.Equal(original) {
		t.Fatalf("正常停机未保存倒计时：status=%s deadline=%s", status, deadline)
	}
	if _, err := db.Exec(ctx, `UPDATE rooms SET paused_at=NOW()-INTERVAL '45 seconds' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(db, nil)
	h.resumeIfHost(id, host)
	var remaining float64
	if err := db.QueryRow(ctx, `SELECT status,EXTRACT(EPOCH FROM turn_deadline-NOW()) FROM rooms WHERE id=$1`, id).Scan(&status, &remaining); err != nil {
		t.Fatal(err)
	}
	if status != "active" || remaining < 70 || remaining > 77 {
		t.Fatalf("主持重连未顺延停机时间：status=%s remaining=%.1f", status, remaining)
	}
	// 异常退出无法记录停机时刻；启动后至少保留完整回合。
	if _, err := db.Exec(ctx, `UPDATE rooms SET turn_deadline=NOW()-INTERVAL '15 seconds' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if err := RecoverActive(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `SELECT status,paused_at,EXTRACT(EPOCH FROM turn_deadline-NOW()) FROM rooms WHERE id=$1`, id).Scan(&status, &paused, &remaining); err != nil {
		t.Fatal(err)
	}
	if status != "paused" || paused.IsZero() || remaining < 59 || remaining > 61 {
		t.Fatalf("异常退出恢复后回合已过期：status=%s remaining=%.1f", status, remaining)
	}
	if err := RecoverActive(ctx, db); err != nil {
		t.Fatal(err)
	}
	var secondPause time.Time
	if err := db.QueryRow(ctx, `SELECT paused_at FROM rooms WHERE id=$1`, id).Scan(&secondPause); err != nil || !secondPause.Equal(paused) {
		t.Fatalf("重复恢复改变了暂停时刻：%s %v", secondPause, err)
	}
	if _, err := db.Exec(ctx, `UPDATE rooms SET paused_at=NOW()-INTERVAL '10 seconds' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	h.resumeIfHost(id, host)
	if err := db.QueryRow(ctx, `SELECT status,EXTRACT(EPOCH FROM turn_deadline-NOW()) FROM rooms WHERE id=$1`, id).Scan(&status, &remaining); err != nil {
		t.Fatal(err)
	}
	if status != "active" || remaining < 69 || remaining > 71 {
		t.Fatalf("异常退出后重连倒计时跳动：status=%s remaining=%.1f", status, remaining)
	}
}
