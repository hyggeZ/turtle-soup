package forum_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
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

const (
	commentAuthor = iota
	commentPlayer
	commentReader
	commentKnown
	commentAIPlayer
)

type commentFixture struct {
	t      *testing.T
	db     *pgxpool.Pool
	router *gin.Engine
	users  [5]int64
	tokens [5]string
}

func newCommentFixture(t *testing.T) *commentFixture {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("需要 TEST_DATABASE_URL")
	}
	ctx := context.Background()
	adminDB, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(adminDB.Close)
	schema := fmt.Sprintf("forum_comment_access_%d", time.Now().UnixNano())
	if _, err := adminDB.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = adminDB.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })
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
	f := &commentFixture{t: t, db: db}
	for i := range f.users {
		name := fmt.Sprintf("comment_access_%d_%d", time.Now().UnixNano(), i)
		if err := db.QueryRow(ctx, `INSERT INTO users(username,password_hash) VALUES($1,'test') RETURNING id`, name).Scan(&f.users[i]); err != nil {
			t.Fatal(err)
		}
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			t.Fatal(err)
		}
		f.tokens[i] = base64.RawURLEncoding.EncodeToString(raw)
		hash := sha256.Sum256([]byte(f.tokens[i]))
		if _, err := db.Exec(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,NOW()+INTERVAL '1 hour')`, hash[:], f.users[i]); err != nil {
			t.Fatal(err)
		}
	}
	gin.SetMode(gin.TestMode)
	f.router = gin.New()
	auth := account.NewHandler(account.NewPGStore(db), false)
	forum.NewHandler(forum.NewStore(db), auth).Register(f.router)
	room.NewHandler(db, auth).Register(f.router)
	return f
}

func (f *commentFixture) call(method, path string, who int, body any) *httptest.ResponseRecorder {
	f.t.Helper()
	var input []byte
	if body != nil {
		var err error
		input, err = json.Marshal(body)
		if err != nil {
			f.t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	request := httptest.NewRequest(method, path, bytes.NewReader(input)).WithContext(ctx)
	if who >= 0 {
		request.AddCookie(&http.Cookie{Name: "turtle_session", Value: f.tokens[who]})
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	f.router.ServeHTTP(response, request)
	return response
}

func (f *commentFixture) soupWithComment(marker string) (int64, int64) {
	f.t.Helper()
	ctx := context.Background()
	var soupID, commentID int64
	if err := f.db.QueryRow(ctx, `INSERT INTO soups(author_id,title,surface,answer,genre,difficulty,horror)
		VALUES($1,'评论权限题','这是供测试的汤面',$2,'本格',2,0) RETURNING id`, f.users[commentAuthor], marker).Scan(&soupID); err != nil {
		f.t.Fatal(err)
	}
	postPath := fmt.Sprintf("/api/soups/%d/comments", soupID)
	if res := f.call(http.MethodPost, postPath, commentAuthor, map[string]any{"body": marker, "spoiler": false}); res.Code != http.StatusCreated {
		f.t.Fatalf("无法发布未标记汤底评论：%d %s", res.Code, res.Body.String())
	}
	if err := f.db.QueryRow(ctx, `SELECT id FROM comments WHERE soup_id=$1 ORDER BY id DESC LIMIT 1`, soupID).Scan(&commentID); err != nil {
		f.t.Fatal(err)
	}
	return soupID, commentID
}

func (f *commentFixture) roomFor(soupID int64, status string) (int64, string) {
	f.t.Helper()
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		f.t.Fatal(err)
	}
	code := strings.ToUpper(hex.EncodeToString(raw))
	var roomID int64
	if err := f.db.QueryRow(context.Background(), `INSERT INTO rooms(code,owner_id,host_id,soup_id,mode,max_players,status)
		VALUES($1,$2,$2,$3,'casual',3,$4) RETURNING id`, code, f.users[commentAuthor], soupID, status).Scan(&roomID); err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.db.Exec(context.Background(), `INSERT INTO room_members(room_id,user_id,role,seen_answer) VALUES($1,$2,'host',TRUE)`, roomID, f.users[commentAuthor]); err != nil {
		f.t.Fatal(err)
	}
	return roomID, code
}

func (f *commentFixture) revealed(soupID int64, who int) bool {
	f.t.Helper()
	var yes bool
	if err := f.db.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM soup_reveals WHERE soup_id=$1 AND user_id=$2)`, soupID, f.users[who]).Scan(&yes); err != nil {
		f.t.Fatal(err)
	}
	return yes
}

