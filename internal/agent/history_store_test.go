package agent

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"turtle-soup/migrations"
)

// 问答窗口按局隔离，只读取问题事件，并保持原有的提问顺序。
func TestHistoryWindowWithPostgres(t *testing.T) {
	ctx, db := newHistoryTestDB(t)
	if _, err := db.Exec(ctx, `INSERT INTO users(username,password_hash)
		VALUES('history_author','test'),('history_long','test'),('history_short','test');
		INSERT INTO soups(author_id,title,surface,answer,genre,difficulty,horror)
		SELECT id,'历史测试','汤面','汤底','本格',2,1 FROM users WHERE username='history_author';
		INSERT INTO ai_games(user_id,soup_id,title,surface,answer)
		SELECT u.id,s.id,s.title,s.surface,s.answer FROM users u CROSS JOIN soups s
		WHERE u.username IN ('history_long','history_short')`); err != nil {
		t.Fatal(err)
	}
	var longGame, shortGame int64
	if err := db.QueryRow(ctx, `SELECT
		max(g.id) FILTER (WHERE u.username='history_long'),
		max(g.id) FILTER (WHERE u.username='history_short')
		FROM ai_games g JOIN users u ON u.id=g.user_id`).Scan(&longGame, &shortGame); err != nil {
		t.Fatal(err)
	}
	alias := "我们之前约定道具的代号叫海燕吗？"
	// 按轮交错写入还原、揭底和另一局的问题，防止先截事件再筛问题。
	if _, err := db.Exec(ctx, `INSERT INTO ai_events(game_id,operation_id,kind,body,result)
		SELECT e.game_id,'history-'||n||'-'||e.seq,e.kind,e.body,e.result
		FROM generate_series(1,30) n CROSS JOIN LATERAL (VALUES
			(1,$1::bigint,'question','长局问题 '||n,CASE WHEN n%2=0 THEN '是' ELSE '否' END),
			(2,$1::bigint,'restore','还原输入 '||n,'不完整'),
			(3,$1::bigint,'reveal','','已查看汤底'),
			(4,$2::bigint,'question',CASE WHEN n=1 THEN $3::text ELSE '短局问题 '||n END,'是')
		) e(seq,game_id,kind,body,result)
		WHERE e.game_id=$1 OR n<=14 ORDER BY n,e.seq`, longGame, shortGame, alias); err != nil {
		t.Fatal(err)
	}
	store := NewStore(db)
	long, err := store.History(ctx, longGame)
	if err != nil || len(long) != 24 {
		t.Fatalf("长局应保留最近 24 条问题，得到 %d 条，错误 %v", len(long), err)
	}
	for i, item := range long {
		n := i + 7
		answer := "否"
		if n%2 == 0 {
			answer = "是"
		}
		if item.Question != fmt.Sprintf("长局问题 %d", n) || item.Answer != answer {
			t.Fatalf("第 %d 条应来自长局第 %d 轮，得到 %+v", i+1, n, item)
		}
	}
	short, err := store.History(ctx, shortGame)
	if err != nil || len(short) != 14 {
		t.Fatalf("短局应保留全部 14 条问题，得到 %d 条，错误 %v", len(short), err)
	}
	if short[0].Question != alias || short[0].Answer != "是" || short[13].Question != "短局问题 14" {
		t.Fatalf("短局上下文遗漏早期代号或最后一轮：%+v", short)
	}
	if _, err := db.Exec(ctx, `INSERT INTO ai_events(game_id,operation_id,kind,body,result)
		SELECT $1,'history-extra-'||n,'question','长局问题 '||n,'是' FROM generate_series(31,70) n`, longGame); err != nil {
		t.Fatal(err)
	}
	expanded, err := store.readHistory(ctx, longGame, contextHistoryTurns)
	if err != nil || len(expanded) != contextHistoryTurns || expanded[0].Question != "长局问题 7" ||
		expanded[len(expanded)-1].Question != "长局问题 70" {
		t.Fatalf("扩展窗口应按正序读取同局最近 64 条问题：数量=%d，错误=%v", len(expanded), err)
	}
}

// 使用本测试独立 schema，避免读写其他对局。
func newHistoryTestDB(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("需要 TEST_DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	schema := fmt.Sprintf("ai_history_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.Exec(clean, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	db, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := migrations.Apply(ctx, db); err != nil {
		t.Fatal(err)
	}
	return ctx, db
}
