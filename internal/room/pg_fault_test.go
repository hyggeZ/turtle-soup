package room_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"turtle-soup/internal/account"
	"turtle-soup/internal/room"
)

// 在测试连接池内切断 PostgreSQL，验证房间接口失败关闭和恢复后的幂等写入。
func TestRoomPostgresConnectionRecovery(t *testing.T) {
	f := newReplayFixture(t)
	host := f.addUser("pg_fault_host")
	player := f.addUser("pg_fault_player")
	f.addSoup(host)
	id := f.addRoom("23700000000000000000000000000000", "casual", "active", false, false, host)
	if _, err := f.db.Exec(f.ctx, `INSERT INTO room_members(room_id,user_id,role) VALUES($1,$2,'player')`, id, f.users[player]); err != nil {
		t.Fatal(err)
	}

	// 保留夹具的造数与断言连接。只对承载接口的连接池注入拨号故障。
	config := f.db.Config()
	dial := config.ConnConfig.DialFunc
	var offline atomic.Bool
	var blockedDials atomic.Int64
	config.ConnConfig.DialFunc = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if offline.Load() {
			blockedDials.Add(1)
			return nil, errors.New("测试连接暂不可用")
		}
		return dial(ctx, network, addr)
	}
	app, err := pgxpool.NewWithConfig(f.ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	f.router = gin.New()
	room.NewHandler(app, account.NewHandler(account.NewPGStore(app), false)).Register(f.router)

	const answer = "回看绝密汤底"
	path := fmt.Sprintf("/api/rooms/%d", id)
	start := time.Now()
	baseline := f.call(http.MethodGet, path, nil, player)
	baselineTime := time.Since(start)
	if baseline.Code != http.StatusOK || strings.Contains(baseline.Body.String(), answer) {
		t.Fatalf("故障前玩家快照异常：%d %s", baseline.Code, baseline.Body.String())
	}
	if res := f.call(http.MethodGet, path, nil, host); res.Code != http.StatusOK || !strings.Contains(res.Body.String(), answer) {
		t.Fatalf("主持未读到夹具汤底：%d %s", res.Code, res.Body.String())
	}

	const operation = "pg_fault_question_237"
	const question = "这和天气有关吗？"
	body := map[string]string{"type": "question", "operationId": operation, "body": question}
	assertCounts := func(want int) {
		t.Helper()
		var events, operations int
		err := f.db.QueryRow(f.ctx, `SELECT
			(SELECT count(*) FROM room_events WHERE room_id=$1 AND user_id=$2 AND kind='question' AND body=$3),
			(SELECT count(*) FROM game_operations WHERE room_id=$1 AND user_id=$2 AND operation_id=$4)`,
			id, f.users[player], question, operation).Scan(&events, &operations)
		if err != nil {
			t.Fatal(err)
		}
		if events != want || operations != want {
			t.Fatalf("房间事件或操作记录不一致：事件=%d 操作=%d 期望各=%d", events, operations, want)
		}
	}

	offline.Store(true)
	app.Reset() // 清除热连接，后续请求必须经过拨号闸门。
	start = time.Now()
	read := f.call(http.MethodGet, path, nil, player)
	readFailTime := time.Since(start)
	start = time.Now()
	write := f.call(http.MethodPost, path+"/actions", body, player)
	writeFailTime := time.Since(start)
	for _, res := range []struct {
		code int
		body string
	}{{read.Code, read.Body.String()}, {write.Code, write.Body.String()}} {
		if res.code != http.StatusInternalServerError || strings.Contains(res.body, answer) || !strings.Contains(res.body, "读取登录状态失败") {
			t.Fatalf("故障期间未安全拒绝请求：%d %s", res.code, res.body)
		}
	}
	if blockedDials.Load() == 0 {
		t.Fatal("接口未经过被阻断的 PostgreSQL 拨号路径")
	}
	assertCounts(0)

	offline.Store(false)
	start = time.Now()
	retry := f.call(http.MethodPost, path+"/actions", body, player)
	retryTime := time.Since(start)
	start = time.Now()
	replay := f.call(http.MethodPost, path+"/actions", body, player)
	replayTime := time.Since(start)
	for _, attempt := range []struct {
		code      int
		body      []byte
		duplicate bool
	}{{retry.Code, retry.Body.Bytes(), false}, {replay.Code, replay.Body.Bytes(), true}} {
		var result struct {
			OK        bool `json:"ok"`
			Duplicate bool `json:"duplicate"`
		}
		err := json.Unmarshal(attempt.body, &result)
		if attempt.code != http.StatusOK || err != nil || !result.OK || result.Duplicate != attempt.duplicate {
			t.Fatalf("恢复后操作结果异常：%d %s", attempt.code, attempt.body)
		}
	}
	assertCounts(1)
	start = time.Now()
	read = f.call(http.MethodGet, path, nil, player)
	readRecoveryTime := time.Since(start)
	if read.Code != http.StatusOK || strings.Contains(read.Body.String(), answer) || !strings.Contains(read.Body.String(), question) {
		t.Fatalf("恢复后的玩家快照异常：%d %s", read.Code, read.Body.String())
	}
	t.Logf("PG 故障验证 基线读=%s 故障读=%d/%s 故障写=%d/%s 恢复写=%d/%s 重放=%d/%s 恢复读=%d/%s 拒绝拨号=%d 提问事件=1 操作记录=1",
		baselineTime, http.StatusInternalServerError, readFailTime, write.Code, writeFailTime, retry.Code, retryTime,
		replay.Code, replayTime, read.Code, readRecoveryTime, blockedDials.Load())
}
