package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"turtle-soup/internal/account"
	"turtle-soup/migrations"
)

type testDecider struct {
	started chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (d *testDecider) DecideQuestion(_ context.Context, _ Soup, _ []Exchange, question string) (Decision, error) {
	d.calls.Add(1)
	if question == "first" {
		d.started <- struct{}{}
		<-d.release
	}
	if question == "fail" {
		return Decision{}, ErrUnavailable
	}
	return Decision{Result: "是", Model: "mock", PromptVersion: PromptVersion}, nil
}

func (d *testDecider) JudgeRestore(_ context.Context, _ Soup, _ []Exchange, _ string) (Decision, error) {
	d.calls.Add(1)
	return Decision{Result: "正确", Model: "mock", PromptVersion: PromptVersion}, nil
}

// 真实数据库验收用户隔离、并发互斥、操作幂等和模型失败后的状态。
func TestSoloGameWithPostgres(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("需要 TEST_DATABASE_URL")
	}
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	db, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := migrations.Apply(ctx, db); err != nil {
		t.Fatal(err)
	}
	schema, err := os.ReadFile("../../migrations/006_agent.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, string(schema)); err != nil {
		t.Fatal(err)
	}
	decider := &testDecider{started: make(chan struct{}, 1), release: make(chan struct{})}
	auth := account.NewHandler(account.NewPGStore(db), false)
	router := gin.New()
	NewHandler(NewStore(db), auth, decider).Register(router)

	var users [3]int64
	var cookies [3]*http.Cookie
	stamp := time.Now().UnixNano()
	var soupID, gameID, soupID2, gameID2, soupID3, gameID3, soupID4, gameID4, soupID5, gameID5 int64
	t.Cleanup(func() {
		if gameID5 != 0 {
			_, _ = db.Exec(ctx, `DELETE FROM ai_games WHERE id=$1`, gameID5)
		}
		if gameID4 != 0 {
			_, _ = db.Exec(ctx, `DELETE FROM ai_games WHERE id=$1`, gameID4)
		}
		if gameID3 != 0 {
			_, _ = db.Exec(ctx, `DELETE FROM ai_games WHERE id=$1`, gameID3)
		}
		if gameID2 != 0 {
			_, _ = db.Exec(ctx, `DELETE FROM ai_games WHERE id=$1`, gameID2)
		}
		if gameID != 0 {
			_, _ = db.Exec(ctx, `DELETE FROM ai_games WHERE id=$1`, gameID)
		}
		if soupID5 != 0 {
			_, _ = db.Exec(ctx, `DELETE FROM soups WHERE id=$1`, soupID5)
		}
		if soupID4 != 0 {
			_, _ = db.Exec(ctx, `DELETE FROM soups WHERE id=$1`, soupID4)
		}
		if soupID3 != 0 {
			_, _ = db.Exec(ctx, `DELETE FROM soups WHERE id=$1`, soupID3)
		}
		if soupID2 != 0 {
			_, _ = db.Exec(ctx, `DELETE FROM soups WHERE id=$1`, soupID2)
		}
		if soupID != 0 {
			_, _ = db.Exec(ctx, `DELETE FROM soups WHERE id=$1`, soupID)
		}
		for _, user := range users {
			if user != 0 {
				_, _ = db.Exec(ctx, `DELETE FROM users WHERE id=$1`, user)
			}
		}
	})
	for i := range users {
		name := fmt.Sprintf("test_ai_%d_%d", stamp, i)
		if err := db.QueryRow(ctx, `INSERT INTO users(username,password_hash) VALUES($1,'test') RETURNING id`, name).Scan(&users[i]); err != nil {
			t.Fatal(err)
		}
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			t.Fatal(err)
		}
		token := base64.RawURLEncoding.EncodeToString(raw)
		hash := sha256.Sum256([]byte(token))
		if _, err := db.Exec(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,NOW()+INTERVAL '1 hour')`, hash[:], users[i]); err != nil {
			t.Fatal(err)
		}
		cookies[i] = &http.Cookie{Name: "turtle_session", Value: token}
	}
	if err := db.QueryRow(ctx, `INSERT INTO soups(author_id,title,surface,answer,genre,difficulty,horror) VALUES($1,'测试题','测试汤面','测试汤底','本格',2,1) RETURNING id`, users[0]).Scan(&soupID); err != nil {
		t.Fatal(err)
	}
	call := func(method, path string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(method, path, bytes.NewReader(data))
		request.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			request.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}

	created := call(http.MethodPost, "/api/ai/solo", map[string]any{"soupId": soupID}, cookies[1])
	if created.Code != http.StatusCreated {
		t.Fatalf("创建失败：%d %s", created.Code, created.Body.String())
	}
	var output struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	gameID = output.ID
	gamePath := fmt.Sprintf("/api/ai/solo/%d", gameID)
	view := call(http.MethodGet, gamePath, nil, cookies[1])
	if view.Code != http.StatusOK || strings.Contains(view.Body.String(), "测试汤底") {
		t.Fatalf("进行中泄露汤底：%d %s", view.Code, view.Body.String())
	}
	if denied := call(http.MethodGet, gamePath, nil, cookies[2]); denied.Code != http.StatusNotFound {
		t.Fatalf("其他用户读取对局：%d %s", denied.Code, denied.Body.String())
	}

	actionPath := gamePath + "/actions"
	action := func(kind, body, key string) *httptest.ResponseRecorder {
		return call(http.MethodPost, actionPath, map[string]string{"type": kind, "body": body, "operationId": key}, cookies[1])
	}
	first := make(chan *httptest.ResponseRecorder, 1)
	go func() { first <- action("question", "first", "question_001") }()
	select {
	case <-decider.started:
	case <-time.After(3 * time.Second):
		t.Fatal("模型未开始处理")
	}
	if busy := action("question", "second", "question_002"); busy.Code != http.StatusConflict {
		t.Fatalf("并发操作未互斥：%d %s", busy.Code, busy.Body.String())
	}
	close(decider.release)
	if res := <-first; res.Code != http.StatusOK {
		t.Fatalf("首次提问失败：%d %s", res.Code, res.Body.String())
	}
	if repeated := action("question", "first", "question_001"); repeated.Code != http.StatusOK || !strings.Contains(repeated.Body.String(), `"duplicate":true`) {
		t.Fatalf("同一操作未去重：%d %s", repeated.Code, repeated.Body.String())
	}
	if mismatch := action("question", "changed", "question_001"); mismatch.Code != http.StatusConflict {
		t.Fatalf("复用操作 ID 修改内容：%d %s", mismatch.Code, mismatch.Body.String())
	}
	if decider.calls.Load() != 1 {
		t.Fatal("重复操作再次调用了模型")
	}
	if failed := action("question", "fail", "question_003"); failed.Code != http.StatusServiceUnavailable {
		t.Fatalf("模型失败没有正确返回：%d %s", failed.Code, failed.Body.String())
	}
	var events, failedAttempts int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM ai_events WHERE game_id=$1`, gameID).Scan(&events); err != nil || events != 1 {
		t.Fatalf("模型失败产生了假事件：%d %v", events, err)
	}
	if err := db.QueryRow(ctx, `SELECT count(*) FROM ai_attempts WHERE game_id=$1 AND success=FALSE`, gameID).Scan(&failedAttempts); err != nil || failedAttempts != 1 {
		t.Fatalf("模型错误指标未记录：%d %v", failedAttempts, err)
	}
	if finished := action("restore", "完整还原", "restore_001"); finished.Code != http.StatusOK || !strings.Contains(finished.Body.String(), "测试汤底") {
		t.Fatalf("正确还原后未结束：%d %s", finished.Code, finished.Body.String())
	}
	view = call(http.MethodGet, gamePath, nil, cookies[1])
	if view.Code != http.StatusOK || !strings.Contains(view.Body.String(), `"status":"finished"`) || !strings.Contains(view.Body.String(), "测试汤底") {
		t.Fatalf("结束后无法查看汤底：%d %s", view.Code, view.Body.String())
	}
	if extra := action("question", "继续", "question_004"); extra.Code != http.StatusConflict {
		t.Fatalf("结束后仍可提问：%d %s", extra.Code, extra.Body.String())
	}
	var revealed bool
	if err := db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM soup_reveals WHERE soup_id=$1 AND user_id=$2)`, soupID, users[1]).Scan(&revealed); err != nil || !revealed {
		t.Fatalf("单人完成后未记录已知汤底：%t %v", revealed, err)
	}

	// 主动揭底立即结束，重发相同操作仍返回同一汤底。
	if err := db.QueryRow(ctx, `INSERT INTO soups(author_id,title,surface,answer,genre,difficulty,horror) VALUES($1,'第二题','第二题汤面','第二题汤底','本格',2,1) RETURNING id`, users[0]).Scan(&soupID2); err != nil {
		t.Fatal(err)
	}
	created = call(http.MethodPost, "/api/ai/solo", map[string]any{"soupId": soupID2}, cookies[1])
	if created.Code != http.StatusCreated {
		t.Fatalf("第二局创建失败：%d %s", created.Code, created.Body.String())
	}
	if err := json.Unmarshal(created.Body.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	gameID2 = output.ID
	actionPath = fmt.Sprintf("/api/ai/solo/%d/actions", gameID2)
	if result := action("reveal", "", "reveal_001"); result.Code != http.StatusOK || !strings.Contains(result.Body.String(), "第二题汤底") {
		t.Fatalf("主动揭底失败：%d %s", result.Code, result.Body.String())
	}
	if repeated := action("reveal", "", "reveal_001"); repeated.Code != http.StatusOK || !strings.Contains(repeated.Body.String(), `"duplicate":true`) || !strings.Contains(repeated.Body.String(), "第二题汤底") {
		t.Fatalf("揭底重试结果错误：%d %s", repeated.Code, repeated.Body.String())
	}

	// 每日模型额度由数据库统一计数，不受单人对局数量影响。
	if err := db.QueryRow(ctx, `INSERT INTO soups(author_id,title,surface,answer,genre,difficulty,horror) VALUES($1,'第三题','第三题汤面','第三题汤底','本格',2,1) RETURNING id`, users[0]).Scan(&soupID3); err != nil {
		t.Fatal(err)
	}
	created = call(http.MethodPost, "/api/ai/solo", map[string]any{"soupId": soupID3}, cookies[1])
	if created.Code != http.StatusCreated {
		t.Fatalf("第三局创建失败：%d %s", created.Code, created.Body.String())
	}
	if err := json.Unmarshal(created.Body.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	gameID3 = output.ID
	store := NewStore(db)
	if _, _, _, err := store.Claim(ctx, users[1], gameID3, "lease_check_1", "question", "lease"); err != nil {
		t.Fatal(err)
	}
	var leaseSeconds float64
	if err := db.QueryRow(ctx, `SELECT EXTRACT(EPOCH FROM busy_until-NOW()) FROM ai_games WHERE id=$1`, gameID3).Scan(&leaseSeconds); err != nil || leaseSeconds < 80 || leaseSeconds > 91 {
		t.Fatalf("租约期限错误：%.1f 秒，%v", leaseSeconds, err)
	}
	store.Release(ctx, users[1], gameID3, "lease_check_1")
	if _, err := db.Exec(ctx, `INSERT INTO ai_daily_usage(user_id,usage_day,calls) VALUES($1,CURRENT_DATE,119)
		ON CONFLICT (user_id,usage_day) DO UPDATE SET calls=119`, users[1]); err != nil {
		t.Fatal(err)
	}
	actionPath = fmt.Sprintf("/api/ai/solo/%d/actions", gameID3)
	if res := action("question", "quota one", "quota_call_1"); res.Code != http.StatusOK {
		t.Fatalf("第 120 次调用失败：%d %s", res.Code, res.Body.String())
	}
	modelCalls := decider.calls.Load()
	if denied := action("question", "quota two", "quota_call_2"); denied.Code != http.StatusTooManyRequests || decider.calls.Load() != modelCalls {
		t.Fatalf("超额调用仍进入模型：%d %s", denied.Code, denied.Body.String())
	}
	if res := action("reveal", "", "quota_reveal_1"); res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "第三题汤底") {
		t.Fatalf("额度满后无法揭底：%d %s", res.Code, res.Body.String())
	}

	// 本局达到 100 条事件后，仍可主动揭底结束，不会永久占用活跃局名额。
	if err := db.QueryRow(ctx, `INSERT INTO soups(author_id,title,surface,answer,genre,difficulty,horror) VALUES($1,'第四题','第四题汤面','第四题汤底','本格',2,1) RETURNING id`, users[0]).Scan(&soupID4); err != nil {
		t.Fatal(err)
	}
	created = call(http.MethodPost, "/api/ai/solo", map[string]any{"soupId": soupID4}, cookies[1])
	if created.Code != http.StatusCreated {
		t.Fatalf("第四局创建失败：%d %s", created.Code, created.Body.String())
	}
	if err := json.Unmarshal(created.Body.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	gameID4 = output.ID
	if err := db.QueryRow(ctx, `INSERT INTO soups(author_id,title,surface,answer,genre,difficulty,horror) VALUES($1,'第五题','第五题汤面','第五题汤底','本格',2,1) RETURNING id`, users[0]).Scan(&soupID5); err != nil {
		t.Fatal(err)
	}
	created = call(http.MethodPost, "/api/ai/solo", map[string]any{"soupId": soupID5}, cookies[1])
	if created.Code != http.StatusCreated {
		t.Fatalf("第五局创建失败：%d %s", created.Code, created.Body.String())
	}
	if err := json.Unmarshal(created.Body.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	gameID5 = output.ID
	if _, err := db.Exec(ctx, `UPDATE ai_daily_usage SET calls=119 WHERE user_id=$1 AND usage_day=CURRENT_DATE`, users[1]); err != nil {
		t.Fatal(err)
	}
	// 两局同时争取最后一次额度，数据库只能放行其中一次。
	type claimResult struct {
		id  int64
		err error
	}
	claims := make(chan claimResult, 2)
	for _, id := range []int64{gameID4, gameID5} {
		go func(id int64) {
			_, _, _, err := store.Claim(ctx, users[1], id, "parallel_quota_1", "question", "测试")
			claims <- claimResult{id: id, err: err}
		}(id)
	}
	var accepted, rejected int
	for i := 0; i < 2; i++ {
		result := <-claims
		if result.err == nil {
			accepted++
			store.Release(ctx, users[1], result.id, "parallel_quota_1")
		} else if errors.Is(result.err, ErrDailyLimit) {
			rejected++
		} else {
			t.Fatalf("并发额度返回错误：%v", result.err)
		}
	}
	if accepted != 1 || rejected != 1 {
		t.Fatalf("并发额度未原子控制：放行 %d，拒绝 %d", accepted, rejected)
	}
	for i := 0; i < 100; i++ {
		_, err := db.Exec(ctx, `INSERT INTO ai_events(game_id,operation_id,kind,body,result) VALUES($1,$2,'question','测试','是')`, gameID4, fmt.Sprintf("filled_%03d", i))
		if err != nil {
			t.Fatal(err)
		}
	}
	actionPath = fmt.Sprintf("/api/ai/solo/%d/actions", gameID4)
	if denied := action("question", "new", "event_limit_1"); denied.Code != http.StatusConflict {
		t.Fatalf("本局超额提问仍被接受：%d %s", denied.Code, denied.Body.String())
	}
	if res := action("reveal", "", "event_reveal_1"); res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "第四题汤底") {
		t.Fatalf("100 次后无法结束对局：%d %s", res.Code, res.Body.String())
	}
}
