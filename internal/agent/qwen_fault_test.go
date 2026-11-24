package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"turtle-soup/internal/account"
)

func TestQwenTimeoutRecoveryWithPostgres(t *testing.T) {
	checkQwenTransportRecovery(t, false)
}

func TestQwenCallerCancellationWithPostgres(t *testing.T) {
	checkQwenTransportRecovery(t, true)
}

// 对照模型客户端超时与主动取消，两种失败都应释放操作并允许原 ID 重试。
func checkQwenTransportRecovery(t *testing.T, cancelByCaller bool) {
	t.Helper()
	ctx, db := newHistoryTestDB(t)
	var author, player, soupID int64
	if err := db.QueryRow(ctx, `INSERT INTO users(username,password_hash) VALUES('fault_author','test') RETURNING id`).Scan(&author); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `INSERT INTO users(username,password_hash) VALUES('fault_player','test') RETURNING id`).Scan(&player); err != nil {
		t.Fatal(err)
	}
	const secret = "超时测试的隐藏答案"
	if err := db.QueryRow(ctx, `INSERT INTO soups(author_id,title,surface,answer,genre,difficulty,horror)
		VALUES($1,'超时回归','一次回环测试',$2,'本格',2,0) RETURNING id`, author, secret).Scan(&soupID); err != nil {
		t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	hash := sha256.Sum256([]byte(token))
	if _, err := db.Exec(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,NOW()+INTERVAL '1 hour')`, hash[:], player); err != nil {
		t.Fatal(err)
	}
	store := NewStore(db)
	game, err := store.Create(ctx, player, soupID)
	if err != nil {
		t.Fatal(err)
	}

	// 第一次回环 HTTP 调用等待取消，下一次立即返回受约束的正常判定。
	var upstreamCalls atomic.Int32
	stop := make(chan struct{})
	started := make(chan struct{})
	upstreamCanceled := make(chan time.Time, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Error("Qwen 请求路径错误")
			http.NotFound(w, r)
			return
		}
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			return
		}
		if upstreamCalls.Add(1) == 1 {
			close(started)
			select {
			case <-r.Context().Done():
				upstreamCanceled <- time.Now()
			case <-stop:
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"mock","choices":[{"finish_reason":"stop","message":{"content":"{\"result\":\"是\"}"}}]}`)
	}))
	defer upstream.Close()
	defer close(stop)
	client := upstream.Client()
	client.Timeout = 250 * time.Millisecond
	if cancelByCaller {
		client.Timeout = 8 * time.Second
	}
	model := &Qwen{Key: "fake-test-key", BaseURL: upstream.URL, Model: "mock", Client: client}
	router := gin.New()
	NewHandler(store, account.NewHandler(account.NewPGStore(db), false), model).Register(router)
	path := fmt.Sprintf("/api/ai/solo/%d/actions", game)
	type result struct {
		response *httptest.ResponseRecorder
		elapsed  time.Duration
	}
	call := func(requestCtx context.Context) result {
		request := httptest.NewRequestWithContext(requestCtx, http.MethodPost, path, strings.NewReader(`{"type":"question","body":"他回家了吗？","operationId":"fault_timeout_01"}`))
		request.Header.Set("Content-Type", "application/json")
		request.AddCookie(&http.Cookie{Name: "turtle_session", Value: token})
		response := httptest.NewRecorder()
		start := time.Now()
		router.ServeHTTP(response, request)
		return result{response, time.Since(start)}
	}

	check := func(wantEvents, wantAttempts, wantCalls int) {
		t.Helper()
		var events, attempts, daily, global int
		var released bool
		err := db.QueryRow(ctx, `SELECT busy_op IS NULL AND busy_until IS NULL AND status='active',
			(SELECT count(*) FROM ai_events WHERE game_id=$1),
			(SELECT count(*) FROM ai_attempts WHERE game_id=$1),
			(SELECT calls FROM ai_daily_usage WHERE user_id=$2 AND usage_day=CURRENT_DATE),
			(SELECT calls FROM ai_global_usage WHERE usage_day=CURRENT_DATE)
			FROM ai_games WHERE id=$1`, game, player).Scan(&released, &events, &attempts, &daily, &global)
		if err != nil || !released || events != wantEvents || attempts != wantAttempts || daily != wantCalls || global != wantCalls {
			t.Fatalf("对局状态或额度错误：released=%v events=%d attempts=%d daily=%d global=%d err=%v", released, events, attempts, daily, global, err)
		}
	}

	var failed result
	if cancelByCaller {
		actionCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		done := make(chan result, 1)
		go func() { done <- call(actionCtx) }()
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("未看到首个模型请求进入上游")
		}
		canceledAt := time.Now()
		cancel()
		select {
		case upstreamAt := <-upstreamCanceled:
			t.Logf("调用方取消传到模型：%s", upstreamAt.Sub(canceledAt))
		case <-time.After(2 * time.Second):
			t.Fatal("调用方取消未及时传到正在等待的模型")
		}
		select {
		case failed = <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("取消后请求未及时完成")
		}
	} else {
		failed = call(ctx)
	}
	if failed.response.Code != http.StatusServiceUnavailable || upstreamCalls.Load() != 1 {
		t.Fatalf("模型传输失败应返回 503：status=%d calls=%d", failed.response.Code, upstreamCalls.Load())
	}
	check(0, 1, 1)
	var failedAttempts int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM ai_attempts WHERE game_id=$1 AND NOT success`, game).Scan(&failedAttempts); err != nil || failedAttempts != 1 {
		t.Fatalf("缺失上游失败记录：count=%d err=%v", failedAttempts, err)
	}
	recovered := call(ctx)
	if recovered.response.Code != http.StatusOK || !strings.Contains(recovered.response.Body.String(), `"result":"是"`) {
		t.Fatalf("同操作 ID 未恢复：status=%d", recovered.response.Code)
	}
	check(1, 2, 2)
	replayed := call(ctx)
	if replayed.response.Code != http.StatusOK || !strings.Contains(replayed.response.Body.String(), `"duplicate":true`) || upstreamCalls.Load() != 2 {
		t.Fatalf("已完成操作未正确重放：status=%d calls=%d", replayed.response.Code, upstreamCalls.Load())
	}
	check(1, 2, 2)
	for _, reply := range []result{failed, recovered, replayed} {
		if strings.Contains(reply.response.Body.String(), secret) || strings.Contains(reply.response.Body.String(), "fake-test-key") {
			t.Fatal("故障或恢复响应泄漏了隐藏内容")
		}
	}
	t.Logf("model_fault_ms=%d model_recovery_ms=%d model_replay_ms=%d upstream_calls=%d", failed.elapsed.Milliseconds(), recovered.elapsed.Milliseconds(), replayed.elapsed.Milliseconds(), upstreamCalls.Load())
}