// 所有评论正文都要主动展开；发帖者未标记剧透也不能绕过同题玩家限制。
func TestUnmarkedCommentAccess(t *testing.T) {
	f := newCommentFixture(t)
	soupID, commentID := f.soupWithComment("完整汤底：凶手是未标记评论")
	listPath := fmt.Sprintf("/api/soups/%d/comments", soupID)
	readPath := fmt.Sprintf("%s/%d/spoiler", listPath, commentID)
	for _, who := range []int{-1, commentAuthor, commentPlayer, commentReader, commentKnown} {
		detailPath := fmt.Sprintf("/api/soups/%d", soupID)
		res := f.call(http.MethodGet, detailPath, who, nil)
		// 作者本来就能从详情读取自己写的汤底。
		if res.Code != http.StatusOK || (who != commentAuthor && strings.Contains(res.Body.String(), "完整汤底：凶手是未标记评论")) {
			t.Fatalf("身份 %d 从详情意外读到汤底：%d %s", who, res.Code, res.Body.String())
		}
		res = f.call(http.MethodGet, listPath, who, nil)
		if res.Code != http.StatusOK || strings.Contains(res.Body.String(), "完整汤底：凶手是未标记评论") {
			t.Fatalf("身份 %d 默认读到评论正文：%d %s", who, res.Code, res.Body.String())
		}
	}
	if res := f.call(http.MethodPost, readPath, -1, nil); res.Code != http.StatusUnauthorized || strings.Contains(res.Body.String(), "未标记评论") {
		t.Fatalf("匿名展开评论：%d %s", res.Code, res.Body.String())
	}
	if res := f.call(http.MethodGet, readPath, commentReader, nil); res.Code != http.StatusNotFound {
		t.Fatalf("GET 执行了揭示动作：%d %s", res.Code, res.Body.String())
	}
	for _, path := range []string{
		fmt.Sprintf("%s/%d/spoiler", listPath, commentID+1000000),
		fmt.Sprintf("/api/soups/%d/comments/%d/spoiler", soupID+1000000, commentID),
	} {
		if res := f.call(http.MethodPost, path, commentReader, nil); res.Code != http.StatusNotFound || strings.Contains(res.Body.String(), "未标记评论") {
			t.Fatalf("错误 ID 暴露评论：%s %d %s", path, res.Code, res.Body.String())
		}
	}
	if f.revealed(soupID, commentReader) {
		t.Fatal("错误 ID 意外记录了揭底")
	}

	roomID, _ := f.roomFor(soupID, "waiting")
	if _, err := f.db.Exec(context.Background(), `INSERT INTO room_members(room_id,user_id,role) VALUES($1,$2,'player')`, roomID, f.users[commentPlayer]); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"waiting", "active", "paused"} {
		if _, err := f.db.Exec(context.Background(), `UPDATE rooms SET status=$2 WHERE id=$1`, roomID, status); err != nil {
			t.Fatal(err)
		}
		if res := f.call(http.MethodPost, readPath, commentPlayer, nil); res.Code != http.StatusForbidden || strings.Contains(res.Body.String(), "未标记评论") {
			t.Fatalf("%s 玩家展开未标记汤底：%d %s", status, res.Code, res.Body.String())
		}
		if f.revealed(soupID, commentPlayer) {
			t.Fatalf("%s 玩家被记录为已揭底", status)
		}
	}
	if _, err := f.db.Exec(context.Background(), `INSERT INTO ai_games(user_id,soup_id,title,surface,answer) VALUES($1,$2,'AI 测试','测试汤面','测试汤底')`, f.users[commentAIPlayer], soupID); err != nil {
		t.Fatal(err)
	}
	if res := f.call(http.MethodPost, readPath, commentAIPlayer, nil); res.Code != http.StatusForbidden || strings.Contains(res.Body.String(), "未标记评论") || f.revealed(soupID, commentAIPlayer) {
		t.Fatalf("AI 玩家展开未标记汤底：%d %s", res.Code, res.Body.String())
	}

	if _, err := f.db.Exec(context.Background(), `INSERT INTO soup_reveals(soup_id,user_id) VALUES($1,$2)`, soupID, f.users[commentKnown]); err != nil {
		t.Fatal(err)
	}
	for _, who := range []int{commentAuthor, commentKnown, commentReader} {
		res := f.call(http.MethodPost, readPath, who, nil)
		if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "完整汤底：凶手是未标记评论") || !f.revealed(soupID, who) {
			t.Fatalf("身份 %d 无法主动展开评论：%d %s", who, res.Code, res.Body.String())
		}
	}
	if _, err := f.db.Exec(context.Background(), `UPDATE room_members SET role='spectator' WHERE room_id=$1 AND user_id=$2`, roomID, f.users[commentPlayer]); err != nil {
		t.Fatal(err)
	}
	if res := f.call(http.MethodPost, readPath, commentPlayer, nil); res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "完整汤底：凶手是未标记评论") || !f.revealed(soupID, commentPlayer) {
		t.Fatalf("离席旁观者无法展开评论：%d %s", res.Code, res.Body.String())
	}
	if _, err := agent.NewStore(f.db).Create(context.Background(), f.users[commentReader], soupID); !errors.Is(err, agent.ErrKnownAnswer) {
		t.Fatalf("读评论后仍可开始同题 AI 单人局：%v", err)
	}
	if res := f.call(http.MethodPost, "/api/rooms", commentReader, map[string]any{"soupId": soupID, "mode": "casual", "maxPlayers": 2, "hostSelf": false}); res.Code != http.StatusConflict {
		t.Fatalf("读评论后仍可自建同题玩家房：%d %s", res.Code, res.Body.String())
	}
	_, code := f.roomFor(soupID, "waiting")
	if res := f.call(http.MethodPost, "/api/rooms/join", commentReader, map[string]string{"code": code}); res.Code != http.StatusOK {
		t.Fatalf("读评论后不能以旁观者入场：%d %s", res.Code, res.Body.String())
	}
	var role string
	if err := f.db.QueryRow(context.Background(), `SELECT m.role FROM room_members m JOIN rooms r ON r.id=m.room_id WHERE r.code=$1 AND m.user_id=$2`, code, f.users[commentReader]).Scan(&role); err != nil || role != "spectator" {
		t.Fatalf("读评论后进入了玩家席位：%q %v", role, err)
	}
}

