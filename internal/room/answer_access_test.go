package room_test

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
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"turtle-soup/internal/account"
	"turtle-soup/internal/agent"
	"turtle-soup/internal/room"
	"turtle-soup/migrations"
)

// 已结束真人房间稍后公开汤底时，也要保护刚开始同题 AI 单人局的成员。
func TestPublishAnswerAgainstActiveAI(t *testing.T) {
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
	var author, player int64
	var soups, rooms []int64
	t.Cleanup(func() {
		for _, id := range rooms {
			_, _ = db.Exec(ctx, `DELETE FROM rooms WHERE id=$1`, id)
		}
		_, _ = db.Exec(ctx, `DELETE FROM ai_games WHERE user_id=$1`, player)
		for _, id := range soups {
			_, _ = db.Exec(ctx, `DELETE FROM soups WHERE id=$1`, id)
		}
		_, _ = db.Exec(ctx, `DELETE FROM users WHERE id IN ($1,$2)`, author, player)
	})
	stamp := time.Now().UnixNano()
	for i, target := range []*int64{&author, &player} {
		name := fmt.Sprintf("test_publish_ai_%d_%d", stamp, i)
		if err := db.QueryRow(ctx, `INSERT INTO users(username,password_hash) VALUES($1,'test') RETURNING id`, name).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(token))
	if _, err := db.Exec(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,NOW()+INTERVAL '1 hour')`, hash[:], author); err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: "turtle_session", Value: token}
	router := gin.New()
	room.NewHandler(db, account.NewHandler(account.NewPGStore(db), false)).Register(router)
	publish := func(requestCtx context.Context, id int64, operationID string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{"type": "publish_answer", "operationId": operationID})
		request := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/rooms/%d/actions", id), bytes.NewReader(body)).WithContext(requestCtx)
		request.Header.Set("Content-Type", "application/json")
		request.AddCookie(cookie)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}
	createRoom := func(number int) (int64, int64) {
		t.Helper()
		title := fmt.Sprintf("公开测试题%d", number)
		answer := fmt.Sprintf("公开测试汤底%d", number)
		var soupID, roomID int64
		if err := db.QueryRow(ctx, `INSERT INTO soups(author_id,title,surface,answer,genre,difficulty,horror)
			VALUES($1,$2,'公开测试汤面',$3,'本格',2,1) RETURNING id`, author, title, answer).Scan(&soupID); err != nil {
			t.Fatal(err)
		}
		soups = append(soups, soupID)
		code := fmt.Sprintf("PUBAI%02d%020d", number, stamp)
		if err := db.QueryRow(ctx, `INSERT INTO rooms(code,owner_id,host_id,soup_id,mode,max_players,status,
			soup_title,soup_surface,soup_answer,soup_genre,soup_difficulty,soup_horror)
			VALUES($1,$2,$2,$3,'casual',2,'finished',$4,'公开测试汤面',$5,'本格',2,1) RETURNING id`, code, author, soupID, title, answer).Scan(&roomID); err != nil {
			t.Fatal(err)
		}
		rooms = append(rooms, roomID)
		if _, err := db.Exec(ctx, `INSERT INTO room_members(room_id,user_id,role,seen_answer)
			VALUES($1,$2,'host',TRUE),($1,$3,'player',FALSE)`, roomID, author, player); err != nil {
			t.Fatal(err)
		}
		return soupID, roomID
	}

	store := agent.NewStore(db)
	soupID, roomID := createRoom(1)
	gameID, err := store.Create(ctx, player, soupID)
	if err != nil {
		t.Fatalf("已结束房间的成员未能开始单人局：%v", err)
	}
	blocked := publish(ctx, roomID, "publish_ai_block_1")
	if blocked.Code != http.StatusConflict || !strings.Contains(blocked.Body.String(), "单人对局") {
		t.Fatalf("公开汤底未阻止活跃单人局：%d %s", blocked.Code, blocked.Body.String())
	}
	var published, revealed bool
	if err := db.QueryRow(ctx, `SELECT answer_published FROM rooms WHERE id=$1`, roomID).Scan(&published); err != nil || published {
		t.Fatalf("拒绝公开后房间仍被标记公开：%t %v", published, err)
	}
	if err := db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM soup_reveals WHERE soup_id=$1 AND user_id=$2)`, soupID, player).Scan(&revealed); err != nil || revealed {
		t.Fatalf("拒绝公开后仍记录了汤底查看：%t %v", revealed, err)
	}
	if _, err := db.Exec(ctx, `UPDATE ai_games SET status='finished',finished_at=NOW() WHERE id=$1`, gameID); err != nil {
		t.Fatal(err)
	}
	if res := publish(ctx, roomID, "publish_ai_after_1"); res.Code != http.StatusOK {
		t.Fatalf("单人局结束后仍不能公开：%d %s", res.Code, res.Body.String())
	}

	// 并发开始单人局和公开汤底：题目行锁规定先后，只能成功一个。
	soupID2, roomID2 := createRoom(2)
	type result struct {
		kind string
		ok   bool
		err  error
	}
	raceCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	start := make(chan struct{})
	results := make(chan result, 2)
	go func() {
		<-start
		_, err := store.Create(raceCtx, player, soupID2)
		if err == nil {
			results <- result{kind: "ai", ok: true}
			return
		}
		results <- result{kind: "ai", err: err}
	}()
	go func() {
		<-start
		response := publish(raceCtx, roomID2, "publish_ai_race_1")
		results <- result{kind: "publish", ok: response.Code == http.StatusOK, err: fmt.Errorf("HTTP %d: %s", response.Code, response.Body.String())}
	}()
	close(start)
	first, second := <-results, <-results
	var aiOK, publishOK bool
	for _, item := range []result{first, second} {
		if item.kind == "ai" {
			aiOK = item.ok
			if !item.ok && !errors.Is(item.err, agent.ErrKnownAnswer) {
				t.Fatalf("并发开局意外失败：%v", item.err)
			}
		} else {
			publishOK = item.ok
			if !item.ok && !strings.Contains(item.err.Error(), "单人对局") {
				t.Fatalf("并发公开意外失败：%v", item.err)
			}
		}
	}
	if aiOK == publishOK {
		t.Fatalf("并发开局与公开结果冲突：AI=%t 公开=%t", aiOK, publishOK)
	}
	var activeAI, publishedAnswer bool
	if err := db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ai_games WHERE user_id=$1 AND soup_id=$2 AND status='active')`, player, soupID2).Scan(&activeAI); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `SELECT answer_published FROM rooms WHERE id=$1`, roomID2).Scan(&publishedAnswer); err != nil {
		t.Fatal(err)
	}
	if activeAI != aiOK || publishedAnswer != publishOK {
		t.Fatalf("并发结果与数据库状态不一致：AI=%t/%t 公开=%t/%t", aiOK, activeAI, publishOK, publishedAnswer)
	}
}
