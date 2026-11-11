package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"turtle-soup/migrations"
)

// 不同题目同时开局时，账号锁必须让五局上限保持准确。
func TestConcurrentCreateHasFiveGameLimit(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("需要 TEST_DATABASE_URL")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := migrations.Apply(ctx, db); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().UnixNano()
	var author, player int64
	for i, target := range []*int64{&author, &player} {
		name := fmt.Sprintf("test_ai_limit_%d_%d", stamp, i)
		if err := db.QueryRow(ctx, `INSERT INTO users(username,password_hash) VALUES($1,'test') RETURNING id`, name).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	soups := make([]int64, 6)
	t.Cleanup(func() {
		_, _ = db.Exec(ctx, `DELETE FROM ai_games WHERE user_id=$1`, player)
		for _, id := range soups {
			if id != 0 {
				_, _ = db.Exec(ctx, `DELETE FROM soups WHERE id=$1`, id)
			}
		}
		_, _ = db.Exec(ctx, `DELETE FROM users WHERE id IN ($1,$2)`, author, player)
	})
	for i := range soups {
		title := fmt.Sprintf("并发题目%d", i)
		if err := db.QueryRow(ctx, `INSERT INTO soups(author_id,title,surface,answer,genre,difficulty,horror)
			VALUES($1,$2,'并发测试汤面','并发测试汤底','本格',2,1) RETURNING id`, author, title).Scan(&soups[i]); err != nil {
			t.Fatal(err)
		}
	}
	store := NewStore(db)
	testCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	type result struct {
		soup int64
		game int64
		err  error
	}
	results := make(chan result, len(soups))
	for _, soup := range soups {
		go func(soup int64) {
			game, err := store.Create(testCtx, player, soup)
			results <- result{soup: soup, game: game, err: err}
		}(soup)
	}
	var accepted, rejected int
	var first result
	for range soups {
		item := <-results
		switch {
		case item.err == nil:
			accepted++
			first = item
		case errors.Is(item.err, ErrActiveGamesLimit):
			rejected++
		default:
			t.Fatalf("并发开局失败：%v", item.err)
		}
	}
	var active int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM ai_games WHERE user_id=$1 AND status='active'`, player).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if accepted != 5 || rejected != 1 || active != 5 {
		t.Fatalf("跨题并发超过上限：成功 %d 拒绝 %d 活跃 %d", accepted, rejected, active)
	}
	again, err := store.Create(ctx, player, first.soup)
	if err != nil || again != first.game {
		t.Fatalf("达到上限后同题幂等开局失败：%d %v", again, err)
	}
}
