package room_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"testing"
)

type historyV2View struct {
	Events        []historyEvent `json:"events"`
	EventCursor   int64          `json:"eventCursor"`
	HistoryBefore *int64         `json:"historyBefore"`
	HasMoreNew    *bool          `json:"hasMoreNew"`
	Tasks         *roomTasksView `json:"tasks"`
}

func getHistoryV2(t *testing.T, f *replayFixture, path string, who int) historyV2View {
	t.Helper()
	res := f.call(http.MethodGet, path, nil, who)
	if res.Code != http.StatusOK {
		t.Fatalf("读取新版快照失败：%s => %d %s", path, res.Code, res.Body.String())
	}
	var view historyV2View
	if err := json.Unmarshal(res.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	return view
}

func mergeHistoryV2(pages ...[]historyEvent) []historyEvent {
	byID := make(map[int64]historyEvent)
	for _, page := range pages {
		for _, event := range page {
			byID[event.ID] = event
		}
	}
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

func TestRoomHistoryV2ActiveAndCompatibility(t *testing.T) {
	f := newReplayFixture(t)
	host := f.addUser("v2_active_host")
	player := f.addUser("v2_active_player")
	f.addSoup(host)
	id := f.addRoom("66666666666666666666666666666666", "casual", "active", true, false, host)
	if _, err := f.db.Exec(f.ctx, `INSERT INTO room_members(room_id,user_id,role) VALUES($1,$2,'player')`, id, f.users[player]); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(f.ctx, `INSERT INTO room_events(room_id,user_id,kind,body)
		SELECT $1,$2,'question','问题 '||n FROM generate_series(1,70) AS n`, id, f.users[player]); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/rooms/%d", id)
	legacy := getHistoryV2(t, f, path, host)
	legacyZero := getHistoryV2(t, f, path+"?after=0", host)
	v1 := getHistoryV2(t, f, path+"?history=1", host)
	if len(legacy.Events) != 70 || legacy.HistoryBefore != nil || legacy.HasMoreNew != nil ||
		!reflect.DeepEqual(legacy.Events, legacyZero.Events) || !reflect.DeepEqual(legacy.Events, v1.Events) ||
		v1.HistoryBefore != nil || v1.HasMoreNew != nil {
		t.Fatalf("旧版与 history=1 活跃快照契约改变：legacy=%d v1=%d", len(legacy.Events), len(v1.Events))
	}
	if res := f.call(http.MethodGet, path+"?history=1&before=1", nil, host); res.Code != http.StatusConflict {
		t.Fatalf("history=1 错误接受活跃旧页：%d", res.Code)
	}
	if res := f.call(http.MethodGet, path+"?history=1&after=0", nil, host); res.Code != http.StatusBadRequest {
		t.Fatalf("history=1 错误接受增量：%d", res.Code)
	}
	latest := getHistoryV2(t, f, path+"?history=2&tasks=1", host)
	if latest.HistoryBefore == nil || *latest.HistoryBefore <= 0 || latest.HasMoreNew != nil ||
		len(latest.Events) != 50 || latest.EventCursor != legacy.EventCursor ||
		latest.Tasks == nil || len(latest.Tasks.Items) != 50 {
		t.Fatalf("活跃最近页或主持待办错误：%+v", latest)
	}
	older := getHistoryV2(t, f, fmt.Sprintf("%s?history=2&before=%d", path, *latest.HistoryBefore), host)
	if older.HistoryBefore == nil || *older.HistoryBefore != 0 || older.HasMoreNew != nil || len(older.Events) != 20 ||
		older.EventCursor >= latest.EventCursor || !reflect.DeepEqual(mergeHistoryV2(latest.Events, older.Events), legacy.Events) {
		t.Fatalf("活跃旧页未与完整历史一致：%+v", older)
	}
	first := getHistoryV2(t, f, path+"?history=2&after=0", host)
	if first.HistoryBefore != nil || first.HasMoreNew == nil || !*first.HasMoreNew || len(first.Events) != 50 ||
		first.EventCursor != first.Events[49].ID || first.Events[0].ID != legacy.Events[0].ID {
		t.Fatalf("after=0 没有读取最早 50 条新原始事件：%+v", first)
	}
	second := getHistoryV2(t, f, fmt.Sprintf("%s?history=2&after=%d", path, first.EventCursor), host)
	if second.HistoryBefore != nil || second.HasMoreNew == nil || *second.HasMoreNew || len(second.Events) != 20 ||
		second.EventCursor != legacy.EventCursor || !reflect.DeepEqual(mergeHistoryV2(first.Events, second.Events), legacy.Events) {
		t.Fatalf("新版增量没有完整追上：%+v", second)
	}
	empty := getHistoryV2(t, f, fmt.Sprintf("%s?history=2&after=%d", path, second.EventCursor), host)
	if len(empty.Events) != 0 || empty.HasMoreNew == nil || *empty.HasMoreNew || empty.EventCursor != second.EventCursor || empty.HistoryBefore != nil {
		t.Fatalf("空增量的游标或字段错误：%+v", empty)
	}
	if _, err := f.db.Exec(f.ctx, `UPDATE rooms SET status='paused',paused_at=NOW() WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	paused := getHistoryV2(t, f, path+"?history=2&tasks=1", host)
	if paused.HistoryBefore == nil || len(paused.Events) != 50 || paused.Tasks == nil || len(paused.Tasks.Items) != 50 {
		t.Fatalf("暂停房间没有继续分页或展示待办：%+v", paused)
	}
	if _, err := f.db.Exec(f.ctx, `UPDATE rooms SET status='finished' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	v1Finished := getHistoryV2(t, f, path+"?history=1", host)
	v2Finished := getHistoryV2(t, f, path+"?history=2&tasks=1", host)
	if v1Finished.HistoryBefore == nil || v2Finished.HistoryBefore == nil ||
		!reflect.DeepEqual(v1Finished.Events, v2Finished.Events) || v2Finished.Tasks == nil || len(v2Finished.Tasks.Items) != 0 {
		t.Fatalf("已结束房间 v1/v2 首屏或待办不一致：v1=%+v v2=%+v", v1Finished, v2Finished)
	}
	for _, query := range []string{"?history=2&after=", "?history=2&after=-1", "?history=2&after=bad", "?history=2&after=9223372036854775808", "?history=2&after=1&after=2", "?history=2&before=0", "?history=2&before=1&after=0", "?history=3"} {
		res := f.call(http.MethodGet, path+query, nil, host)
		if res.Code != http.StatusBadRequest {
			t.Fatalf("无效新版参数未被拒绝：%s => %d %s", query, res.Code, res.Body.String())
		}
	}
}

func TestRoomHistoryV2FilteredSpillAndAnswerParents(t *testing.T) {
	f := newReplayFixture(t)
	host := f.addUser("v2_spill_host")
	player := f.addUser("v2_spill_player")
	viewer := f.addUser("v2_spill_viewer")
	f.addSoup(host)
	id := f.addRoom("77777777777777777777777777777777", "casual", "active", true, false, host)
	if _, err := f.db.Exec(f.ctx, `INSERT INTO room_members(room_id,user_id,role) VALUES($1,$2,'player'),($1,$3,'spectator')`, id, f.users[player], f.users[viewer]); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(f.ctx, `INSERT INTO room_events(room_id,user_id,kind,body)
		SELECT $1,$2,'question','尚未回答的问题 '||n FROM generate_series(1,101) AS n`, id, f.users[player]); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/rooms/%d", id)
	first := getHistoryV2(t, f, path+"?history=2&after=0", viewer)
	if len(first.Events) != 0 || first.HasMoreNew == nil || !*first.HasMoreNew || first.EventCursor <= 0 || first.HistoryBefore != nil {
		t.Fatalf("首批全隐私事件未推动原始游标：%+v", first)
	}
	second := getHistoryV2(t, f, fmt.Sprintf("%s?history=2&after=%d", path, first.EventCursor), viewer)
	if len(second.Events) != 0 || second.HasMoreNew == nil || !*second.HasMoreNew || second.EventCursor <= first.EventCursor {
		t.Fatalf("第二批全隐私事件未推动原始游标：%+v", second)
	}
	third := getHistoryV2(t, f, fmt.Sprintf("%s?history=2&after=%d", path, second.EventCursor), viewer)
	if len(third.Events) != 0 || third.HasMoreNew == nil || *third.HasMoreNew || third.EventCursor <= second.EventCursor {
		t.Fatalf("最后一批全隐私事件未结束增量：%+v", third)
	}
	if _, err := f.db.Exec(f.ctx, `INSERT INTO room_events(room_id,user_id,kind,result,parent_id)
		SELECT $1,$2,'answer','是',id FROM room_events WHERE room_id=$1 AND kind='question' ORDER BY id LIMIT 51`, id, f.users[host]); err != nil {
		t.Fatal(err)
	}
	answerPage := getHistoryV2(t, f, fmt.Sprintf("%s?history=2&after=%d", path, third.EventCursor), viewer)
	if answerPage.HasMoreNew == nil || !*answerPage.HasMoreNew || len(answerPage.Events) != 100 || answerPage.EventCursor <= third.EventCursor || answerPage.HistoryBefore != nil {
		t.Fatalf("50 条回答未有界补回旧问题：%+v", answerPage)
	}
	lastAnswer := getHistoryV2(t, f, fmt.Sprintf("%s?history=2&after=%d", path, answerPage.EventCursor), viewer)
	if lastAnswer.HasMoreNew == nil || *lastAnswer.HasMoreNew || len(lastAnswer.Events) != 2 || lastAnswer.EventCursor <= answerPage.EventCursor {
		t.Fatalf("第 51 条回答或旧问题丢失：%+v", lastAnswer)
	}
	legacy := getHistoryV2(t, f, fmt.Sprintf("%s?after=%d", path, third.EventCursor), viewer)
	if !reflect.DeepEqual(mergeHistoryV2(answerPage.Events, lastAnswer.Events), legacy.Events) || len(legacy.Events) != 102 {
		t.Fatalf("新版有界增量与旧增量权限视图不一致：got=%d want=%d", len(mergeHistoryV2(answerPage.Events, lastAnswer.Events)), len(legacy.Events))
	}
	latest := getHistoryV2(t, f, path+"?history=2", viewer)
	if latest.HistoryBefore == nil || *latest.HistoryBefore <= 0 || latest.HasMoreNew != nil || len(latest.Events) != 100 ||
		latest.EventCursor != lastAnswer.EventCursor {
		t.Fatalf("最新页父问题或游标错误：%+v", latest)
	}
}

func TestRoomHistoryV2KeepsEarlyTasks(t *testing.T) {
	f := newReplayFixture(t)
	host := f.addUser("v2_task_host")
	player := f.addUser("v2_task_player")
	viewer := f.addUser("v2_task_viewer")
	f.addSoup(host)
	id := f.addRoom("88888888888888888888888888888888", "casual", "active", true, false, host)
	if _, err := f.db.Exec(f.ctx, `INSERT INTO room_members(room_id,user_id,role) VALUES($1,$2,'player'),($1,$3,'spectator')`, id, f.users[player], f.users[viewer]); err != nil {
		t.Fatal(err)
	}
	insert := func(kind, body string) int64 {
		t.Helper()
		var eventID int64
		if err := f.db.QueryRow(f.ctx, `INSERT INTO room_events(room_id,user_id,kind,body) VALUES($1,$2,$3,$4) RETURNING id`, id, f.users[player], kind, body).Scan(&eventID); err != nil {
			t.Fatal(err)
		}
		return eventID
	}
	question := insert("question", "最早的问题")
	restore := insert("restore", "最早的私人还原")
	request := insert("answer_request", "")
	if _, err := f.db.Exec(f.ctx, `INSERT INTO room_events(room_id,user_id,kind,body)
		SELECT $1,$2,'question','后来的问题' FROM generate_series(1,60)`, id, f.users[player]); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/rooms/%d", id)
	latest := getHistoryV2(t, f, path+"?history=2&tasks=1", host)
	if len(latest.Events) != 50 || latest.HistoryBefore == nil || latest.Tasks == nil || len(latest.Tasks.Items) != 50 ||
		latest.Tasks.Items[0].ID != question || latest.Tasks.Items[1].ID != restore || latest.Tasks.Items[2].ID != request {
		t.Fatalf("早期待办消失于最新历史页之外：%+v", latest)
	}
	current := getHistoryV2(t, f, fmt.Sprintf("%s?history=2&after=%d&tasks=1", path, latest.EventCursor), host)
	if len(current.Events) != 0 || current.HasMoreNew == nil || *current.HasMoreNew || current.Tasks == nil ||
		current.Tasks.Items[0].ID != question {
		t.Fatalf("空增量未携带早期主持待办：%+v", current)
	}
	playerView := getHistoryV2(t, f, path+"?history=2&tasks=1", player)
	if playerView.Tasks == nil || !playerView.Tasks.PendingRestore || !playerView.Tasks.PendingAnswer || len(playerView.Tasks.Items) != 0 {
		t.Fatalf("早期待办标记未与历史分离：%+v", playerView.Tasks)
	}
	viewerView := getHistoryV2(t, f, path+"?history=2&tasks=1", viewer)
	if viewerView.Tasks == nil || viewerView.Tasks.PendingRestore || viewerView.Tasks.PendingAnswer || len(viewerView.Tasks.Items) != 0 || len(viewerView.Events) != 0 {
		t.Fatalf("旁观者看到了未答问题或私有待办：%+v", viewerView)
	}
	performTaskAction(t, f, id, host, "answer", question, "v2_task_answer_01")
	performTaskAction(t, f, id, host, "judge", restore, "v2_task_judge_01")
	performTaskAction(t, f, id, host, "answer_decide", request, "v2_task_decide_01")
	current = getHistoryV2(t, f, path+"?history=2&tasks=1", host)
	for _, item := range current.Tasks.Items {
		if item.ID == question || item.ID == restore || item.ID == request {
			t.Fatalf("已完成的早期待办仍在列表：%+v", current.Tasks.Items)
		}
	}
	playerView = getHistoryV2(t, f, path+"?history=2&tasks=1", player)
	if playerView.Tasks == nil || playerView.Tasks.PendingRestore || playerView.Tasks.PendingAnswer {
		t.Fatalf("已处理的早期玩家待办仍存在：%+v", playerView.Tasks)
	}
}

func TestRoomHistoryV2WaitingClosedAndEmptyDelta(t *testing.T) {
	queries := &queryCounter{}
	f := newReplayFixtureWithCounter(t, queries)
	host := f.addUser("v2_other_status_host")
	f.addSoup(host)
	for _, item := range []struct {
		code   string
		status string
	}{
		{"99999999999999999999999999999999", "waiting"},
		{"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "closed"},
	} {
		id := f.addRoom(item.code, "casual", item.status, true, false, host)
		if _, err := f.db.Exec(f.ctx, `INSERT INTO room_events(room_id,user_id,kind,body)
			SELECT $1,$2,'question','旧记录' FROM generate_series(1,51)`, id, f.users[host]); err != nil {
			t.Fatal(err)
		}
		path := fmt.Sprintf("/api/rooms/%d", id)
		v1 := getHistoryV2(t, f, path+"?history=1", host)
		v2 := getHistoryV2(t, f, path+"?history=2", host)
		if len(v1.Events) != 51 || v1.HistoryBefore != nil || v1.HasMoreNew != nil ||
			len(v2.Events) != 50 || v2.HistoryBefore == nil || *v2.HistoryBefore == 0 || v2.HasMoreNew != nil {
			t.Fatalf("%s 房间历史版本不一致：v1=%+v v2=%+v", item.status, v1, v2)
		}
		older := getHistoryV2(t, f, fmt.Sprintf("%s?history=2&before=%d", path, *v2.HistoryBefore), host)
		if len(older.Events) != 1 || older.HistoryBefore == nil || *older.HistoryBefore != 0 {
			t.Fatalf("%s 房间旧页不可读：%+v", item.status, older)
		}
		if res := f.call(http.MethodGet, path+"?history=1&before=1", nil, host); res.Code != http.StatusConflict {
			t.Fatalf("history=1 错误允许 %s 房间旧页：%d", item.status, res.Code)
		}
		newPage := getHistoryV2(t, f, path+"?history=2&after=0", host)
		if len(newPage.Events) != 50 || newPage.HasMoreNew == nil || !*newPage.HasMoreNew || newPage.HistoryBefore != nil {
			t.Fatalf("%s 房间新版增量不一致：%+v", item.status, newPage)
		}
	}
	emptyID := f.addRoom("BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", "casual", "waiting", true, false, host)
	emptyPath := fmt.Sprintf("/api/rooms/%d", emptyID)
	queries.count.Store(0)
	empty := getHistoryV2(t, f, emptyPath+"?history=2&after=0", host)
	v2Queries := queries.count.Load()
	queries.count.Store(0)
	legacy := getHistoryV2(t, f, emptyPath+"?after=0", host)
	legacyQueries := queries.count.Load()
	if len(empty.Events) != 0 || empty.EventCursor != 0 || empty.HasMoreNew == nil || *empty.HasMoreNew || empty.HistoryBefore != nil ||
		len(legacy.Events) != 0 || legacy.HasMoreNew != nil || v2Queries > legacyQueries {
		t.Fatalf("空增量仍读取事件明细或旧版响应改变：v2=%+v legacy=%+v dbq=%d/%d", empty, legacy, v2Queries, legacyQueries)
	}
}
