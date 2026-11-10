package room_test

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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"turtle-soup/internal/account"
	"turtle-soup/internal/forum"
	"turtle-soup/internal/room"
	"turtle-soup/migrations"
)

// 使用 TEST_DATABASE_URL 运行真实 PostgreSQL 验收，不依赖固定账号和题目。
func TestAnswerRevealAndRoomJoin(t *testing.T) {
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

	auth := account.NewHandler(account.NewPGStore(db), false)
	router := gin.New()
	forum.NewHandler(forum.NewStore(db), auth).Register(router)
	room.NewHandler(db, auth).Register(router)

	stamp := time.Now().UnixNano()
	users := make([]int64, 6)
	cookies := make([]*http.Cookie, 6)
	for i := range users {
		name := fmt.Sprintf("test_game_%d_%d", stamp, i)
		if err := db.QueryRow(ctx, `INSERT INTO users(username,password_hash) VALUES($1,'test') RETURNING id`, name).Scan(&users[i]); err != nil {
			t.Fatal(err)
		}
		token := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{byte(i + 1)}, 32))
		hash := sha256.Sum256([]byte(token))
		_, err := db.Exec(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,NOW()+INTERVAL '1 hour')`, hash[:], users[i])
		if err != nil {
			t.Fatal(err)
		}
		cookies[i] = &http.Cookie{Name: "turtle_session", Value: token}
	}
	var soupID, soupID2, soupID3, soupID4, roomID, roomID2, roomID3, roomID4, roomID5 int64
	t.Cleanup(func() {
		if roomID5 != 0 {
			_, _ = db.Exec(ctx, `DELETE FROM rooms WHERE id=$1`, roomID5)
		}
		if roomID4 != 0 {
			_, _ = db.Exec(ctx, `DELETE FROM rooms WHERE id=$1`, roomID4)
		}
		if roomID3 != 0 {
			_, _ = db.Exec(ctx, `DELETE FROM rooms WHERE id=$1`, roomID3)
		}
		if roomID2 != 0 {
			_, _ = db.Exec(ctx, `DELETE FROM rooms WHERE id=$1`, roomID2)
		}
		if roomID != 0 {
			_, _ = db.Exec(ctx, `DELETE FROM rooms WHERE id=$1`, roomID)
		}
		if soupID != 0 {
			_, _ = db.Exec(ctx, `DELETE FROM soups WHERE id=$1`, soupID)
		}
		if soupID2 != 0 {
			_, _ = db.Exec(ctx, `DELETE FROM soups WHERE id=$1`, soupID2)
		}
		if soupID3 != 0 {
			_, _ = db.Exec(ctx, `DELETE FROM soups WHERE id=$1`, soupID3)
		}
		if soupID4 != 0 {
			_, _ = db.Exec(ctx, `DELETE FROM soups WHERE id=$1`, soupID4)
		}
		for _, id := range users {
			if id != 0 {
				_, _ = db.Exec(ctx, `DELETE FROM users WHERE id=$1`, id)
			}
		}
	})
	if err := db.QueryRow(ctx, `INSERT INTO soups(author_id,title,surface,answer,genre,difficulty,horror) VALUES($1,'测试题','测试汤面','测试汤底','本格',2,1) RETURNING id`, users[0]).Scan(&soupID); err != nil {
		t.Fatal(err)
	}

	call := func(method, path string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		return res
	}

	created := call(http.MethodPost, "/api/rooms", map[string]any{"soupId": soupID, "mode": "casual", "maxPlayers": 2}, cookies[0])
	if created.Code != http.StatusCreated {
		t.Fatalf("创建房间失败：%d %s", created.Code, created.Body.String())
	}
	var roomInfo struct {
		ID   int64  `json:"id"`
		Code string `json:"code"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &roomInfo); err != nil {
		t.Fatal(err)
	}
	if len(roomInfo.Code) != 32 {
		t.Fatalf("新房间码应有 128 位随机性，实际长度为 %d", len(roomInfo.Code))
	}
	roomID = roomInfo.ID
	joinPath := "/api/rooms/join"
	joinBody := map[string]string{"code": roomInfo.Code}
	if res := call(http.MethodPost, joinPath, joinBody, cookies[1]); res.Code != http.StatusOK {
		t.Fatalf("玩家加入失败：%d %s", res.Code, res.Body.String())
	}
	if res := call(http.MethodPost, "/api/rooms", map[string]any{"soupId": soupID, "mode": "casual", "maxPlayers": 2}, cookies[1]); res.Code != http.StatusConflict {
		t.Fatalf("其他同题房间玩家仍可自任主持并拿汤底：%d %s", res.Code, res.Body.String())
	}
	answerPath := fmt.Sprintf("/api/soups/%d/answer", soupID)
	if res := call(http.MethodGet, answerPath, nil, cookies[2]); res.Code != http.StatusNotFound {
		t.Fatalf("GET 不应改变汤底查看记录：%d %s", res.Code, res.Body.String())
	}
	if res := call(http.MethodPost, answerPath, nil, cookies[1]); res.Code != http.StatusForbidden {
		t.Fatalf("等待中的玩家看到了汤底：%d %s", res.Code, res.Body.String())
	}
	if res := call(http.MethodPost, answerPath, nil, cookies[2]); res.Code != http.StatusOK {
		t.Fatalf("旁观者查看汤底失败：%d %s", res.Code, res.Body.String())
	}
	if res := call(http.MethodPost, joinPath, joinBody, cookies[2]); res.Code != http.StatusOK {
		t.Fatalf("已看汤底用户加入失败：%d %s", res.Code, res.Body.String())
	}
	path := fmt.Sprintf("/api/rooms/%d", roomID)
	player := call(http.MethodGet, path, nil, cookies[1])
	if player.Code != http.StatusOK || strings.Contains(player.Body.String(), "测试汤底") {
		t.Fatalf("玩家响应泄露汤底：%d %s", player.Code, player.Body.String())
	}
	spectator := call(http.MethodGet, path, nil, cookies[2])
	if spectator.Code != http.StatusOK || !strings.Contains(spectator.Body.String(), `"myRole":"spectator"`) || !strings.Contains(spectator.Body.String(), "测试汤底") {
		t.Fatalf("已看汤底用户身份错误：%d %s", spectator.Code, spectator.Body.String())
	}
	var group sync.WaitGroup
	results := make(chan int, 3)
	for _, cookie := range cookies[3:] {
		group.Add(1)
		go func(cookie *http.Cookie) {
			defer group.Done()
			results <- call(http.MethodPost, joinPath, joinBody, cookie).Code
		}(cookie)
	}
	group.Wait()
	close(results)
	for code := range results {
		if code != http.StatusOK {
			t.Fatalf("并发加入失败：%d", code)
		}
	}
	var playerCount int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM room_members WHERE room_id=$1 AND role='player'`, roomID).Scan(&playerCount); err != nil || playerCount != 2 {
		t.Fatalf("并发入场超过人数上限：count=%d err=%v", playerCount, err)
	}

	// 房主可以作为玩家建房，其他已知汤底的成员申请并接任主持。
	created = call(http.MethodPost, "/api/rooms", map[string]any{"soupId": soupID, "mode": "casual", "maxPlayers": 2, "hostSelf": false}, cookies[1])
	if created.Code != http.StatusCreated {
		t.Fatalf("玩家建房失败：%d %s", created.Code, created.Body.String())
	}
	if err := json.Unmarshal(created.Body.Bytes(), &roomInfo); err != nil {
		t.Fatal(err)
	}
	roomID2 = roomInfo.ID
	joinBody = map[string]string{"code": roomInfo.Code}
	if res := call(http.MethodPost, joinPath, joinBody, cookies[2]); res.Code != http.StatusOK {
		t.Fatalf("主持申请人加入失败：%d %s", res.Code, res.Body.String())
	}
	actionPath := fmt.Sprintf("/api/rooms/%d/actions", roomID2)
	action := func(cookie *http.Cookie, kind, key string, fields map[string]any) *httptest.ResponseRecorder {
		body := map[string]any{"type": kind, "operationId": key}
		for name, value := range fields {
			body[name] = value
		}
		return call(http.MethodPost, actionPath, body, cookie)
	}
	var otherPlayerID int64
	if err := db.QueryRow(ctx, `SELECT user_id FROM room_members WHERE room_id=$1 AND role='player' AND user_id<>$2 LIMIT 1`, roomID, users[1]).Scan(&otherPlayerID); err != nil {
		t.Fatal(err)
	}
	var otherCookie *http.Cookie
	for i, id := range users {
		if id == otherPlayerID {
			otherCookie = cookies[i]
		}
	}
	if otherCookie == nil {
		t.Fatal("没有找到另一名玩家的会话")
	}
	if res := call(http.MethodPost, joinPath, joinBody, otherCookie); res.Code != http.StatusOK {
		t.Fatalf("其他同题玩家加入第二房间失败：%d %s", res.Code, res.Body.String())
	}
	if res := action(otherCookie, "host_request", "cross_host_req", nil); res.Code != http.StatusOK {
		t.Fatalf("其他同题玩家申请主持失败：%d %s", res.Code, res.Body.String())
	}
	if res := action(cookies[1], "host_approve", "cross_host_app", map[string]any{"userId": otherPlayerID}); res.Code != http.StatusConflict {
		t.Fatalf("其他房间的同题玩家接任主持：%d %s", res.Code, res.Body.String())
	}
	if _, err := db.Exec(ctx, `UPDATE room_members SET role='spectator' WHERE room_id=$1 AND user_id=$2`, roomID2, otherPlayerID); err != nil {
		t.Fatal(err)
	}
	if res := action(cookies[2], "host_request", "host_req_1", nil); res.Code != http.StatusOK {
		t.Fatalf("申请主持失败：%d %s", res.Code, res.Body.String())
	}
	if res := action(cookies[1], "host_approve", "host_app_1", map[string]any{"userId": users[2]}); res.Code != http.StatusOK {
		t.Fatalf("确认主持失败：%d %s", res.Code, res.Body.String())
	}
	settingsPath := fmt.Sprintf("/api/rooms/%d/settings", roomID2)
	if err := db.QueryRow(ctx, `INSERT INTO soups(author_id,title,surface,answer,genre,difficulty,horror) VALUES($1,'房主题目','另一道汤面','另一道汤底','变格',3,2) RETURNING id`, users[1]).Scan(&soupID2); err != nil {
		t.Fatal(err)
	}
	badSettings := map[string]any{"soupId": soupID2, "mode": "casual", "maxPlayers": 3, "public": true}
	if res := call(http.MethodPatch, settingsPath, badSettings, cookies[1]); res.Code != http.StatusConflict {
		t.Fatalf("玩家换成自己的题目未被拒绝：%d %s", res.Code, res.Body.String())
	}
	settings := map[string]any{"soupId": soupID, "mode": "casual", "maxPlayers": 3, "public": true}
	if res := call(http.MethodPatch, settingsPath, settings, cookies[1]); res.Code != http.StatusNoContent {
		t.Fatalf("等待期修改设置失败：%d %s", res.Code, res.Body.String())
	}
	if res := action(cookies[1], "start", "start_001", nil); res.Code != http.StatusOK {
		t.Fatalf("开局失败：%d %s", res.Code, res.Body.String())
	}
	if res := action(cookies[1], "answer_request", "cross_reveal_req", nil); res.Code != http.StatusOK {
		t.Fatalf("玩家申请看底失败：%d %s", res.Code, res.Body.String())
	}
	hostBefore := call(http.MethodGet, fmt.Sprintf("/api/rooms/%d", roomID2), nil, cookies[2])
	var requests struct {
		Events []struct {
			ID   int64  `json:"id"`
			Kind string `json:"kind"`
		} `json:"events"`
	}
	if err := json.Unmarshal(hostBefore.Body.Bytes(), &requests); err != nil {
		t.Fatal(err)
	}
	var requestID int64
	for _, event := range requests.Events {
		if event.Kind == "answer_request" {
			requestID = event.ID
		}
	}
	if requestID == 0 {
		t.Fatal("主持人未看到查看汤底申请")
	}
	if res := action(cookies[2], "answer_decide", "cross_reveal_approve", map[string]any{"eventId": requestID, "result": "同意"}); res.Code != http.StatusConflict {
		t.Fatalf("仍在其他同题房间的玩家获准看底：%d %s", res.Code, res.Body.String())
	}
	if res := call(http.MethodPatch, settingsPath, settings, cookies[1]); res.Code != http.StatusForbidden {
		t.Fatalf("开局后仍可改设置：%d %s", res.Code, res.Body.String())
	}
	if res := action(cookies[1], "start", "start_001", nil); res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"duplicate":true`) {
		t.Fatalf("重复操作未被去重：%d %s", res.Code, res.Body.String())
	}
	if res := action(cookies[1], "retire", "start_001", nil); res.Code != http.StatusConflict {
		t.Fatalf("不同操作复用同一 ID 未被拒绝：%d %s", res.Code, res.Body.String())
	}
	path = fmt.Sprintf("/api/rooms/%d", roomID2)
	player = call(http.MethodGet, path, nil, cookies[1])
	if player.Code != http.StatusOK || strings.Contains(player.Body.String(), "测试汤底") {
		t.Fatalf("房主作为玩家看到了汤底：%d %s", player.Code, player.Body.String())
	}
	host := call(http.MethodGet, path, nil, cookies[2])
	if host.Code != http.StatusOK || !strings.Contains(host.Body.String(), `"myRole":"host"`) || !strings.Contains(host.Body.String(), "测试汤底") {
		t.Fatalf("主持人无法查看汤底：%d %s", host.Code, host.Body.String())
	}

	// 实时连接只发送变化提示；主持人离线后暂停，重连后恢复。
	server := httptest.NewServer(router)
	defer server.Close()
	socketURL := "ws" + strings.TrimPrefix(server.URL, "http") + path + "/ws"
	dial := func(cookie *http.Cookie) *websocket.Conn {
		t.Helper()
		header := http.Header{}
		header.Set("Cookie", cookie.String())
		header.Set("Origin", server.URL)
		conn, _, err := websocket.Dial(ctx, socketURL, &websocket.DialOptions{HTTPHeader: header})
		if err != nil {
			t.Fatal(err)
		}
		return conn
	}
	conn := dial(cookies[2])
	conn2 := dial(cookies[2])
	defer conn2.CloseNow()
	conn3 := dial(cookies[2])
	defer conn3.CloseNow()
	header := http.Header{}
	header.Set("Cookie", cookies[2].String())
	header.Set("Origin", server.URL)
	extra, response, dialErr := websocket.Dial(ctx, socketURL, &websocket.DialOptions{HTTPHeader: header})
	if extra != nil {
		extra.CloseNow()
	}
	if response != nil && response.Body != nil {
		response.Body.Close()
	}
	if dialErr == nil || response == nil || response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("第 4 个实时连接未被限制：status=%v err=%v", response, dialErr)
	}
	if res := action(cookies[1], "question", "question_001", map[string]any{"body": "测试问题"}); res.Code != http.StatusOK {
		t.Fatalf("提问失败：%d %s", res.Code, res.Body.String())
	}
	readCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	_, notice, err := conn.Read(readCtx)
	cancel()
	if err != nil || string(notice) != `{"type":"changed"}` {
		t.Fatalf("未收到房间变化提示：%s %v", notice, err)
	}
	conn.CloseNow()
	conn2.CloseNow()
	conn3.CloseNow()
	waitStatus := func(want string) {
		t.Helper()
		for i := 0; i < 40; i++ {
			var status string
			if err := db.QueryRow(ctx, `SELECT status FROM rooms WHERE id=$1`, roomID2).Scan(&status); err == nil && status == want {
				return
			}
			time.Sleep(25 * time.Millisecond)
		}
		t.Fatalf("房间未进入 %s 状态", want)
	}
	waitStatus("paused")
	if res := action(cookies[1], "question", "question_002", map[string]any{"body": "暂停期间的问题"}); res.Code != http.StatusConflict {
		t.Fatalf("暂停期间仍可提问：%d %s", res.Code, res.Body.String())
	}
	conn = dial(cookies[2])
	defer conn.CloseNow()
	waitStatus("active")
	if res := call(http.MethodPost, joinPath, joinBody, cookies[0]); res.Code != http.StatusOK {
		t.Fatalf("作者旁观失败：%d %s", res.Code, res.Body.String())
	}
	observer := call(http.MethodGet, path, nil, cookies[0])
	if observer.Code != http.StatusOK || strings.Contains(observer.Body.String(), "测试问题") {
		t.Fatalf("未回答的问题泄露给旁观者：%d %s", observer.Code, observer.Body.String())
	}
	host = call(http.MethodGet, path, nil, cookies[2])
	var questionData struct {
		Events []struct {
			ID   int64  `json:"id"`
			Kind string `json:"kind"`
		} `json:"events"`
	}
	if err := json.Unmarshal(host.Body.Bytes(), &questionData); err != nil {
		t.Fatal(err)
	}
	var questionID int64
	for _, event := range questionData.Events {
		if event.Kind == "question" {
			questionID = event.ID
		}
	}
	if questionID == 0 {
		t.Fatal("主持人未看到问题")
	}
	if res := action(cookies[2], "answer", "answer_001", map[string]any{"eventId": questionID, "result": "是"}); res.Code != http.StatusOK {
		t.Fatalf("主持回答失败：%d %s", res.Code, res.Body.String())
	}
	observer = call(http.MethodGet, path, nil, cookies[0])
	if observer.Code != http.StatusOK || !strings.Contains(observer.Body.String(), "测试问题") {
		t.Fatalf("已回答的问题没有公开：%d %s", observer.Code, observer.Body.String())
	}
	if res := action(cookies[1], "restore", "restore_001", map[string]any{"body": "私密还原内容"}); res.Code != http.StatusOK {
		t.Fatalf("提交还原失败：%d %s", res.Code, res.Body.String())
	}
	observer = call(http.MethodGet, path, nil, cookies[0])
	if observer.Code != http.StatusOK || strings.Contains(observer.Body.String(), `"kind":"restore"`) || strings.Contains(observer.Body.String(), "私密还原内容") {
		t.Fatalf("其他玩家提前看到了私人还原：%d %s", observer.Code, observer.Body.String())
	}
	host = call(http.MethodGet, path, nil, cookies[2])
	var game struct {
		Events []struct {
			ID   int64  `json:"id"`
			Kind string `json:"kind"`
		} `json:"events"`
	}
	if err := json.Unmarshal(host.Body.Bytes(), &game); err != nil {
		t.Fatal(err)
	}
	var restoreID int64
	for _, event := range game.Events {
		if event.Kind == "restore" {
			restoreID = event.ID
		}
	}
	if restoreID == 0 {
		t.Fatal("主持人未看到还原")
	}
	if res := action(cookies[2], "judge", "judge_001", map[string]any{"eventId": restoreID, "result": "正确"}); res.Code != http.StatusOK {
		t.Fatalf("判定还原失败：%d %s", res.Code, res.Body.String())
	}
	waitStatus("finished")
	player = call(http.MethodGet, path, nil, cookies[1])
	if player.Code != http.StatusOK || strings.Contains(player.Body.String(), "测试汤底") {
		t.Fatalf("汤底公开前泄露给玩家：%d %s", player.Code, player.Body.String())
	}
	if res := action(cookies[2], "publish_answer", "publish_blocked_001", nil); res.Code != http.StatusConflict {
		t.Fatalf("跨房间仍有玩家时公开了汤底：%d %s", res.Code, res.Body.String())
	}
	if _, err := db.Exec(ctx, `UPDATE room_members SET role='spectator' WHERE room_id=$1 AND user_id IN ($2,$3)`, roomID, users[1], otherPlayerID); err != nil {
		t.Fatal(err)
	}
	if res := action(cookies[2], "publish_answer", "publish_001", nil); res.Code != http.StatusOK {
		t.Fatalf("主持公开汤底失败：%d %s", res.Code, res.Body.String())
	}
	player = call(http.MethodGet, path, nil, cookies[1])
	if player.Code != http.StatusOK || !strings.Contains(player.Body.String(), "测试汤底") {
		t.Fatalf("汤底公开后玩家未看到：%d %s", player.Code, player.Body.String())
	}
	var revealed bool
	if err := db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM soup_reveals WHERE soup_id=$1 AND user_id=$2)`, soupID, users[1]).Scan(&revealed); err != nil || !revealed {
		t.Fatalf("公开汤底后未记录玩家已看底：%v", err)
	}
	observer = call(http.MethodGet, path, nil, cookies[0])
	if observer.Code != http.StatusOK || strings.Contains(observer.Body.String(), `"kind":"restore"`) || strings.Contains(observer.Body.String(), "私密还原内容") || !strings.Contains(observer.Body.String(), fmt.Sprintf(`"subjectUserId":%d`, users[1])) {
		t.Fatalf("旁观者看到了私人还原或缺少公开判定：%d %s", observer.Code, observer.Body.String())
	}

	// 竞答按入场顺序轮流提问，第一位还原正确的玩家获胜。
	created = call(http.MethodPost, "/api/rooms", map[string]any{"soupId": soupID2, "mode": "competitive", "maxPlayers": 2}, cookies[1])
	if created.Code != http.StatusCreated {
		t.Fatalf("创建竞答房间失败：%d %s", created.Code, created.Body.String())
	}
	if err := json.Unmarshal(created.Body.Bytes(), &roomInfo); err != nil {
		t.Fatal(err)
	}
	roomID3 = roomInfo.ID
	joinBody = map[string]string{"code": roomInfo.Code}
	for _, cookie := range []*http.Cookie{cookies[0], cookies[3]} {
		if res := call(http.MethodPost, joinPath, joinBody, cookie); res.Code != http.StatusOK {
			t.Fatalf("竞答玩家加入失败：%d %s", res.Code, res.Body.String())
		}
	}
	actionPath = fmt.Sprintf("/api/rooms/%d/actions", roomID3)
	if res := action(cookies[1], "start", "comp_start_001", nil); res.Code != http.StatusOK {
		t.Fatalf("竞答开局失败：%d %s", res.Code, res.Body.String())
	}
	path = fmt.Sprintf("/api/rooms/%d", roomID3)
	var turn struct {
		TurnUserID int64 `json:"turnUserId"`
	}
	if err := json.Unmarshal(call(http.MethodGet, path, nil, cookies[1]).Body.Bytes(), &turn); err != nil || turn.TurnUserID != users[0] {
		t.Fatalf("首轮玩家错误：id=%d err=%v", turn.TurnUserID, err)
	}
	if res := action(cookies[3], "question", "comp_wrong_001", map[string]any{"body": "抢先提问"}); res.Code != http.StatusConflict {
		t.Fatalf("非当前玩家抢答未被拒绝：%d %s", res.Code, res.Body.String())
	}
	if res := action(cookies[0], "question", "comp_ask_001", map[string]any{"body": "本轮问题"}); res.Code != http.StatusOK {
		t.Fatalf("当前玩家提问失败：%d %s", res.Code, res.Body.String())
	}
	host = call(http.MethodGet, path, nil, cookies[1])
	if err := json.Unmarshal(host.Body.Bytes(), &questionData); err != nil {
		t.Fatal(err)
	}
	questionID = 0
	for _, event := range questionData.Events {
		if event.Kind == "question" {
			questionID = event.ID
		}
	}
	if res := action(cookies[1], "answer", "comp_answer_001", map[string]any{"eventId": questionID, "result": "无关"}); res.Code != http.StatusOK {
		t.Fatalf("竞答主持回答失败：%d %s", res.Code, res.Body.String())
	}
	if err := json.Unmarshal(call(http.MethodGet, path, nil, cookies[1]).Body.Bytes(), &turn); err != nil || turn.TurnUserID != users[3] {
		t.Fatalf("下一轮玩家错误：id=%d err=%v", turn.TurnUserID, err)
	}
	if res := action(cookies[3], "restore", "comp_restore_001", map[string]any{"body": "竞答还原"}); res.Code != http.StatusOK {
		t.Fatalf("竞答还原失败：%d %s", res.Code, res.Body.String())
	}
	host = call(http.MethodGet, path, nil, cookies[1])
	if err := json.Unmarshal(host.Body.Bytes(), &game); err != nil {
		t.Fatal(err)
	}
	restoreID = 0
	for _, event := range game.Events {
		if event.Kind == "restore" {
			restoreID = event.ID
		}
	}
	if res := action(cookies[1], "judge", "comp_judge_001", map[string]any{"eventId": restoreID, "result": "正确"}); res.Code != http.StatusOK {
		t.Fatalf("竞答判定失败：%d %s", res.Code, res.Body.String())
	}
	var winner struct {
		Status   string `json:"status"`
		WinnerID int64  `json:"winnerId"`
	}
	if err := json.Unmarshal(call(http.MethodGet, path, nil, cookies[1]).Body.Bytes(), &winner); err != nil || winner.Status != "finished" || winner.WinnerID != users[3] {
		t.Fatalf("竞答胜负错误：status=%s winner=%d err=%v", winner.Status, winner.WinnerID, err)
	}

	// 三名选手时，当前玩家提问后退席，主持回答应轮到紧随其后的选手。
	if err := db.QueryRow(ctx, `INSERT INTO soups(author_id,title,surface,answer,genre,difficulty,horror) VALUES($1,'轮次测试','新的测试汤面','新的测试汤底','本格',2,1) RETURNING id`, users[4]).Scan(&soupID3); err != nil {
		t.Fatal(err)
	}
	created = call(http.MethodPost, "/api/rooms", map[string]any{"soupId": soupID3, "mode": "competitive", "maxPlayers": 3}, cookies[4])
	if created.Code != http.StatusCreated {
		t.Fatalf("创建三人竞答失败：%d %s", created.Code, created.Body.String())
	}
	if err := json.Unmarshal(created.Body.Bytes(), &roomInfo); err != nil {
		t.Fatal(err)
	}
	roomID4 = roomInfo.ID
	joinBody = map[string]string{"code": roomInfo.Code}
	for _, cookie := range []*http.Cookie{cookies[0], cookies[1], cookies[3]} {
		if res := call(http.MethodPost, joinPath, joinBody, cookie); res.Code != http.StatusOK {
			t.Fatalf("三人竞答入场失败：%d %s", res.Code, res.Body.String())
		}
	}
	if err := db.QueryRow(ctx, `INSERT INTO soups(author_id,title,surface,answer,genre,difficulty,horror) VALUES($1,'换题测试','另一新汤面','另一新汤底','本格',2,1) RETURNING id`, users[5]).Scan(&soupID4); err != nil {
		t.Fatal(err)
	}
	created = call(http.MethodPost, "/api/rooms", map[string]any{"soupId": soupID4, "mode": "casual", "maxPlayers": 2}, cookies[1])
	if created.Code != http.StatusCreated {
		t.Fatalf("创建换题房间失败：%d %s", created.Code, created.Body.String())
	}
	if err := json.Unmarshal(created.Body.Bytes(), &roomInfo); err != nil {
		t.Fatal(err)
	}
	roomID5 = roomInfo.ID
	settingsPath = fmt.Sprintf("/api/rooms/%d/settings", roomID5)
	settings = map[string]any{"soupId": soupID3, "mode": "casual", "maxPlayers": 2, "public": true}
	if res := call(http.MethodPatch, settingsPath, settings, cookies[1]); res.Code != http.StatusConflict {
		t.Fatalf("房主仍在其他房间推理时换题取得汤底：%d %s", res.Code, res.Body.String())
	}
	actionPath = fmt.Sprintf("/api/rooms/%d/actions", roomID4)
	if res := action(cookies[4], "start", "turn_start_001", nil); res.Code != http.StatusOK {
		t.Fatalf("三人竞答开局失败：%d %s", res.Code, res.Body.String())
	}
	path = fmt.Sprintf("/api/rooms/%d", roomID4)
	if res := action(cookies[0], "answer_request", "turn_req_001", nil); res.Code != http.StatusOK {
		t.Fatalf("首次看底申请失败：%d %s", res.Code, res.Body.String())
	}
	if res := action(cookies[0], "answer_request", "turn_req_002", nil); res.Code != http.StatusConflict {
		t.Fatalf("重复待处理看底申请未拒绝：%d %s", res.Code, res.Body.String())
	}
	if res := action(cookies[0], "restore", "turn_restore_001", map[string]any{"body": "第一次还原"}); res.Code != http.StatusOK {
		t.Fatalf("首次还原失败：%d %s", res.Code, res.Body.String())
	}
	if res := action(cookies[0], "restore", "turn_restore_002", map[string]any{"body": "重复还原"}); res.Code != http.StatusConflict {
		t.Fatalf("重复待判定还原未拒绝：%d %s", res.Code, res.Body.String())
	}
	if res := action(cookies[0], "question", "turn_question_001", map[string]any{"body": "离席前的问题"}); res.Code != http.StatusOK {
		t.Fatalf("提问失败：%d %s", res.Code, res.Body.String())
	}
	host = call(http.MethodGet, path, nil, cookies[4])
	if err := json.Unmarshal(host.Body.Bytes(), &questionData); err != nil {
		t.Fatal(err)
	}
	questionID = 0
	for _, event := range questionData.Events {
		if event.Kind == "question" {
			questionID = event.ID
		}
	}
	if questionID == 0 {
		t.Fatal("主持人未看到离席前的问题")
	}
	if res := action(cookies[0], "retire", "turn_retire_001", nil); res.Code != http.StatusOK {
		t.Fatalf("提问后退席失败：%d %s", res.Code, res.Body.String())
	}
	if err := json.Unmarshal(call(http.MethodGet, path, nil, cookies[4]).Body.Bytes(), &turn); err != nil || turn.TurnUserID != users[0] {
		t.Fatalf("主持未答时提前推进回合：id=%d err=%v", turn.TurnUserID, err)
	}
	if res := action(cookies[4], "answer", "turn_answer_001", map[string]any{"eventId": questionID, "result": "否"}); res.Code != http.StatusOK {
		t.Fatalf("退席后回答失败：%d %s", res.Code, res.Body.String())
	}
	if err := json.Unmarshal(call(http.MethodGet, path, nil, cookies[4]).Body.Bytes(), &turn); err != nil || turn.TurnUserID != users[1] {
		t.Fatalf("退席后跳过了下一位玩家：id=%d err=%v", turn.TurnUserID, err)
	}

	// 多次猜错房间码后，已在房间中的成员仍可以凭原码重连。
	blocked := false
	wrongCode := map[string]string{"code": "FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF"}
	for i := 0; i <= 40; i++ {
		res := call(http.MethodPost, joinPath, wrongCode, cookies[1])
		if res.Code == http.StatusTooManyRequests {
			blocked = true
			break
		}
		if res.Code != http.StatusNotFound {
			t.Fatalf("错误房间码返回 %d：%s", res.Code, res.Body.String())
		}
	}
	if !blocked {
		t.Fatal("错误房间码未触发试码限制")
	}
	if res := call(http.MethodPost, joinPath, map[string]string{"code": roomInfo.Code}, cookies[1]); res.Code != http.StatusOK {
		t.Fatalf("试码限额耗尽后，成员无法重连：%d %s", res.Code, res.Body.String())
	}
}
