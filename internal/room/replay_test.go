package room_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
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
	"turtle-soup/internal/room"
	"turtle-soup/migrations"
)

type replayFixture struct {
	t      testing.TB
	ctx    context.Context
	db     *pgxpool.Pool
	router *gin.Engine
	users  []int64
	cookie []*http.Cookie
	soupID int64
}

func newReplayFixture(t testing.TB) *replayFixture {
	return newReplayFixtureWithCounter(t, nil)
}

func newReplayFixtureWithCounter(t testing.TB, counter *queryCounter) *replayFixture {
	t.Helper()
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
	schema := fmt.Sprintf("room_replay_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA "+schema+" CASCADE")
	})
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	if counter != nil {
		config.ConnConfig.Tracer = counter
	}
	db, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := migrations.Apply(ctx, db); err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	room.NewHandler(db, account.NewHandler(account.NewPGStore(db), false)).Register(router)
	return &replayFixture{t: t, ctx: ctx, db: db, router: router}
}

func (f *replayFixture) addUser(name string) int {
	f.t.Helper()
	var id int64
	if err := f.db.QueryRow(f.ctx, `INSERT INTO users(username,password_hash) VALUES($1,'test') RETURNING id`, name).Scan(&id); err != nil {
		f.t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{byte(len(f.users) + 1)}, 32))
	hash := sha256.Sum256([]byte(token))
	if _, err := f.db.Exec(f.ctx, `INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,NOW()+INTERVAL '1 hour')`, hash[:], id); err != nil {
		f.t.Fatal(err)
	}
	f.users = append(f.users, id)
	f.cookie = append(f.cookie, &http.Cookie{Name: "turtle_session", Value: token})
	return len(f.users) - 1
}

func (f *replayFixture) addSoup(author int) int64 {
	f.t.Helper()
	var id int64
	if err := f.db.QueryRow(f.ctx, `INSERT INTO soups(author_id,title,surface,answer,genre,difficulty,horror)
		VALUES($1,'回看测试题','回看汤面','回看绝密汤底','本格',2,1) RETURNING id`, f.users[author]).Scan(&id); err != nil {
		f.t.Fatal(err)
	}
	f.soupID = id
	return id
}

func (f *replayFixture) addRoom(code, mode, status string, public, published bool, host int) int64 {
	f.t.Helper()
	var id int64
	if err := f.db.QueryRow(f.ctx, `INSERT INTO rooms(code,owner_id,host_id,soup_id,mode,max_players,public,status,answer_published,
		soup_title,soup_surface,soup_answer,soup_genre,soup_difficulty,soup_horror)
		VALUES($1,$2,$2,$3,$4,2,$5,$6,$7,'回看测试题','回看汤面','回看绝密汤底','本格',2,1) RETURNING id`,
		code, f.users[host], f.soupID, mode, public, status, published).Scan(&id); err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.db.Exec(f.ctx, `INSERT INTO room_members(room_id,user_id,role,seen_answer) VALUES($1,$2,'host',TRUE)`, id, f.users[host]); err != nil {
		f.t.Fatal(err)
	}
	return id
}

