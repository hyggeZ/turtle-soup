package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"turtle-soup/internal/account"
	"turtle-soup/migrations"
)

// 模型暂不可用时，旧操作仍可重放；新操作不能占用额度或处理租约。
func TestOfflineReplayWithPostgres(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("需要 TEST_DATABASE_URL")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	schema := fmt.Sprintf("ai_replay_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })
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
	var author, player, soupID int64
	if err := db.QueryRow(ctx, `INSERT INTO users(username,password_hash) VALUES('author','test') RETURNING id`).Scan(&author); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `INSERT INTO users(username,password_hash) VALUES('player','test') RETURNING id`).Scan(&player); err != nil {
		t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	hash := sha256.Sum256([]byte(token))
	if _, err := db.Exec(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,NOW()+INTERVAL '1 hour')`, hash[:], player); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `INSERT INTO soups(author_id,title,surface,answer,genre,difficulty,horror)
		VALUES($1,'重放测试','汤面','汤底','本格',2,1) RETURNING id`, author).Scan(&soupID); err != nil {
		t.Fatal(err)
	}
	store := NewStore(db)
	gameID, err := store.Create(ctx, player, soupID)
	if err != nil {
		t.Fatal(err)
	}
	const firstOp = "offline_question_001"
	const firstBody = "他回家了吗"
	if _, _, _, err := store.Claim(ctx, player, gameID, firstOp, "question", firstBody); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Complete(ctx, player, gameID, firstOp, "question", firstBody,
		Decision{Result: "是", Model: "mock", LatencyMS: 12, PromptTokens: 40, CompletionTokens: 2}); err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	NewHandler(store, account.NewHandler(account.NewPGStore(db), false), &Qwen{}).Register(router)
	path := fmt.Sprintf("/api/ai/solo/%d/actions", gameID)
	call := func(op, body string) *httptest.ResponseRecorder {
		data, err := json.Marshal(actionInput{Type: "question", Body: body, OperationID: op})
		if err != nil {
			t.Errorf("编码测试请求：%v", err)
			return httptest.NewRecorder()
		}
		request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(data))
		request.Header.Set("Content-Type", "application/json")
		request.AddCookie(&http.Cookie{Name: "turtle_session", Value: token})
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}
	checkUsage := func(want int) {
		t.Helper()
		var global, daily int
		err := db.QueryRow(ctx, `SELECT COALESCE((SELECT calls FROM ai_global_usage WHERE usage_day=CURRENT_DATE),0)`).Scan(&global)
		if err != nil || global != want {
			t.Fatalf("全局模型额度变化：得到 %d，期望 %d，错误 %v", global, want, err)
		}
		err = db.QueryRow(ctx, `SELECT COALESCE((SELECT calls FROM ai_daily_usage WHERE user_id=$1 AND usage_day=CURRENT_DATE),0)`, player).Scan(&daily)
		if err != nil || daily != want {
			t.Fatalf("账号模型额度变化：得到 %d，期望 %d，错误 %v", daily, want, err)
		}
	}
	checkUsage(1)
	if response := call(firstOp, firstBody); response.Code != http.StatusOK ||
		!bytes.Contains(response.Body.Bytes(), []byte(`"duplicate":true`)) ||
		!bytes.Contains(response.Body.Bytes(), []byte(`"result":"是"`)) {
		t.Fatalf("无模型时未重放完成结果：%d %s", response.Code, response.Body.String())
	}
	if response := call(firstOp, "换了问题"); response.Code != http.StatusConflict {
		t.Fatalf("相同操作号的内容变化未拒绝：%d %s", response.Code, response.Body.String())
	}
	if response := call("offline_question_002", "新的问题"); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("模型不可用时仍接受新操作：%d %s", response.Code, response.Body.String())
	}
	checkUsage(1)
	var busy *string
	if err := db.QueryRow(ctx, `SELECT busy_op FROM ai_games WHERE id=$1`, gameID).Scan(&busy); err != nil || busy != nil {
		t.Fatalf("被拒新操作留下处理租约：%v %v", busy, err)
	}

	// 一个尚未完成的操作没有可重放结果；释放后仍不应扣第二次额度。
	const pendingOp = "offline_question_003"
	if _, _, _, err := store.Claim(ctx, player, gameID, pendingOp, "question", "处理中"); err != nil {
		t.Fatal(err)
	}
	if response := call(pendingOp, "处理中"); response.Code != http.StatusConflict {
		t.Fatalf("处理中的操作被错误重放：%d %s", response.Code, response.Body.String())
	}
	if err := store.Release(ctx, player, gameID, pendingOp); err != nil {
		t.Fatal(err)
	}
	if response := call(pendingOp, "处理中"); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("失败但未完成的操作被错误重放：%d %s", response.Code, response.Body.String())
	}
	checkUsage(2)

	// 多个重复请求只读已提交判定，不再调用模型或增加事件。
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if response := call(firstOp, firstBody); response.Code != http.StatusOK {
				t.Errorf("并发重放失败：%d %s", response.Code, response.Body.String())
			}
		}()
	}
	workers.Wait()
	checkUsage(2)
	var events, attempts int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM ai_events WHERE game_id=$1`, gameID).Scan(&events); err != nil || events != 1 {
		t.Fatalf("重复重放写入事件：%d %v", events, err)
	}
	if err := db.QueryRow(ctx, `SELECT count(*) FROM ai_attempts WHERE game_id=$1`, gameID).Scan(&attempts); err != nil || attempts != 0 {
		t.Fatalf("重复重放调用模型：%d %v", attempts, err)
	}
	// 模型恢复后，先前失败的同一操作号仍可正常完成一次。
	readyRouter := gin.New()
	NewHandler(store, account.NewHandler(account.NewPGStore(db), false), &testDecider{}).Register(readyRouter)
	data, err := json.Marshal(actionInput{Type: "question", Body: "处理中", OperationID: pendingOp})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(data))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: "turtle_session", Value: token})
	response := httptest.NewRecorder()
	readyRouter.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"result":"是"`)) {
		t.Fatalf("模型恢复后原操作号不能完成：%d %s", response.Code, response.Body.String())
	}
	checkUsage(3)
	if err := db.QueryRow(ctx, `SELECT count(*) FROM ai_events WHERE game_id=$1`, gameID).Scan(&events); err != nil || events != 2 {
		t.Fatalf("恢复后结果未写入一次：%d %v", events, err)
	}
}
