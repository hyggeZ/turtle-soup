package room

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"turtle-soup/internal/account"
	"turtle-soup/migrations"
)

// 多客户端同时刷新只推进一个回合；未回答提问、主持暂停和私人还原遵守独立规则。
func TestCompetitiveTurnAdvanceAndRestore(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("需要 TEST_DATABASE_URL")
	}
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	db, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := migrations.Apply(ctx, db); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().UnixNano()
	var users [3]int64
	for i := range users {
		name := fmt.Sprintf("turn_%d_%d", stamp, i)
		if err := db.QueryRow(ctx, `INSERT INTO users(username,password_hash) VALUES($1,'test') RETURNING id`, name).Scan(&users[i]); err != nil {
			t.Fatal(err)
		}
	}
	var soupID, roomID int64
	t.Cleanup(func() {
		clean := context.Background()
		if roomID != 0 {
			_, _ = db.Exec(clean, `DELETE FROM rooms WHERE id=$1`, roomID)
		}
		if soupID != 0 {
			_, _ = db.Exec(clean, `DELETE FROM soups WHERE id=$1`, soupID)
		}
		for _, id := range users {
			_, _ = db.Exec(clean, `DELETE FROM users WHERE id=$1`, id)
		}
	})
	if err := db.QueryRow(ctx, `INSERT INTO soups(author_id,title,surface,answer,genre,difficulty,horror)
		VALUES($1,'轮次测试','一件奇怪的事','测试汤底内容','本格',2,0) RETURNING id`, users[0]).Scan(&soupID); err != nil {
		t.Fatal(err)
	}
	code := fmt.Sprintf("T%d", stamp)
	if err := db.QueryRow(ctx, `INSERT INTO rooms(code,owner_id,host_id,soup_id,mode,max_players,status,turn_user_id,turn_deadline,
		soup_title,soup_surface,soup_answer,soup_genre,soup_difficulty,soup_horror)
		VALUES($1,$2,$2,$3,'competitive',2,'active',$4,NOW()-INTERVAL '1 second',
		'轮次测试','一件奇怪的事','测试汤底内容','本格',2,0) RETURNING id`, code, users[0], soupID, users[1]).Scan(&roomID); err != nil {
		t.Fatal(err)
	}
	for i, role := range []string{"host", "player", "player"} {
		if _, err := db.Exec(ctx, `INSERT INTO room_members(room_id,user_id,role) VALUES($1,$2,$3)`, roomID, users[i], role); err != nil {
			t.Fatal(err)
		}
	}
	createSession := func(user int64) *http.Cookie {
		t.Helper()
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			t.Fatal(err)
		}
		token := base64.RawURLEncoding.EncodeToString(raw)
		hash := sha256.Sum256([]byte(token))
		if _, err := db.Exec(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,NOW()+INTERVAL '1 hour')`, hash[:], user); err != nil {
			t.Fatal(err)
		}
		return &http.Cookie{Name: "turtle_session", Value: token}
	}
	hostCookie := createSession(users[0])
	playerCookie := createSession(users[2])
	router := gin.New()
	auth := account.NewHandler(account.NewPGStore(db), false)
	handler := NewHandler(db, auth)
	handler.Register(router)
	path := "/api/rooms/" + strconv.FormatInt(roomID, 10)
	call := func(method, target, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, target, bytes.NewBufferString(body))
		request.Header.Set("Content-Type", "application/json")
		request.AddCookie(cookie)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}
	type turnView struct {
		TurnUserID int64     `json:"turnUserId"`
		Deadline   time.Time `json:"turnDeadline"`
		Status     string    `json:"status"`
		Answer     string    `json:"answer"`
	}
	view := func() turnView {
		t.Helper()
		res := call(http.MethodGet, path, "", playerCookie)
		var state turnView
		if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &state) != nil {
			t.Fatalf("读取房间失败：%d %s", res.Code, res.Body.String())
		}
		if state.Answer != "" {
			t.Fatal("玩家快照泄露汤底")
		}
		return state
	}

	// 同一到期回合的并发请求只应换到下一位玩家。
	start := make(chan struct{})
	var wait sync.WaitGroup
	var failures []string
	var mu sync.Mutex
	for i := 0; i < 12; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			res := call(http.MethodGet, path, "", playerCookie)
			if res.Code != http.StatusOK {
				mu.Lock()
				failures = append(failures, res.Body.String())
				mu.Unlock()
			}
		}()
	}
	close(start)
	wait.Wait()
	if len(failures) != 0 {
		t.Fatalf("并发快照失败：%v", failures)
	}
	if state := view(); state.TurnUserID != users[2] || !state.Deadline.After(time.Now()) {
		t.Fatalf("到期回合未正确推进：%+v", state)
	}

	// 竞答中的还原不受提问顺序限制。
	if _, err := db.Exec(ctx, `UPDATE rooms SET turn_user_id=$2,turn_deadline=NOW()+INTERVAL '60 seconds' WHERE id=$1`, roomID, users[1]); err != nil {
		t.Fatal(err)
	}
	res := call(http.MethodPost, path+"/actions", `{"type":"restore","body":"我的完整还原","operationId":"restore_01"}`, playerCookie)
	if res.Code != http.StatusOK {
		t.Fatalf("非当前提问者无法提交私有还原：%d %s", res.Code, res.Body.String())
	}

	// 轮到玩家提问后即使截止时间已过，也必须等待主持人回答。
	var questionID int64
	if err := db.QueryRow(ctx, `INSERT INTO room_events(room_id,user_id,kind,body) VALUES($1,$2,'question','待回答问题') RETURNING id`, roomID, users[1]).Scan(&questionID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE rooms SET turn_deadline=NOW()-INTERVAL '1 second' WHERE id=$1`, roomID); err != nil {
		t.Fatal(err)
	}
	if state := view(); state.TurnUserID != users[1] {
		t.Fatalf("未回答的问题被跳过：%+v", state)
	}
	answer := fmt.Sprintf(`{"type":"answer","eventId":%d,"result":"是","operationId":"answer_01"}`, questionID)
	if res := call(http.MethodPost, path+"/actions", answer, hostCookie); res.Code != http.StatusOK {
		t.Fatalf("主持人无法回答已过时问题：%d %s", res.Code, res.Body.String())
	}
	if state := view(); state.TurnUserID != users[2] {
		t.Fatalf("回答后未进入下一位玩家：%+v", state)
	}

	// 暂停期间不能推进；主持恢复后截止时间按离线时长顺延。
	if _, err := db.Exec(ctx, `UPDATE rooms SET status='paused',paused_at=NOW()-INTERVAL '30 seconds',
		turn_user_id=$2,turn_deadline=NOW()-INTERVAL '10 seconds' WHERE id=$1`, roomID, users[1]); err != nil {
		t.Fatal(err)
	}
	if state := view(); state.Status != "paused" || state.TurnUserID != users[1] {
		t.Fatalf("暂停期间错误推进：%+v", state)
	}
	handler.resumeIfHost(roomID, users[0])
	if state := view(); state.Status != "active" || state.TurnUserID != users[1] || !state.Deadline.After(time.Now()) {
		t.Fatalf("恢复后回合时间未顺延：%+v", state)
	}
}
