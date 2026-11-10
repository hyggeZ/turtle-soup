package forum_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
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
	"turtle-soup/internal/forum"
	"turtle-soup/migrations"
)

func TestSpoilerRequiresLeavingPlayerSeat(t *testing.T) {
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

	stamp := time.Now().UnixNano()
	ids := make([]int64, 2)
	for i := range ids {
		name := fmt.Sprintf("test_spoiler_%d_%d", stamp, i)
		if err := db.QueryRow(ctx, `INSERT INTO users(username,password_hash) VALUES($1,'test') RETURNING id`, name).Scan(&ids[i]); err != nil {
			t.Fatal(err)
		}
	}
	var soupID, roomID, commentID int64
	t.Cleanup(func() {
		if roomID != 0 {
			_, _ = db.Exec(ctx, `DELETE FROM rooms WHERE id=$1`, roomID)
		}
		if soupID != 0 {
			_, _ = db.Exec(ctx, `DELETE FROM soups WHERE id=$1`, soupID)
		}
		for _, id := range ids {
			if id != 0 {
				_, _ = db.Exec(ctx, `DELETE FROM users WHERE id=$1`, id)
			}
		}
	})
	if err := db.QueryRow(ctx, `INSERT INTO soups(author_id,title,surface,answer,genre,difficulty,horror) VALUES($1,'剧透测试','测试汤面','测试汤底','本格',2,1) RETURNING id`, ids[0]).Scan(&soupID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `INSERT INTO comments(soup_id,author_id,body,spoiler) VALUES($1,$2,'这是剧透内容',TRUE) RETURNING id`, soupID, ids[0]).Scan(&commentID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `INSERT INTO rooms(code,owner_id,host_id,soup_id,mode,max_players) VALUES($1,$2,$2,$3,'casual',2) RETURNING id`, fmt.Sprintf("S%015d", stamp%1e15), ids[0], soupID).Scan(&roomID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO room_members(room_id,user_id,role) VALUES($1,$2,'player')`, roomID, ids[1]); err != nil {
		t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	hash := sha256.Sum256([]byte(token))
	if _, err := db.Exec(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,NOW()+INTERVAL '1 hour')`, hash[:], ids[1]); err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	forum.NewHandler(forum.NewStore(db), account.NewHandler(account.NewPGStore(db), false)).Register(router)
	call := func(method, path string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, nil)
		request.AddCookie(&http.Cookie{Name: "turtle_session", Value: token})
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}
	comments := call(http.MethodGet, fmt.Sprintf("/api/soups/%d/comments", soupID))
	if comments.Code != http.StatusOK || strings.Contains(comments.Body.String(), "这是剧透内容") || !strings.Contains(comments.Body.String(), `"spoiler":true`) {
		t.Fatalf("评论列表泄露剧透：%d %s", comments.Code, comments.Body.String())
	}
	path := fmt.Sprintf("/api/soups/%d/comments/%d/spoiler", soupID, commentID)
	if res := call(http.MethodGet, path); res.Code != http.StatusNotFound {
		t.Fatalf("GET 不应执行揭底操作：%d %s", res.Code, res.Body.String())
	}
	blocked := call(http.MethodPost, path)
	if blocked.Code != http.StatusForbidden || strings.Contains(blocked.Body.String(), "这是剧透内容") {
		t.Fatalf("玩家展开了剧透：%d %s", blocked.Code, blocked.Body.String())
	}
	if _, err := db.Exec(ctx, `UPDATE room_members SET role='spectator' WHERE room_id=$1 AND user_id=$2`, roomID, ids[1]); err != nil {
		t.Fatal(err)
	}
	opened := call(http.MethodPost, path)
	if opened.Code != http.StatusOK || !strings.Contains(opened.Body.String(), "这是剧透内容") {
		t.Fatalf("旁观者无法主动展开：%d %s", opened.Code, opened.Body.String())
	}
	var revealed bool
	if err := db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM soup_reveals WHERE soup_id=$1 AND user_id=$2)`, soupID, ids[1]).Scan(&revealed); err != nil || !revealed {
		t.Fatalf("未保存剧透查看记录：%v", err)
	}
}
