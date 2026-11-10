package forum_test

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
	"turtle-soup/internal/forum"
	"turtle-soup/internal/room"
	"turtle-soup/migrations"
)

func TestSoupAuthorManageAndGameSnapshots(t *testing.T) {
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

	var users [3]int64
	var cookies [3]*http.Cookie
	var soupID, roomID, gameID, soupID2, roomID2 int64
	t.Cleanup(func() {
		if roomID2 != 0 {
			_, _ = db.Exec(ctx, `DELETE FROM rooms WHERE id=$1`, roomID2)
		}
		if roomID != 0 {
			_, _ = db.Exec(ctx, `DELETE FROM rooms WHERE id=$1`, roomID)
		}
		if gameID != 0 {
			_, _ = db.Exec(ctx, `DELETE FROM ai_games WHERE id=$1`, gameID)
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
	stamp := time.Now().UnixNano()
	for i := range users {
		name := fmt.Sprintf("test_manage_%d_%d", stamp, i)
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

	auth := account.NewHandler(account.NewPGStore(db), false)
	router := gin.New()
	forum.NewHandler(forum.NewStore(db), auth).Register(router)
	room.NewHandler(db, auth).Register(router)
	call := func(method, path string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		var data []byte
		if body != nil {
			var err error
			data, err = json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
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
	oldAnswer := fmt.Sprintf("初始汤底-%d", stamp)
	input := map[string]any{"title": "初始题目", "surface": "这是一条初始汤面", "answer": oldAnswer, "genre": "本格", "difficulty": 2, "horror": 1}
	created := call(http.MethodPost, "/api/soups", input, cookies[0])
	if created.Code != http.StatusCreated {
		t.Fatalf("创建题目失败：%d %s", created.Code, created.Body.String())
	}
	var output struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	soupID = output.ID
	path := fmt.Sprintf("/api/soups/%d", soupID)
	if res := call(http.MethodGet, path, nil, nil); res.Code != http.StatusOK || strings.Contains(res.Body.String(), oldAnswer) {
		t.Fatalf("匿名详情泄露汤底：%d %s", res.Code, res.Body.String())
	}
	if res := call(http.MethodGet, path, nil, cookies[1]); res.Code != http.StatusOK || strings.Contains(res.Body.String(), oldAnswer) {
		t.Fatalf("其他用户详情泄露汤底：%d %s", res.Code, res.Body.String())
	}
	if res := call(http.MethodGet, path, nil, cookies[0]); res.Code != http.StatusOK || !strings.Contains(res.Body.String(), oldAnswer) {
		t.Fatalf("作者无法预填汤底：%d %s", res.Code, res.Body.String())
	}
	if res := call(http.MethodGet, "/api/soups", nil, cookies[0]); strings.Contains(res.Body.String(), oldAnswer) {
		t.Fatalf("作者列表泄露汤底：%s", res.Body.String())
	}
	if res := call(http.MethodPatch, path, input, nil); res.Code != http.StatusUnauthorized {
		t.Fatalf("匿名用户可以编辑：%d %s", res.Code, res.Body.String())
	}
	if res := call(http.MethodPatch, path, input, cookies[1]); res.Code != http.StatusForbidden {
		t.Fatalf("其他用户可以编辑：%d %s", res.Code, res.Body.String())
	}
	if res := call(http.MethodDelete, path, nil, cookies[1]); res.Code != http.StatusForbidden {
		t.Fatalf("其他用户可以删除：%d %s", res.Code, res.Body.String())
	}
	if res := call(http.MethodPatch, path, map[string]any{"title": "短"}, cookies[0]); res.Code != http.StatusBadRequest {
		t.Fatalf("不完整编辑被接受：%d %s", res.Code, res.Body.String())
	}
	newAnswer := fmt.Sprintf("修改后的汤底-%d", stamp)
	input = map[string]any{"title": "修改后的题目", "surface": "修改后的完整汤面", "answer": newAnswer, "genre": "变格", "difficulty": 4, "horror": 3}
	if res := call(http.MethodPatch, path, input, cookies[0]); res.Code != http.StatusOK || !strings.Contains(res.Body.String(), newAnswer) || !strings.Contains(res.Body.String(), `"genre":"变格"`) {
		t.Fatalf("作者编辑失败：%d %s", res.Code, res.Body.String())
	}
	if res := call(http.MethodGet, path, nil, cookies[1]); res.Code != http.StatusOK || strings.Contains(res.Body.String(), newAnswer) || !strings.Contains(res.Body.String(), "修改后的题目") {
		t.Fatalf("公开详情未正确更新：%d %s", res.Code, res.Body.String())
	}

	roomInput := map[string]any{"soupId": soupID, "mode": "casual", "maxPlayers": 3, "public": true}
	roomCreated := call(http.MethodPost, "/api/rooms", roomInput, cookies[0])
	if roomCreated.Code != http.StatusCreated {
		t.Fatalf("创建真人房间失败：%d %s", roomCreated.Code, roomCreated.Body.String())
	}
	var roomOutput struct {
		ID   int64  `json:"id"`
		Code string `json:"code"`
	}
	if err := json.Unmarshal(roomCreated.Body.Bytes(), &roomOutput); err != nil {
		t.Fatal(err)
	}
	roomID = roomOutput.ID
	firstRoomCode := roomOutput.Code
	gameID, err = agent.NewStore(db).Create(ctx, users[2], soupID)
	if err != nil {
		t.Fatalf("创建单人对局失败：%v", err)
	}
	if res := call(http.MethodPatch, path, input, cookies[0]); res.Code != http.StatusConflict {
		t.Fatalf("活跃对局期间允许编辑：%d %s", res.Code, res.Body.String())
	}
	if res := call(http.MethodDelete, path, nil, cookies[0]); res.Code != http.StatusNoContent {
		t.Fatalf("作者软删除失败：%d %s", res.Code, res.Body.String())
	}
	if res := call(http.MethodGet, path, nil, cookies[0]); res.Code != http.StatusNotFound {
		t.Fatalf("已删除题目详情仍可见：%d %s", res.Code, res.Body.String())
	}
	if res := call(http.MethodGet, "/api/soups", nil, cookies[0]); strings.Contains(res.Body.String(), "修改后的题目") {
		t.Fatalf("已删除题目仍在广场：%s", res.Body.String())
	}
	if res := call(http.MethodPost, "/api/rooms", roomInput, cookies[0]); res.Code != http.StatusNotFound {
		t.Fatalf("删除后仍能开真人房间：%d %s", res.Code, res.Body.String())
	}
	// 等待中的另一间房不能换成已删除题目。
	second := map[string]any{"title": "第二道题目", "surface": "第二道题目的汤面", "answer": "第二道题目的汤底", "genre": "本格", "difficulty": 2, "horror": 0}
	created = call(http.MethodPost, "/api/soups", second, cookies[0])
	if created.Code != http.StatusCreated {
		t.Fatalf("创建另一题目失败：%d %s", created.Code, created.Body.String())
	}
	if err := json.Unmarshal(created.Body.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	soupID2 = output.ID
	otherRoomInput := map[string]any{"soupId": soupID2, "mode": "casual", "maxPlayers": 3, "public": false}
	roomCreated = call(http.MethodPost, "/api/rooms", otherRoomInput, cookies[0])
	if roomCreated.Code != http.StatusCreated {
		t.Fatalf("创建另一房间失败：%d %s", roomCreated.Code, roomCreated.Body.String())
	}
	if err := json.Unmarshal(roomCreated.Body.Bytes(), &roomOutput); err != nil {
		t.Fatal(err)
	}
	roomID2 = roomOutput.ID
	if res := call(http.MethodPatch, fmt.Sprintf("/api/rooms/%d/settings", roomID2), roomInput, cookies[0]); res.Code != http.StatusNotFound {
		t.Fatalf("房间换入已删除题目：%d %s", res.Code, res.Body.String())
	}
	var unchangedSoupID int64
	if err := db.QueryRow(ctx, `SELECT soup_id FROM rooms WHERE id=$1`, roomID2).Scan(&unchangedSoupID); err != nil || unchangedSoupID != soupID2 {
		t.Fatalf("换题失败后房间题目被修改：%d %v", unchangedSoupID, err)
	}
	if _, err := agent.NewStore(db).Create(ctx, users[1], soupID); !errors.Is(err, agent.ErrNotFound) {
		t.Fatalf("删除后仍能开单人局：%v", err)
	}
	if res := call(http.MethodPost, "/api/rooms/join", map[string]any{"code": firstRoomCode}, cookies[1]); res.Code != http.StatusOK {
		t.Fatalf("软删除影响旧房间入场：%d %s", res.Code, res.Body.String())
	}
	roomView := call(http.MethodGet, fmt.Sprintf("/api/rooms/%d", roomID), nil, cookies[0])
	if roomView.Code != http.StatusOK || !strings.Contains(roomView.Body.String(), newAnswer) || !strings.Contains(roomView.Body.String(), "修改后的题目") {
		t.Fatalf("旧真人房间快照失效：%d %s", roomView.Code, roomView.Body.String())
	}
	game, err := agent.NewStore(db).Get(ctx, users[2], gameID)
	if err != nil || game.Title != "修改后的题目" || game.Surface != "修改后的完整汤面" || game.Answer != "" {
		t.Fatalf("旧单人局快照异常：%+v %v", game, err)
	}
	var savedAnswer string
	if err := db.QueryRow(ctx, `SELECT answer FROM ai_games WHERE id=$1`, gameID).Scan(&savedAnswer); err != nil || savedAnswer != newAnswer {
		t.Fatalf("单人局汤底快照丢失：%q %v", savedAnswer, err)
	}
	if _, _, _, err := agent.NewStore(db).Claim(ctx, users[2], gameID, "after_delete_1", "question", "继续提问"); err != nil {
		t.Fatalf("软删除影响旧单人局继续操作：%v", err)
	}
	agent.NewStore(db).Release(ctx, users[2], gameID, "after_delete_1")
}
