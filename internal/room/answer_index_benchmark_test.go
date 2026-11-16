package room_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"turtle-soup/migrations"
)

// BenchmarkAnswerInsert 在相同事件规模下测量新增回答的写入成本和索引空间。
// 对比索引前后时使用固定的 -benchtime=500x，确保每轮写入相同数量。
func BenchmarkAnswerInsert(b *testing.B) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		b.Skip("需要 TEST_DATABASE_URL")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(admin.Close)
	schema := fmt.Sprintf("bench_answer_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE") })

	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		b.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	db, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(db.Close)
	if err := migrations.Apply(ctx, db); err != nil {
		b.Fatal(err)
	}

	var host, player, soup, room int64
	for _, item := range []struct {
		name string
		id   *int64
	}{{"bench_answer_host", &host}, {"bench_answer_player", &player}} {
		if err := db.QueryRow(ctx, `INSERT INTO users(username,password_hash) VALUES($1,'test') RETURNING id`, item.name).Scan(item.id); err != nil {
			b.Fatal(err)
		}
	}
	if err := db.QueryRow(ctx, `INSERT INTO soups(author_id,title,surface,answer,genre,difficulty,horror)
		VALUES($1,'写入基准题','汤面','汤底','本格',2,0) RETURNING id`, host).Scan(&soup); err != nil {
		b.Fatal(err)
	}
	if err := db.QueryRow(ctx, `INSERT INTO rooms(code,owner_id,host_id,soup_id,mode,max_players,public)
		VALUES('AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA',$1,$1,$2,'casual',4,TRUE) RETURNING id`, host, soup).Scan(&room); err != nil {
		b.Fatal(err)
	}
	// 预置 2500 组问答，与长局快照测试的初始数据规模一致。
	if _, err := db.Exec(ctx, `WITH questions AS (
		INSERT INTO room_events(room_id,user_id,kind,body)
		SELECT $1,$2,'question','已有问题' FROM generate_series(1,2500)
		RETURNING id
	) INSERT INTO room_events(room_id,user_id,kind,result,parent_id)
	SELECT $1,$3,'answer','是',id FROM questions`, room, player, host); err != nil {
		b.Fatal(err)
	}
	rows, err := db.Query(ctx, `INSERT INTO room_events(room_id,user_id,kind,body)
		SELECT $1,$2,'question','新问题' FROM generate_series(1,$3::int) RETURNING id`, room, player, b.N)
	if err != nil {
		b.Fatal(err)
	}
	parents := make([]int64, 0, b.N)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			b.Fatal(err)
		}
		parents = append(parents, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		b.Fatal(err)
	}
	rows.Close()
	if len(parents) != b.N {
		b.Fatalf("待回答问题数量错误：%d", len(parents))
	}
	if _, err := db.Exec(ctx, `ANALYZE room_events`); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for _, parent := range parents {
		if _, err := db.Exec(ctx, `INSERT INTO room_events(room_id,user_id,kind,result,parent_id)
			VALUES($1,$2,'answer','是',$3)`, room, host, parent); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	var answerIndex, allIndexes int64
	if err := db.QueryRow(ctx, `SELECT COALESCE(pg_relation_size(to_regclass('room_events_answer_parent_idx')),0),
		pg_indexes_size('room_events'::regclass)`).Scan(&answerIndex, &allIndexes); err != nil {
		b.Fatal(err)
	}
	b.ReportMetric(float64(answerIndex), "answer-index-B")
	b.ReportMetric(float64(allIndexes), "all-indexes-B")
}
