package room_test

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
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"turtle-soup/internal/account"
	"turtle-soup/internal/room"
	"turtle-soup/migrations"
)

type staleRestoreFixture struct {
	t       *testing.T
	router  *gin.Engine
	roomID  int64
	users   [3]int64 // 主持人、离席玩家、继续推理的玩家。
	cookies [3]*http.Cookie
}

type staleRestoreView struct {
	Status     string `json:"status"`
	WinnerID   *int64 `json:"winnerId"`
	TurnUserID *int64 `json:"turnUserId"`
	Answer     string `json:"answer"`
	Members    []struct {
		ID      int64  `json:"id"`
		Role    string `json:"role"`
		Correct bool   `json:"correct"`
	} `json:"members"`
	Events []struct {
		ID            int64  `json:"id"`
		Kind          string `json:"kind"`
		Body          string `json:"body"`
		Result        string `json:"result"`
		ParentID      *int64 `json:"parentId"`
		SubjectUserID *int64 `json:"subjectUserId"`
	} `json:"events"`
}

func newStaleRestoreFixture(t *testing.T, db *pgxpool.Pool, mode string) *staleRestoreFixture {
	t.Helper()
	ctx := context.Background()
	f := &staleRestoreFixture{t: t}
	var soupID int64
	t.Cleanup(func() {
		if f.roomID != 0 {
			_, _ = db.Exec(ctx, `DELETE FROM rooms WHERE id=$1`, f.roomID)
		}
		if soupID != 0 {
			_, _ = db.Exec(ctx, `DELETE FROM soups WHERE id=$1`, soupID)
		}
		for _, id := range f.users {
			if id != 0 {
				_, _ = db.Exec(ctx, `DELETE FROM users WHERE id=$1`, id)
			}
		}
	})
	stamp := time.Now().UnixNano()
	for i := range f.users {
		name := fmt.Sprintf("stale_restore_%d_%d", stamp, i)
		if err := db.QueryRow(ctx, `INSERT INTO users(username,password_hash) VALUES($1,'test') RETURNING id`, name).Scan(&f.users[i]); err != nil {
			t.Fatal(err)
		}
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			t.Fatal(err)
		}
		token := base64.RawURLEncoding.EncodeToString(raw)
		hash := sha256.Sum256([]byte(token))
		if _, err := db.Exec(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,NOW()+INTERVAL '1 hour')`, hash[:], f.users[i]); err != nil {
			t.Fatal(err)
		}
		f.cookies[i] = &http.Cookie{Name: "turtle_session", Value: token}
	}
	if err := db.QueryRow(ctx, `INSERT INTO soups(author_id,title,surface,answer,genre,difficulty,horror)
		VALUES($1,'离席还原测试','测试汤面','测试汤底','本格',2,0) RETURNING id`, f.users[0]).Scan(&soupID); err != nil {
		t.Fatal(err)
	}
	code := fmt.Sprintf("STALE%d", stamp)
	if err := db.QueryRow(ctx, `INSERT INTO rooms(code,owner_id,host_id,soup_id,mode,max_players,status,turn_user_id,turn_deadline,
		soup_title,soup_surface,soup_answer,soup_genre,soup_difficulty,soup_horror)
		VALUES($1,$2,$2,$3,$4,2,'active',$5,NOW()+INTERVAL '60 seconds',
		'离席还原测试','测试汤面','测试汤底','本格',2,0) RETURNING id`, code, f.users[0], soupID, mode, f.users[1]).Scan(&f.roomID); err != nil {
		t.Fatal(err)
	}
	for i, role := range []string{"host", "player", "player"} {
		if _, err := db.Exec(ctx, `INSERT INTO room_members(room_id,user_id,role,seen_answer) VALUES($1,$2,$3,$4)`, f.roomID, f.users[i], role, i == 0); err != nil {
			t.Fatal(err)
		}
	}
	f.router = gin.New()
	room.NewHandler(db, account.NewHandler(account.NewPGStore(db), false)).Register(f.router)
	return f
}

func (f *staleRestoreFixture) call(user int, method, path string, body any) *httptest.ResponseRecorder {
	f.t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		f.t.Fatal(err)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(data))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(f.cookies[user])
	response := httptest.NewRecorder()
	f.router.ServeHTTP(response, request)
	return response
}

func (f *staleRestoreFixture) action(user int, kind, operation string, fields map[string]any) *httptest.ResponseRecorder {
	f.t.Helper()
	body := map[string]any{"type": kind, "operationId": operation}
	for key, value := range fields {
		body[key] = value
	}
	return f.call(user, http.MethodPost, fmt.Sprintf("/api/rooms/%d/actions", f.roomID), body)
}

func (f *staleRestoreFixture) view(user int) staleRestoreView {
	f.t.Helper()
	response := f.call(user, http.MethodGet, fmt.Sprintf("/api/rooms/%d", f.roomID), nil)
	var state staleRestoreView
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &state) != nil {
		f.t.Fatalf("读取房间失败：%d %s", response.Code, response.Body.String())
	}
	return state
}

func staleRestoreEventID(t *testing.T, view staleRestoreView, kind string) int64 {
	t.Helper()
	for _, event := range view.Events {
		if event.Kind == kind {
			return event.ID
		}
	}
	t.Fatalf("主持人快照缺少 %s 事件", kind)
	return 0
}

// 玩家离席时，尚未判定的还原失效；其他玩家仍能继续推理并获胜。
func TestStaleRestoreAfterLeaving(t *testing.T) {
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
	for _, mode := range []string{"competitive", "casual"} {
		for _, leaving := range []string{"retire", "answer_approved"} {
			t.Run(mode+"/"+leaving, func(t *testing.T) {
				f := newStaleRestoreFixture(t, db, mode)
				secret := "只有提交者和主持人可见的还原"
				if res := f.action(1, "restore", "stale_restore_01", map[string]any{"body": secret}); res.Code != http.StatusOK {
					t.Fatalf("提交还原失败：%d %s", res.Code, res.Body.String())
				}
				restoreID := staleRestoreEventID(t, f.view(0), "restore")
				var leave *httptest.ResponseRecorder
				if leaving == "retire" {
					leave = f.action(1, "retire", "stale_retire_01", nil)
				} else {
					if res := f.action(1, "answer_request", "stale_request_01", nil); res.Code != http.StatusOK {
						t.Fatalf("申请看底失败：%d %s", res.Code, res.Body.String())
					}
					requestID := staleRestoreEventID(t, f.view(0), "answer_request")
					leave = f.action(0, "answer_decide", "stale_approve_01", map[string]any{"eventId": requestID, "result": "同意"})
				}
				if leave.Code != http.StatusOK {
					t.Fatalf("离席失败：%d %s", leave.Code, leave.Body.String())
				}
				if leaving == "retire" {
					leave = f.action(1, "retire", "stale_retire_01", nil)
				} else {
					requestID := staleRestoreEventID(t, f.view(0), "answer_request")
					leave = f.action(0, "answer_decide", "stale_approve_01", map[string]any{"eventId": requestID, "result": "同意"})
				}
				if leave.Code != http.StatusOK {
					t.Fatalf("重复离席操作失败：%d %s", leave.Code, leave.Body.String())
				}
				if res := f.action(0, "judge", "stale_judge_01", map[string]any{"eventId": restoreID, "result": "正确"}); res.Code != http.StatusConflict {
					t.Fatalf("离席还原仍可判为正确：%d %s", res.Code, res.Body.String())
				}
				host := f.view(0)
				if host.Status != "active" || host.WinnerID != nil {
					t.Fatalf("离席还原错误结束对局：status=%s winner=%v", host.Status, host.WinnerID)
				}
				if mode == "competitive" && (host.TurnUserID == nil || *host.TurnUserID != f.users[2]) {
					t.Fatalf("退席后未轮到其他玩家：%v", host.TurnUserID)
				}
				for _, member := range host.Members {
					if member.ID == f.users[1] && (member.Role != "spectator" || member.Correct) {
						t.Fatalf("退席玩家仍可获胜或被标记答对：%+v", member)
					}
				}
				invalid := 0
				for _, event := range host.Events {
					if event.Kind == "restore_result" && event.ParentID != nil && *event.ParentID == restoreID {
						if event.Result != "已失效" {
							t.Fatalf("离席还原回复不正确：%s", event.Result)
						}
						invalid++
					}
				}
				if invalid != 1 {
					t.Fatalf("失效结果数量错误：%d", invalid)
				}
				other := f.view(2)
				visibleResult := false
				for _, event := range other.Events {
					if strings.Contains(event.Body, secret) {
						t.Fatal("其他玩家看到了私人还原正文")
					}
					if event.Kind == "restore" {
						t.Fatal("其他玩家看到了私人还原事件")
					}
					if event.Kind == "restore_result" && event.Result == "已失效" && event.SubjectUserID != nil && *event.SubjectUserID == f.users[1] {
						visibleResult = true
					}
				}
				if !visibleResult {
					t.Fatal("其他玩家未看到公开的失效结果")
				}
				if leaving == "answer_approved" && f.view(1).Answer != "测试汤底" {
					t.Fatal("获准看底的玩家未看到汤底")
				}
				if res := f.action(2, "restore", "other_restore_01", map[string]any{"body": "另一位玩家的还原"}); res.Code != http.StatusOK {
					t.Fatalf("其他玩家无法继续还原：%d %s", res.Code, res.Body.String())
				}
				otherRestoreID := int64(0)
				for _, event := range f.view(0).Events {
					if event.Kind == "restore" && event.ID != restoreID {
						otherRestoreID = event.ID
					}
				}
				if otherRestoreID == 0 {
					t.Fatal("主持人未看到其他玩家还原")
				}
				if res := f.action(0, "judge", "other_judge_01", map[string]any{"eventId": otherRestoreID, "result": "正确"}); res.Code != http.StatusOK {
					t.Fatalf("主持人无法判定其他玩家：%d %s", res.Code, res.Body.String())
				}
				finished := f.view(0)
				if finished.Status != "finished" || (mode == "competitive" && (finished.WinnerID == nil || *finished.WinnerID != f.users[2])) || (mode == "casual" && finished.WinnerID != nil) {
					t.Fatalf("其他玩家获胜结果错误：status=%s winner=%v", finished.Status, finished.WinnerID)
				}
			})
		}
	}
	t.Run("legacy_unresolved", func(t *testing.T) {
		f := newStaleRestoreFixture(t, db, "competitive")
		if res := f.action(1, "restore", "legacy_restore_01", map[string]any{"body": "历史待判还原"}); res.Code != http.StatusOK {
			t.Fatalf("提交还原失败：%d %s", res.Code, res.Body.String())
		}
		restoreID := staleRestoreEventID(t, f.view(0), "restore")
		// 模拟修复前已留下的无回复旧事件。
		if _, err := db.Exec(ctx, `UPDATE room_members SET role='spectator' WHERE room_id=$1 AND user_id=$2`, f.roomID, f.users[1]); err != nil {
			t.Fatal(err)
		}
		for i, result := range []string{"正确", "错误", "不完整"} {
			operation := fmt.Sprintf("legacy_judge_%02d", i)
			if res := f.action(0, "judge", operation, map[string]any{"eventId": restoreID, "result": result}); res.Code != http.StatusConflict {
				t.Fatalf("旧还原仍可判为%s：%d %s", result, res.Code, res.Body.String())
			}
		}
		state := f.view(0)
		if state.Status != "active" || state.WinnerID != nil {
			t.Fatalf("旧还原错误结束对局：status=%s winner=%v", state.Status, state.WinnerID)
		}
		for _, event := range state.Events {
			if event.ParentID != nil && *event.ParentID == restoreID {
				t.Fatalf("拒绝判定后仍写入了结果：%+v", event)
			}
		}
	})
	t.Run("concurrent_retire_and_judge", func(t *testing.T) {
		f := newStaleRestoreFixture(t, db, "competitive")
		if res := f.action(1, "restore", "race_restore_01", map[string]any{"body": "同时退席和判定"}); res.Code != http.StatusOK {
			t.Fatalf("提交还原失败：%d %s", res.Code, res.Body.String())
		}
		restoreID := staleRestoreEventID(t, f.view(0), "restore")
		type outcome struct {
			kind string
			res  *httptest.ResponseRecorder
		}
		start := make(chan struct{})
		results := make(chan outcome, 2)
		go func() {
			<-start
			results <- outcome{"retire", f.action(1, "retire", "race_retire_01", nil)}
		}()
		go func() {
			<-start
			results <- outcome{"judge", f.action(0, "judge", "race_judge_01", map[string]any{"eventId": restoreID, "result": "正确"})}
		}()
		close(start)
		first, second := <-results, <-results
		codes := map[string]int{first.kind: first.res.Code, second.kind: second.res.Code}
		judgeWon := codes["judge"] == http.StatusOK && codes["retire"] == http.StatusConflict
		retireWon := codes["retire"] == http.StatusOK && codes["judge"] == http.StatusConflict
		if !judgeWon && !retireWon {
			t.Fatalf("并发操作未串行：retire=%d judge=%d", codes["retire"], codes["judge"])
		}
		state := f.view(0)
		if judgeWon && (state.Status != "finished" || state.WinnerID == nil || *state.WinnerID != f.users[1]) {
			t.Fatalf("先判定后退席的胜负错误：status=%s winner=%v", state.Status, state.WinnerID)
		}
		if retireWon && (state.Status != "active" || state.WinnerID != nil) {
			t.Fatalf("先退席后判定仍产生胜者：status=%s winner=%v", state.Status, state.WinnerID)
		}
		for _, member := range state.Members {
			if member.ID == f.users[1] && (member.Role != "spectator" || member.Correct != judgeWon) {
				t.Fatalf("并发后玩家身份错误：%+v", member)
			}
		}
		replies := 0
		for _, event := range state.Events {
			if event.ParentID != nil && *event.ParentID == restoreID {
				replies++
				if event.Kind != "restore_result" || (judgeWon && event.Result != "正确") || (retireWon && event.Result != "已失效") {
					t.Fatalf("并发后还原回复错误：%+v", event)
				}
			}
		}
		if replies != 1 {
			t.Fatalf("并发后同一还原出现 %d 条回复", replies)
		}
	})
}