func (f *replayFixture) call(method, path string, body any, who int) *httptest.ResponseRecorder {
	f.t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		f.t.Fatal(err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	if who >= 0 {
		req.AddCookie(f.cookie[who])
	}
	res := httptest.NewRecorder()
	f.router.ServeHTTP(res, req)
	return res
}

func TestFinishedPublicRoomReplayEntry(t *testing.T) {
	f := newReplayFixture(t)
	host := f.addUser("replay_host")
	visitor := f.addUser("replay_visitor")
	f.addSoup(host)
	code := "00112233445566778899AABBCCDDEEFF"
	id := f.addRoom(code, "casual", "finished", true, false, host)

	join := f.call(http.MethodPost, "/api/rooms/join", map[string]any{"code": code}, visitor)
	if join.Code != http.StatusOK {
		t.Errorf("已结束公开房间应允许新用户回看：%d %s", join.Code, join.Body.String())
	}
	replays := f.call(http.MethodGet, "/api/rooms/replays", nil, -1)
	if replays.Code != http.StatusOK {
		t.Errorf("公开回看入口不可用：%d %s", replays.Code, replays.Body.String())
	}
	if join.Code == http.StatusOK {
		view := f.call(http.MethodGet, fmt.Sprintf("/api/rooms/%d", id), nil, visitor)
		if view.Code != http.StatusOK {
			t.Errorf("回看成员无法读取快照：%d %s", view.Code, view.Body.String())
		}
	}
}

func TestFinishedReplayDiscoveryAndCodes(t *testing.T) {
	f := newReplayFixture(t)
	host := f.addUser("replay_host")
	visitor := f.addUser("replay_visitor")
	other := f.addUser("replay_other")
	f.addSoup(host)
	code := func(n int) string { return fmt.Sprintf("%032X", n) }
	publicCasual := f.addRoom(code(1), "casual", "finished", true, false, host)
	publicCompetitive := f.addRoom(code(2), "competitive", "finished", true, false, host)
	if _, err := f.db.Exec(f.ctx, `INSERT INTO room_members(room_id,user_id,role,correct) VALUES($1,$2,'spectator',TRUE)`, publicCompetitive, f.users[other]); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(f.ctx, `UPDATE rooms SET winner_id=$2 WHERE id=$1`, publicCompetitive, f.users[other]); err != nil {
		t.Fatal(err)
	}
	privateNew := f.addRoom(code(3), "casual", "finished", false, false, host)
	privateOld := f.addRoom("ABCDEF12", "competitive", "finished", false, false, host)
	if _, err := f.db.Exec(f.ctx, `INSERT INTO room_members(room_id,user_id,role) VALUES($1,$2,'spectator')`, privateOld, f.users[other]); err != nil {
		t.Fatal(err)
	}
	f.addRoom(code(4), "casual", "waiting", true, false, host)
	f.addRoom(code(5), "competitive", "active", true, false, host)
	f.addRoom(code(6), "casual", "closed", true, false, host)
	hiddenSoup := f.addSoup(host)
	hiddenRoom := f.addRoom(code(7), "casual", "finished", true, false, host)
	if _, err := f.db.Exec(f.ctx, `UPDATE soups SET hidden_at=NOW(),hidden_by=$2 WHERE id=$1`, hiddenSoup, f.users[host]); err != nil {
		t.Fatal(err)
	}
	deletedSoup := f.addSoup(host)
	deletedRoom := f.addRoom(code(8), "competitive", "finished", true, false, host)
	if _, err := f.db.Exec(f.ctx, `UPDATE soups SET deleted_at=NOW() WHERE id=$1`, deletedSoup); err != nil {
		t.Fatal(err)
	}

	replays := f.call(http.MethodGet, "/api/rooms/replays", nil, -1)
	if replays.Code != http.StatusOK {
		t.Fatalf("公开回看列表失败：%d %s", replays.Code, replays.Body.String())
	}
	var listed []struct {
		ID              int64  `json:"id"`
		Code            string `json:"code"`
		Mode            string `json:"mode"`
		Status          string `json:"status"`
		AnswerPublished bool   `json:"answerPublished"`
	}
	if err := json.Unmarshal(replays.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 || listed[0].ID != publicCompetitive || listed[1].ID != publicCasual {
		t.Fatalf("公开回看列表混入私密、未完成或下架房间：%+v", listed)
	}
	for _, item := range listed {
		if item.Status != "finished" || len(item.Code) != 32 || item.AnswerPublished || item.Mode != "casual" && item.Mode != "competitive" {
			t.Fatalf("公开回看摘要错误：%+v", item)
		}
	}
	for _, secret := range []string{"回看绝密汤底", "回看汤面", "events", "members"} {
		if strings.Contains(replays.Body.String(), secret) {
			t.Fatalf("公开回看列表泄露 %q", secret)
		}
	}
	lobby := f.call(http.MethodGet, "/api/rooms", nil, -1)
	if lobby.Code != http.StatusOK || strings.Contains(lobby.Body.String(), `"status":"finished"`) {
		t.Fatalf("招募大厅混入回看：%d %s", lobby.Code, lobby.Body.String())
	}

	for _, roomCode := range []string{code(1), code(2), code(3)} {
		joined := f.call(http.MethodPost, "/api/rooms/join", map[string]any{"code": roomCode}, visitor)
		if joined.Code != http.StatusOK {
			t.Fatalf("有效房间码未进入回看 %s：%d %s", roomCode, joined.Code, joined.Body.String())
		}
	}
	for _, roomID := range []int64{publicCasual, publicCompetitive, privateNew} {
		var role string
		if err := f.db.QueryRow(f.ctx, `SELECT role FROM room_members WHERE room_id=$1 AND user_id=$2`, roomID, f.users[visitor]).Scan(&role); err != nil || role != "spectator" {
			t.Fatalf("新访客未固定为旁观者：room=%d role=%q err=%v", roomID, role, err)
		}
	}
	var winner int64
	var status string
	if err := f.db.QueryRow(f.ctx, `SELECT winner_id,status FROM rooms WHERE id=$1`, publicCompetitive).Scan(&winner, &status); err != nil || winner != f.users[other] || status != "finished" {
		t.Fatalf("回看加入改变竞答结果：winner=%d status=%q err=%v", winner, status, err)
	}
	if res := f.call(http.MethodPost, "/api/rooms/join", map[string]any{"code": "ABCDEF12"}, visitor); res.Code != http.StatusNotFound {
		t.Fatalf("私密旧码向新访客暴露历史：%d %s", res.Code, res.Body.String())
	}
	if res := f.call(http.MethodPost, "/api/rooms/join", map[string]any{"code": "ABCDEF12"}, other); res.Code != http.StatusOK {
		t.Fatalf("私密旧码的原成员无法回看：%d %s", res.Code, res.Body.String())
	}
	for _, tc := range []struct {
		code string
		want int
	}{{code(6), http.StatusConflict}, {code(7), http.StatusNotFound}, {code(8), http.StatusNotFound}} {
		if res := f.call(http.MethodPost, "/api/rooms/join", map[string]any{"code": tc.code}, visitor); res.Code != tc.want {
			t.Fatalf("关闭或下架房间仍可新入场：code=%s status=%d body=%s", tc.code, res.Code, res.Body.String())
		}
	}
	if res := f.call(http.MethodGet, fmt.Sprintf("/api/rooms/%d", hiddenRoom), nil, host); res.Code != http.StatusOK {
		t.Fatalf("隐藏题目的原成员历史不可读：%d %s", res.Code, res.Body.String())
	}
	if res := f.call(http.MethodGet, fmt.Sprintf("/api/rooms/%d", deletedRoom), nil, host); res.Code != http.StatusOK {
		t.Fatalf("删除题目的原成员历史不可读：%d %s", res.Code, res.Body.String())
	}
	if res := f.call(http.MethodGet, fmt.Sprintf("/api/rooms/%d", publicCasual), nil, other); res.Code != http.StatusNotFound {
		t.Fatalf("非成员凭公开房间 ID 直读快照：%d %s", res.Code, res.Body.String())
	}
	if res := f.call(http.MethodGet, fmt.Sprintf("/api/rooms/%d/ws", publicCasual), nil, other); res.Code != http.StatusForbidden {
		t.Fatalf("非成员凭公开房间 ID 连接实时流：%d %s", res.Code, res.Body.String())
	}
	if res := f.call(http.MethodPost, "/api/rooms/join", map[string]any{"code": code(1)}, visitor); res.Code != http.StatusOK {
		t.Fatalf("重复回看加入不幂等：%d %s", res.Code, res.Body.String())
	}
	var members, players int
	if err := f.db.QueryRow(f.ctx, `SELECT count(*),count(*) FILTER (WHERE role='player') FROM room_members WHERE room_id=$1 AND user_id=$2`, publicCasual, f.users[visitor]).Scan(&members, &players); err != nil || members != 1 || players != 0 {
		t.Fatalf("回看占用玩家席位或重复成员：members=%d players=%d err=%v", members, players, err)
	}
	for i, kind := range []string{"question", "restore", "answer", "judge", "publish_answer"} {
		res := f.call(http.MethodPost, fmt.Sprintf("/api/rooms/%d/actions", publicCasual), map[string]any{
			"type": kind, "body": "测试", "result": "正确", "eventId": 1, "operationId": fmt.Sprintf("replay_action_%02d", i),
		}, visitor)
		if res.Code != http.StatusConflict {
			t.Fatalf("回看者仍能执行 %s：%d %s", kind, res.Code, res.Body.String())
		}
	}
}

func TestFinishedReplayCursorAcrossInsert(t *testing.T) {
	f := newReplayFixture(t)
	host := f.addUser("replay_host")
	f.addSoup(host)
	for n := 1; n <= 51; n++ {
		f.addRoom(fmt.Sprintf("%032X", n), "casual", "finished", true, false, host)
	}
	read := func(path string) []struct {
		ID int64 `json:"id"`
	} {
		t.Helper()
		res := f.call(http.MethodGet, path, nil, -1)
		if res.Code != http.StatusOK {
			t.Fatalf("回看列表分页失败：%d %s", res.Code, res.Body.String())
		}
		var items []struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal(res.Body.Bytes(), &items); err != nil {
			t.Fatal(err)
		}
		return items
	}
	first := read("/api/rooms/replays")
	if len(first) != 50 || first[0].ID <= first[49].ID {
		t.Fatalf("回看第一页顺序或数量错误：%+v", first)
	}
	newID := f.addRoom(fmt.Sprintf("%032X", 52), "competitive", "finished", true, false, host)
	second := read(fmt.Sprintf("/api/rooms/replays?before=%d", first[49].ID))
	if len(second) != 1 || second[0].ID >= first[49].ID {
		t.Fatalf("新房间插入扰动回看第二页：%+v", second)
	}
	if refreshed := read("/api/rooms/replays"); len(refreshed) != 50 || refreshed[0].ID != newID {
		t.Fatalf("回看刷新未显示新房间：%+v", refreshed)
	}
	for _, before := range []string{"0", "-1", "bad"} {
		res := f.call(http.MethodGet, "/api/rooms/replays?before="+before, nil, -1)
		if res.Code != http.StatusBadRequest {
			t.Fatalf("无效回看游标未被拒绝：%q => %d", before, res.Code)
		}
	}
}

func TestFinishedReplaySnapshotPrivacy(t *testing.T) {
	f := newReplayFixture(t)
	host := f.addUser("replay_host")
	player := f.addUser("replay_player")
	visitor := f.addUser("replay_visitor")
	f.addSoup(host)
	code := "00112233445566778899AABBCCDDEEFF"
	id := f.addRoom(code, "casual", "finished", true, false, host)
	if _, err := f.db.Exec(f.ctx, `INSERT INTO room_members(room_id,user_id,role) VALUES($1,$2,'spectator')`, id, f.users[player]); err != nil {
		t.Fatal(err)
	}
	var question, restore, answerRequest int64
	if err := f.db.QueryRow(f.ctx, `INSERT INTO room_events(room_id,user_id,kind,body) VALUES($1,$2,'question','公开问题') RETURNING id`, id, f.users[player]).Scan(&question); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(f.ctx, `INSERT INTO room_events(room_id,user_id,kind,result,parent_id) VALUES($1,$2,'answer','是',$3)`, id, f.users[host], question); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(f.ctx, `INSERT INTO room_events(room_id,user_id,kind,body) VALUES($1,$2,'question','未答私有问题')`, id, f.users[player]); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(f.ctx, `INSERT INTO room_events(room_id,user_id,kind,body) VALUES($1,$2,'restore','私人还原正文') RETURNING id`, id, f.users[player]).Scan(&restore); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(f.ctx, `INSERT INTO room_events(room_id,user_id,kind,result,parent_id) VALUES($1,$2,'restore_result','正确',$3)`, id, f.users[host], restore); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(f.ctx, `INSERT INTO room_events(room_id,user_id,kind) VALUES($1,$2,'answer_request') RETURNING id`, id, f.users[player]).Scan(&answerRequest); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(f.ctx, `INSERT INTO room_events(room_id,user_id,kind,result,parent_id) VALUES($1,$2,'answer_result','拒绝',$3)`, id, f.users[host], answerRequest); err != nil {
		t.Fatal(err)
	}
	if res := f.call(http.MethodPost, "/api/rooms/join", map[string]any{"code": code}, visitor); res.Code != http.StatusOK {
		t.Fatalf("回看入场失败：%d %s", res.Code, res.Body.String())
	}
	for _, path := range []string{fmt.Sprintf("/api/rooms/%d", id), fmt.Sprintf("/api/rooms/%d?after=%d", id, question)} {
		res := f.call(http.MethodGet, path, nil, visitor)
		if res.Code != http.StatusOK {
			t.Fatalf("回看快照失败：%d %s", res.Code, res.Body.String())
		}
		body := res.Body.String()
		for _, marker := range []string{"回看绝密汤底", "私人还原正文", "未答私有问题", `"kind":"restore"`, `"kind":"answer_request"`, `"kind":"answer_result"`} {
			if strings.Contains(body, marker) {
				t.Fatalf("回看快照泄露 %q：%s", marker, body)
			}
		}
		if !strings.Contains(body, "公开问题") || !strings.Contains(body, `"kind":"restore_result"`) || !strings.Contains(body, `"myRole":"spectator"`) || !strings.Contains(body, `"answer":""`) {
			t.Fatalf("公开回看内容或身份错误：%s", body)
		}
	}
	var revealed bool
	if err := f.db.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM soup_reveals WHERE soup_id=$1 AND user_id=$2)`, f.soupID, f.users[visitor]).Scan(&revealed); err != nil || revealed {
		t.Fatalf("未公开汤底的回看误记为已知汤底：revealed=%t err=%v", revealed, err)
	}
	ownerView := f.call(http.MethodGet, fmt.Sprintf("/api/rooms/%d", id), nil, player)
	if ownerView.Code != http.StatusOK || !strings.Contains(ownerView.Body.String(), "私人还原正文") {
		t.Fatalf("原提交者看不到自己的还原：%d %s", ownerView.Code, ownerView.Body.String())
	}
}

func TestFinishedReplayPublishedAnswerQualification(t *testing.T) {
	f := newReplayFixture(t)
	host := f.addUser("replay_host")
	roomPlayer := f.addUser("replay_room_player")
	aiPlayer := f.addUser("replay_ai_player")
	f.addSoup(host)
	code := "00112233445566778899AABBCCDDEEFF"
	id := f.addRoom(code, "competitive", "finished", true, false, host)
	otherRoom := f.addRoom("11112222333344445555666677778888", "casual", "active", false, false, host)
	if _, err := f.db.Exec(f.ctx, `INSERT INTO room_members(room_id,user_id,role) VALUES($1,$2,'player')`, otherRoom, f.users[roomPlayer]); err != nil {
		t.Fatal(err)
	}
	var gameID int64
	if err := f.db.QueryRow(f.ctx, `INSERT INTO ai_games(user_id,soup_id,title,surface,answer,status)
		VALUES($1,$2,'回看测试题','回看汤面','回看绝密汤底','active') RETURNING id`, f.users[aiPlayer], f.soupID).Scan(&gameID); err != nil {
		t.Fatal(err)
	}
	publish := f.call(http.MethodPost, fmt.Sprintf("/api/rooms/%d/actions", id), map[string]any{
		"type": "publish_answer", "operationId": "replay_publish_01",
	}, host)
	if publish.Code != http.StatusOK {
		t.Fatalf("主持公开汤底失败：%d %s", publish.Code, publish.Body.String())
	}
	listed := f.call(http.MethodGet, "/api/rooms/replays", nil, -1)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"answerPublished":true`) || strings.Contains(listed.Body.String(), "回看绝密汤底") {
		t.Fatalf("已公开回看摘要错误：%d %s", listed.Code, listed.Body.String())
	}
	for _, who := range []int{roomPlayer, aiPlayer} {
		res := f.call(http.MethodPost, "/api/rooms/join", map[string]any{"code": code}, who)
		if res.Code != http.StatusConflict || strings.Contains(res.Body.String(), "回看绝密汤底") {
			t.Fatalf("同题在玩者读到公开汤底：user=%d status=%d body=%s", who, res.Code, res.Body.String())
		}
		var member, revealed bool
		if err := f.db.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM room_members WHERE room_id=$1 AND user_id=$2),
			EXISTS(SELECT 1 FROM soup_reveals WHERE soup_id=$3 AND user_id=$2)`, id, f.users[who], f.soupID).Scan(&member, &revealed); err != nil || member || revealed {
			t.Fatalf("被拒用户留下成员或揭示记录：member=%t revealed=%t err=%v", member, revealed, err)
		}
	}
	if _, err := f.db.Exec(f.ctx, `UPDATE rooms SET status='finished' WHERE id=$1`, otherRoom); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(f.ctx, `UPDATE ai_games SET status='finished',finished_at=NOW() WHERE id=$1`, gameID); err != nil {
		t.Fatal(err)
	}
	for _, who := range []int{roomPlayer, aiPlayer} {
		res := f.call(http.MethodPost, "/api/rooms/join", map[string]any{"code": code}, who)
		if res.Code != http.StatusOK {
			t.Fatalf("同题游戏结束后仍无法回看：user=%d status=%d body=%s", who, res.Code, res.Body.String())
		}
		var role string
		var seen, revealed bool
		if err := f.db.QueryRow(f.ctx, `SELECT m.role,m.seen_answer,
			EXISTS(SELECT 1 FROM soup_reveals WHERE soup_id=$3 AND user_id=$2)
			FROM room_members m WHERE m.room_id=$1 AND m.user_id=$2`, id, f.users[who], f.soupID).Scan(&role, &seen, &revealed); err != nil || role != "spectator" || !seen || !revealed {
			t.Fatalf("看底未记录旁观与揭示：role=%q seen=%t revealed=%t err=%v", role, seen, revealed, err)
		}
		view := f.call(http.MethodGet, fmt.Sprintf("/api/rooms/%d", id), nil, who)
		if view.Code != http.StatusOK || !strings.Contains(view.Body.String(), "回看绝密汤底") {
			t.Fatalf("已公开汤底不可见：%d %s", view.Code, view.Body.String())
		}
		if _, err := agent.NewStore(f.db).Create(f.ctx, f.users[who], f.soupID); !errors.Is(err, agent.ErrKnownAnswer) {
			t.Fatalf("看底后仍能开始同题 AI 局：%v", err)
		}
	}
	waitingCode := "99992222333344445555666677778888"
	waitingRoom := f.addRoom(waitingCode, "casual", "waiting", false, false, host)
	for _, who := range []int{roomPlayer, aiPlayer} {
		res := f.call(http.MethodPost, "/api/rooms/join", map[string]any{"code": waitingCode}, who)
		if res.Code != http.StatusOK {
			t.Fatalf("已知汤底用户不能旁观同题房间：%d %s", res.Code, res.Body.String())
		}
		var role string
		if err := f.db.QueryRow(f.ctx, `SELECT role FROM room_members WHERE room_id=$1 AND user_id=$2`, waitingRoom, f.users[who]).Scan(&role); err != nil || role != "spectator" {
			t.Fatalf("回看汤底后仍作为同题玩家入场：role=%q err=%v", role, err)
		}
	}
}

func TestFinishedReplayJoinPublishRace(t *testing.T) {
	f := newReplayFixture(t)
	host := f.addUser("replay_host")
	visitor := f.addUser("replay_visitor")
	f.addSoup(host)
	code := "00112233445566778899AABBCCDDEEFF"
	id := f.addRoom(code, "casual", "finished", true, false, host)
	start := make(chan struct{})
	results := make(chan *httptest.ResponseRecorder, 2)
	var work sync.WaitGroup
	work.Add(2)
	go func() {
		defer work.Done()
		<-start
		results <- f.call(http.MethodPost, "/api/rooms/join", map[string]any{"code": code}, visitor)
	}()
	go func() {
		defer work.Done()
		<-start
		results <- f.call(http.MethodPost, fmt.Sprintf("/api/rooms/%d/actions", id), map[string]any{
			"type": "publish_answer", "operationId": "replay_publish_01",
		}, host)
	}()
	close(start)
	work.Wait()
	close(results)
	for result := range results {
		if result.Code != http.StatusOK {
			t.Fatalf("加入与公开汤底竞态失败：%d %s", result.Code, result.Body.String())
		}
	}
	var published, revealed bool
	if err := f.db.QueryRow(f.ctx, `SELECT answer_published,
		EXISTS(SELECT 1 FROM soup_reveals WHERE soup_id=$2 AND user_id=$3)
		FROM rooms WHERE id=$1`, id, f.soupID, f.users[visitor]).Scan(&published, &revealed); err != nil || !published || !revealed {
		t.Fatalf("加入与公开竞态后未记录看底：published=%t revealed=%t err=%v", published, revealed, err)
	}
}

func TestFinishedReplayJoinAIRace(t *testing.T) {
	f := newReplayFixture(t)
	host := f.addUser("replay_host")
	visitor := f.addUser("replay_visitor")
	f.addSoup(host)
	code := "00112233445566778899AABBCCDDEEFF"
	id := f.addRoom(code, "competitive", "finished", true, true, host)
	start := make(chan struct{})
	type outcome struct {
		kind string
		code int
		err  error
	}
	results := make(chan outcome, 2)
	go func() {
		<-start
		res := f.call(http.MethodPost, "/api/rooms/join", map[string]any{"code": code}, visitor)
		results <- outcome{kind: "join", code: res.Code}
	}()
	go func() {
		<-start
		_, err := agent.NewStore(f.db).Create(f.ctx, f.users[visitor], f.soupID)
		results <- outcome{kind: "ai", err: err}
	}()
	close(start)
	first, second := <-results, <-results
	var joined, aiCreated bool
	for _, result := range []outcome{first, second} {
		if result.kind == "join" {
			joined = result.code == http.StatusOK
			if !joined && result.code != http.StatusConflict {
				t.Fatalf("回看入场竞态返回异常状态：%d", result.code)
			}
		} else {
			aiCreated = result.err == nil
			if !aiCreated && !errors.Is(result.err, agent.ErrKnownAnswer) {
				t.Fatalf("AI 开局竞态返回异常错误：%v", result.err)
			}
		}
	}
	if joined == aiCreated {
		t.Fatalf("看底回看与同题 AI 局同时成功或同时失败：join=%t ai=%t", joined, aiCreated)
	}
	var member, revealed, activeAI bool
	if err := f.db.QueryRow(f.ctx, `SELECT
		EXISTS(SELECT 1 FROM room_members WHERE room_id=$1 AND user_id=$2),
		EXISTS(SELECT 1 FROM soup_reveals WHERE soup_id=$3 AND user_id=$2),
		EXISTS(SELECT 1 FROM ai_games WHERE soup_id=$3 AND user_id=$2 AND status='active')`, id, f.users[visitor], f.soupID).Scan(&member, &revealed, &activeAI); err != nil || member != joined || revealed != joined || activeAI != aiCreated {
		t.Fatalf("竞态持久状态不一致：member=%t revealed=%t activeAI=%t err=%v", member, revealed, activeAI, err)
	}
}
