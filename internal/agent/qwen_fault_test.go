package agent

import (
	"bytes"
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
			select {
			case <-r.Context().Done():
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
	model := &Qwen{Key: "fake-test-key", BaseURL: upstream.URL, Model: "mock", Client: client}
	router := gin.New()
	NewHandler(store, account.NewHandler(account.NewPGStore(db), false), model).Register(router)
	path := fmt.Sprintf("/api/ai/solo/%d/actions", game)
	call := func() (*httptest.ResponseRecorder, time.Duration) {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"type":"question","body":"他回家了吗？","operationId":"fault_timeout_01"}`))
		request.Header.Set("Content-Type", "application/json")
		request.AddCookie(&http.Cookie{Name: "turtle_session", Value: token})
		response := httptest.NewRecorder()
		start := time.Now()
		router.ServeHTTP(response, request)
		if strings.Contains(response.Body.String(), secret) || strings.Contains(response.Body.String(), "fake-test-key") {
			t.Fatal("故障或恢复响应泄漏了隐藏内容")
		}
		return response, time.Since(start)
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

	failed, failureTime := call()
	if failed.Code != http.StatusServiceUnavailable || upstreamCalls.Load() != 1 {
		t.Fatalf("应经历一次真实客户端超时并返回 503：status=%d calls=%d", failed.Code, upstreamCalls.Load())
	}
	check(0, 1, 1)
	var failedAttempts int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM ai_attempts WHERE game_id=$1 AND NOT success`, game).Scan(&failedAttempts); err != nil || failedAttempts != 1 {
		t.Fatalf("缺失上游失败记录：count=%d err=%v", failedAttempts, err)
	}
	recovered, recoveryTime := call()
	if recovered.Code != http.StatusOK || !strings.Contains(recovered.Body.String(), `"result":"是"`) {
		t.Fatalf("同操作 ID 未恢复：status=%d", recovered.Code)
	}
	check(1, 2, 2)
	replayed, replayTime := call()
	if replayed.Code != http.StatusOK || !strings.Contains(replayed.Body.String(), `"duplicate":true`) || upstreamCalls.Load() != 2 {
		t.Fatalf("已完成操作未正确重放：status=%d calls=%d", replayed.Code, upstreamCalls.Load())
	}
	check(1, 2, 2)
	t.Logf("model_fault_ms=%d model_recovery_ms=%d model_replay_ms=%d upstream_calls=%d", failureTime.Milliseconds(), recoveryTime.Milliseconds(), replayTime.Milliseconds(), upstreamCalls.Load())
}
