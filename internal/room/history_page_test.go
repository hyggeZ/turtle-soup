package room_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"
)

type historyEvent struct {
	ID            int64  `json:"id"`
	Kind          string `json:"kind"`
	Body          string `json:"body"`
	Result        string `json:"result"`
	ParentID      *int64 `json:"parentId"`
	SubjectUserID *int64 `json:"subjectUserId"`
}

type historyView struct {
	Events        []historyEvent `json:"events"`
	EventCursor   int64          `json:"eventCursor"`
	HistoryBefore *int64         `json:"historyBefore"`
	MyRole        string         `json:"myRole"`
	Answer        string         `json:"answer"`
	Status        string         `json:"status"`
}

func getHistoryView(t *testing.T, f *replayFixture, path string, who int) historyView {
	t.Helper()
	res := f.call(http.MethodGet, path, nil, who)
	if res.Code != http.StatusOK {
		t.Fatalf("读取房间快照失败：%s => %d %s", path, res.Code, res.Body.String())
	}
	var view historyView
	if err := json.Unmarshal(res.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	return view
}

func collectHistory(t *testing.T, f *replayFixture, roomID int64, who int) []historyEvent {
	t.Helper()
	byID := make(map[int64]historyEvent)
	path := fmt.Sprintf("/api/rooms/%d?history=1", roomID)
	var previousBefore, previousCursor int64
	for page := 0; page < 20; page++ {
		view := getHistoryView(t, f, path, who)
		if view.HistoryBefore == nil {
			t.Fatal("已结束房间历史页缺少 historyBefore")
		}
		if page > 0 && view.EventCursor >= previousCursor {
			t.Fatalf("较早历史页的原始事件游标未递减：previous=%d current=%d", previousCursor, view.EventCursor)
		}
		previousCursor = view.EventCursor
		for i, event := range view.Events {
			if i > 0 && view.Events[i-1].ID >= event.ID {
				t.Fatalf("页面事件未按 ID 严格升序：%+v", view.Events)
			}
			if previous, ok := byID[event.ID]; ok && !reflect.DeepEqual(previous, event) {
				t.Fatalf("重复事件内容不一致：old=%+v new=%+v", previous, event)
			}
			byID[event.ID] = event
		}
		before := *view.HistoryBefore
		if before == 0 {
			ids := make([]int64, 0, len(byID))
			for id := range byID {
				ids = append(ids, id)
			}
			sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
			events := make([]historyEvent, 0, len(ids))
			for _, id := range ids {
				events = append(events, byID[id])
			}
			return events
		}
		if page > 0 && before >= previousBefore {
			t.Fatalf("历史游标未递减：previous=%d current=%d", previousBefore, before)
		}
		previousBefore = before
		path = fmt.Sprintf("/api/rooms/%d?history=1&before=%d", roomID, before)
	}
	t.Fatal("历史翻页未结束")
	return nil
}

func TestFinishedRoomHistoryAnswerParents(t *testing.T) {
	f := newReplayFixture(t)
	host := f.addUser("history_parent_host")
	player := f.addUser("history_parent_player")
	visitor := f.addUser("history_parent_visitor")
	f.addSoup(host)
	id := f.addRoom("11111111111111111111111111111111", "casual", "finished", true, false, host)
	if _, err := f.db.Exec(f.ctx, `INSERT INTO room_members(room_id,user_id,role) VALUES($1,$2,'spectator'),($1,$3,'spectator')`, id, f.users[player], f.users[visitor]); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(f.ctx, `INSERT INTO room_events(room_id,user_id,kind,body)
		SELECT $1,$2,'question','问题 '||n FROM generate_series(1,50) AS n`, id, f.users[player]); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(f.ctx, `INSERT INTO room_events(room_id,user_id,kind,result,parent_id)
		SELECT $1,$2,'answer','是',id FROM room_events WHERE room_id=$1 AND kind='question' ORDER BY id`, id, f.users[host]); err != nil {
		t.Fatal(err)
	}
	var firstAnswer, lastEvent int64
	if err := f.db.QueryRow(f.ctx, `SELECT MIN(id) FILTER (WHERE kind='answer'),MAX(id) FROM room_events WHERE room_id=$1`, id).Scan(&firstAnswer, &lastEvent); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/rooms/%d", id)
	latest := getHistoryView(t, f, path+"?history=1", visitor)
	if latest.HistoryBefore == nil || *latest.HistoryBefore != firstAnswer || latest.EventCursor != lastEvent || len(latest.Events) != 100 {
		t.Fatalf("最近 50 条回答没有有界补齐问题：before=%v cursor=%d events=%d", latest.HistoryBefore, latest.EventCursor, len(latest.Events))
	}
	older := getHistoryView(t, f, fmt.Sprintf("%s?history=1&before=%d", path, *latest.HistoryBefore), visitor)
	if older.HistoryBefore == nil || *older.HistoryBefore != 0 || older.EventCursor >= firstAnswer || len(older.Events) != 50 {
		t.Fatalf("较早页面或高水位错误：before=%v cursor=%d events=%d", older.HistoryBefore, older.EventCursor, len(older.Events))
	}
	for _, who := range []int{host, visitor} {
		legacy := getHistoryView(t, f, path, who)
		if legacy.HistoryBefore != nil || len(legacy.Events) != 100 {
			t.Fatalf("旧快照契约改变：role=%d events=%d before=%v", who, len(legacy.Events), legacy.HistoryBefore)
		}
		if got := collectHistory(t, f, id, who); !reflect.DeepEqual(got, legacy.Events) {
			t.Fatalf("分页合并与旧快照不一致：role=%d got=%d want=%d", who, len(got), len(legacy.Events))
		}
	}
}

func TestFinishedRoomHistoryFilteredEmptyPages(t *testing.T) {
	f := newReplayFixture(t)
	host := f.addUser("history_private_host")
	player := f.addUser("history_private_player")
	visitor := f.addUser("history_private_visitor")
	f.addSoup(host)
	id := f.addRoom("22222222222222222222222222222222", "casual", "finished", true, false, host)
	if _, err := f.db.Exec(f.ctx, `INSERT INTO room_members(room_id,user_id,role) VALUES($1,$2,'spectator'),($1,$3,'spectator')`, id, f.users[player], f.users[visitor]); err != nil {
		t.Fatal(err)
	}
	var question, restore, request int64
	if err := f.db.QueryRow(f.ctx, `INSERT INTO room_events(room_id,user_id,kind,body) VALUES($1,$2,'question','公开问题') RETURNING id`, id, f.users[player]).Scan(&question); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(f.ctx, `INSERT INTO room_events(room_id,user_id,kind,result,parent_id) VALUES($1,$2,'answer','是',$3)`, id, f.users[host], question); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(f.ctx, `INSERT INTO room_events(room_id,user_id,kind,body) VALUES($1,$2,'restore','私人还原正文') RETURNING id`, id, f.users[player]).Scan(&restore); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(f.ctx, `INSERT INTO room_events(room_id,user_id,kind,result,parent_id) VALUES($1,$2,'restore_result','错误',$3)`, id, f.users[host], restore); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(f.ctx, `INSERT INTO room_events(room_id,user_id,kind) VALUES($1,$2,'answer_request') RETURNING id`, id, f.users[player]).Scan(&request); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(f.ctx, `INSERT INTO room_events(room_id,user_id,kind,result,parent_id) VALUES($1,$2,'answer_result','拒绝',$3)`, id, f.users[host], request); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(f.ctx, `INSERT INTO room_events(room_id,user_id,kind,body)
		SELECT $1,$2,'question','未答私有问题' FROM generate_series(1,101)`, id, f.users[player]); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/rooms/%d", id)
	first := getHistoryView(t, f, path+"?history=1", visitor)
	if len(first.Events) != 0 || first.HistoryBefore == nil || *first.HistoryBefore == 0 || first.Answer != "" {
		t.Fatalf("首个私有原始页应过滤后为空但继续翻页：%+v", first)
	}
	second := getHistoryView(t, f, fmt.Sprintf("%s?history=1&before=%d", path, *first.HistoryBefore), visitor)
	if len(second.Events) != 0 || second.HistoryBefore == nil || *second.HistoryBefore == 0 || second.EventCursor >= first.EventCursor {
		t.Fatalf("第二个私有原始页未正确前进：%+v", second)
	}
	visitorLegacy := getHistoryView(t, f, path, visitor)
	if got := collectHistory(t, f, id, visitor); !reflect.DeepEqual(got, visitorLegacy.Events) || len(got) != 3 {
		t.Fatalf("旁观者分页与旧权限视图不一致：got=%+v want=%+v", got, visitorLegacy.Events)
	}
	for _, who := range []int{host, player} {
		legacy := getHistoryView(t, f, path, who)
		if got := collectHistory(t, f, id, who); !reflect.DeepEqual(got, legacy.Events) {
			t.Fatalf("角色 %d 分页与旧权限视图不一致：got=%d want=%d", who, len(got), len(legacy.Events))
		}
	}
	if !strings.Contains(getHistoryView(t, f, path+"?history=1", host).Answer, "回看绝密汤底") {
		t.Fatal("主持人分页快照缺少汤底")
	}
}

func TestRoomHistoryInputAndActiveFallback(t *testing.T) {
	f := newReplayFixture(t)
	host := f.addUser("history_active_host")
	f.addSoup(host)
	id := f.addRoom("33333333333333333333333333333333", "casual", "active", true, false, host)
	if _, err := f.db.Exec(f.ctx, `INSERT INTO room_events(room_id,user_id,kind,body)
		SELECT $1,$2,'question','进行中的问题' FROM generate_series(1,51)`, id, f.users[host]); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/rooms/%d", id)
	legacy := getHistoryView(t, f, path, host)
	active := getHistoryView(t, f, path+"?history=1", host)
	if active.HistoryBefore != nil || len(active.Events) != 51 || !reflect.DeepEqual(active.Events, legacy.Events) {
		t.Fatalf("进行中房间未回退到完整快照：before=%v events=%d", active.HistoryBefore, len(active.Events))
	}
	if res := f.call(http.MethodGet, path+"?history=1&before=1", nil, host); res.Code != http.StatusConflict {
		t.Fatalf("进行中房间允许历史翻页：%d %s", res.Code, res.Body.String())
	}
	for _, query := range []string{"?history=0", "?history=1&after=0", "?history=1&before=", "?history=1&before=0", "?history=1&before=-1", "?history=1&before=bad", "?history=1&before=9223372036854775808", "?history=1&history=1"} {
		res := f.call(http.MethodGet, path+query, nil, host)
		if res.Code != http.StatusBadRequest {
			t.Fatalf("无效历史参数未被拒绝：%s => %d %s", query, res.Code, res.Body.String())
		}
	}
	ignoredBefore := getHistoryView(t, f, path+"?before=bad", host)
	if !reflect.DeepEqual(ignoredBefore.Events, legacy.Events) || ignoredBefore.HistoryBefore != nil {
		t.Fatal("旧快照不再忽略未知 before 参数")
	}
	last := legacy.EventCursor
	legacyDelta := getHistoryView(t, f, fmt.Sprintf("%s?after=%d", path, last), host)
	if legacyDelta.EventCursor != last || len(legacyDelta.Events) != 0 || legacyDelta.HistoryBefore != nil {
		t.Fatalf("旧增量契约改变：%+v", legacyDelta)
	}
}
