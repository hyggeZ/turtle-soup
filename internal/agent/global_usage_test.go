package agent

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"turtle-soup/migrations"
)

// 多账号同时争抢最后一次额度，验证扣减原子性及失败后的额度归属。
func TestGlobalDailyLimitWithPostgres(t *testing.T) {
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
	t.Cleanup(admin.Close)
	schema := "quota_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_, _ = admin.Exec(cleanCtx, "DROP SCHEMA "+schema+" CASCADE")
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

	var users [3]int64
	for i := range users {
		name := schema + "_" + strconv.Itoa(i)
		if err := db.QueryRow(ctx, `INSERT INTO users(username,password_hash) VALUES($1,'test') RETURNING id`, name).Scan(&users[i]); err != nil {
			t.Fatal(err)
		}
	}
	var soupID int64
	if err := db.QueryRow(ctx, `INSERT INTO soups(author_id,title,surface,answer,genre,difficulty,horror)
		VALUES($1,'并发额度测试','汤面','汤底','本格',2,0) RETURNING id`, users[0]).Scan(&soupID); err != nil {
		t.Fatal(err)
	}
	var games [2]int64
	for i := range games {
		if err := db.QueryRow(ctx, `INSERT INTO ai_games(user_id,soup_id,title,surface,answer)
			VALUES($1,$2,'并发额度测试','汤面','汤底') RETURNING id`, users[i+1], soupID).Scan(&games[i]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(ctx, `INSERT INTO ai_global_usage(usage_day,calls) VALUES(CURRENT_DATE,$1)`, globalDailyLimit-1); err != nil {
		t.Fatal(err)
	}

	store := NewStore(db)
	type result struct {
		user int64
		game int64
		err  error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for i := range games {
		go func(i int) {
			<-start
			_, _, _, err := store.Claim(ctx, users[i+1], games[i], "first", "question", "问题")
			results <- result{user: users[i+1], game: games[i], err: err}
		}(i)
	}
	close(start)
	first, second := <-results, <-results
	var winner, loser result
	if first.err == nil {
		winner, loser = first, second
	} else {
		winner, loser = second, first
	}
	if winner.err != nil || !errors.Is(loser.err, ErrDailyLimit) {
		t.Fatalf("应仅允许一次调用，结果：%v、%v", first.err, second.err)
	}
	var globalCalls, accountCalls int
	if err := db.QueryRow(ctx, `SELECT calls FROM ai_global_usage WHERE usage_day=CURRENT_DATE`).Scan(&globalCalls); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `SELECT COALESCE(SUM(calls),0) FROM ai_daily_usage WHERE usage_day=CURRENT_DATE`).Scan(&accountCalls); err != nil {
		t.Fatal(err)
	}
	if globalCalls != globalDailyLimit || accountCalls != 1 {
		t.Fatalf("并发扣减错误：全站=%d，账号合计=%d", globalCalls, accountCalls)
	}

	// 模型失败后释放租约，但已发起的调用仍占用额度。
	store.Release(ctx, winner.user, winner.game, "first")
	store.RecordAttempt(ctx, winner.game, "question", "mock", 1, ErrUnavailable)
	_, _, _, err = store.Claim(ctx, winner.user, winner.game, "retry", "question", "再问一次")
	if !errors.Is(err, ErrDailyLimit) {
		t.Fatalf("失败调用被错误退还额度：%v", err)
	}
	// 主动揭底不调用模型，额度满后仍可结束对局。
	_, _, _, err = store.Claim(ctx, loser.user, loser.game, "reveal", "reveal", "")
	if err != nil {
		t.Fatalf("额度满后无法主动揭底：%v", err)
	}
	if err := db.QueryRow(ctx, `SELECT calls FROM ai_global_usage WHERE usage_day=CURRENT_DATE`).Scan(&globalCalls); err != nil || globalCalls != globalDailyLimit {
		t.Fatalf("揭底改变全站额度：%d，%v", globalCalls, err)
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	(&Handler{}).replyError(c, loser.err)
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("全站额度不足应返回 429，得到 %d", recorder.Code)
	}
}