// 并发揭示评论和开局共享题目行锁：两边不能同时获得“已读评论”和玩家资格。
func TestCommentAccessRacesWithPlayerEntry(t *testing.T) {
	f := newCommentFixture(t)
	for _, kind := range []string{"create", "join", "ai"} {
		t.Run(kind, func(t *testing.T) {
			marker := "并发评论汤底-" + kind
			soupID, commentID := f.soupWithComment(marker)
			readPath := fmt.Sprintf("/api/soups/%d/comments/%d/spoiler", soupID, commentID)
			var roomID int64
			var code string
			if kind == "join" {
				roomID, code = f.roomFor(soupID, "waiting")
			}
			start := make(chan struct{})
			var group sync.WaitGroup
			var readRes, entryRes *httptest.ResponseRecorder
			var aiErr error
			group.Add(2)
			go func() {
				defer group.Done()
				<-start
				readRes = f.call(http.MethodPost, readPath, commentReader, nil)
			}()
			go func() {
				defer group.Done()
				<-start
				switch kind {
				case "create":
					entryRes = f.call(http.MethodPost, "/api/rooms", commentReader, map[string]any{"soupId": soupID, "mode": "casual", "maxPlayers": 2, "hostSelf": false})
				case "join":
					entryRes = f.call(http.MethodPost, "/api/rooms/join", commentReader, map[string]string{"code": code})
				case "ai":
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					_, aiErr = agent.NewStore(f.db).Create(ctx, f.users[commentReader], soupID)
				}
			}()
			close(start)
			group.Wait()
			if readRes.Code != http.StatusOK && readRes.Code != http.StatusForbidden {
				t.Fatalf("评论并发读取异常：%d %s", readRes.Code, readRes.Body.String())
			}
			read := readRes.Code == http.StatusOK
			if read != strings.Contains(readRes.Body.String(), marker) {
				t.Fatalf("评论正文与授权结果不一致：%d %s", readRes.Code, readRes.Body.String())
			}
			if f.revealed(soupID, commentReader) != read {
				t.Fatal("评论揭示记录与响应不一致")
			}
			switch kind {
			case "create":
				if (entryRes.Code == http.StatusCreated) == read || (entryRes.Code != http.StatusCreated && entryRes.Code != http.StatusConflict) {
					t.Fatalf("建房与评论揭示同时成功或同时失败：揭示 %d，建房 %d %s", readRes.Code, entryRes.Code, entryRes.Body.String())
				}
			case "join":
				if entryRes.Code != http.StatusOK {
					t.Fatalf("入场异常：%d %s", entryRes.Code, entryRes.Body.String())
				}
				var role string
				if err := f.db.QueryRow(context.Background(), `SELECT role FROM room_members WHERE room_id=$1 AND user_id=$2`, roomID, f.users[commentReader]).Scan(&role); err != nil || (role == "player") == read {
					t.Fatalf("评论揭示与入场角色冲突：已读 %t，角色 %q，错误 %v", read, role, err)
				}
			case "ai":
				if read && !errors.Is(aiErr, agent.ErrKnownAnswer) || !read && aiErr != nil {
					t.Fatalf("AI 开局与评论揭示冲突：已读 %t，错误 %v", read, aiErr)
				}
			}
		})
	}
}
