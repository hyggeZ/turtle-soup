package room_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

type taskItemView struct {
	ID        int64  `json:"id"`
	UserID    int64  `json:"userId"`
	Kind      string `json:"kind"`
	Body      string `json:"body"`
	CreatedAt string `json:"createdAt"`
}

type roomTasksView struct {
	PendingRestore bool           `json:"pendingRestore"`
	PendingAnswer  bool           `json:"pendingAnswer"`
	Items          []taskItemView `json:"items"`
	NextAfter      int64          `json:"nextAfter"`
}

type taskSnapshotView struct {
	Tasks         *roomTasksView    `json:"tasks"`
	Events        []json.RawMessage `json:"events"`
	HistoryBefore *int64            `json:"historyBefore"`
	EventCursor   int64             `json:"eventCursor"`
}

func getTaskSnapshot(t *testing.T, f *replayFixture, path string, who int) taskSnapshotView {
	t.Helper()
	res := f.call(http.MethodGet, path, nil, who)
	if res.Code != http.StatusOK {
		t.Fatalf("读取房间待办失败：%s => %d %s", path, res.Code, res.Body.String())
	}
	var view taskSnapshotView
	if err := json.Unmarshal(res.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	return view
}

func performTaskAction(t *testing.T, f *replayFixture, roomID int64, host int, kind string, eventID int64, operationID string) {
	t.Helper()
	result := "是"
	if kind == "judge" {
		result = "错误"
	} else if kind == "answer_decide" {
		result = "拒绝"
	}
	res := f.call(http.MethodPost, fmt.Sprintf("/api/rooms/%d/actions", roomID), map[string]any{
		"type": kind, "eventId": eventID, "result": result, "operationId": operationID,
	}, host)
	if res.Code != http.StatusOK {
		t.Fatalf("待办操作失败：%s/%d => %d %s", kind, eventID, res.Code, res.Body.String())
	}
}

func TestRoomTasksPageAndLegacySnapshot(t *testing.T) {
	f := newReplayFixture(t)
	host := f.addUser("task_page_host")
	player := f.addUser("task_page_player")
	f.addSoup(host)
	id := f.addRoom("44444444444444444444444444444444", "casual", "active", true, false, host)
	if _, err := f.db.Exec(f.ctx, `INSERT INTO room_members(room_id,user_id,role) VALUES($1,$2,'player')`, id, f.users[player]); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(f.ctx, `INSERT INTO room_events(room_id,user_id,kind,body)
		SELECT $1,$2,'question','待答问题 '||n FROM generate_series(1,51) AS n`, id, f.users[player]); err != nil {
		t.Fatal(err)
	}
	rows, err := f.db.Query(f.ctx, `SELECT id FROM room_events WHERE room_id=$1 ORDER BY id`, id)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, 0, 51)
	for rows.Next() {
		var eventID int64
		if err := rows.Scan(&eventID); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		ids = append(ids, eventID)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(ids) != 51 {
		t.Fatalf("测试事件预置失败：count=%d err=%v", len(ids), err)
	}
	path := fmt.Sprintf("/api/rooms/%d", id)
	legacy := getTaskSnapshot(t, f, path, host)
	if legacy.Tasks != nil || len(legacy.Events) != 51 {
		t.Fatalf("旧快照新增了待办或丢失历史：%+v", legacy)
	}
	first := getTaskSnapshot(t, f, path+"?tasks=1", host)
	if first.Tasks == nil || len(first.Tasks.Items) != 50 || first.Tasks.NextAfter != ids[49] || first.Tasks.Items[0].ID != ids[0] || first.Tasks.Items[49].ID != ids[49] {
		t.Fatalf("前 50 个待办或游标错误：%+v", first.Tasks)
	}
	second := getTaskSnapshot(t, f, fmt.Sprintf("%s?tasks=1&tasksAfter=%d", path, first.Tasks.NextAfter), host)
	if second.Tasks == nil || len(second.Tasks.Items) != 1 || second.Tasks.Items[0].ID != ids[50] || second.Tasks.NextAfter != 0 {
		t.Fatalf("第 51 个待办或结束游标错误：%+v", second.Tasks)
	}
	if _, err := f.db.Exec(f.ctx, `INSERT INTO room_events(room_id,user_id,kind,body) VALUES($1,$2,'question','新待答问题')`, id, f.users[player]); err != nil {
		t.Fatal(err)
	}
	second = getTaskSnapshot(t, f, fmt.Sprintf("%s?tasks=1&tasksAfter=%d", path, first.Tasks.NextAfter), host)
	if second.Tasks == nil || len(second.Tasks.Items) != 2 || second.Tasks.Items[0].ID != ids[50] || second.Tasks.NextAfter != 0 || second.Tasks.Items[1].ID <= ids[50] {
		t.Fatalf("新增待办后旧游标跳页或重复：%+v", second.Tasks)
	}
	latest := second.Tasks.Items[1].ID
	delta := getTaskSnapshot(t, f, fmt.Sprintf("%s?after=%d&tasks=1", path, latest), host)
	if len(delta.Events) != 0 || delta.Tasks == nil || len(delta.Tasks.Items) != 50 || delta.Tasks.Items[0].ID != ids[0] {
		t.Fatalf("旧增量为空时未保留早期待办：events=%d tasks=%+v", len(delta.Events), delta.Tasks)
	}
	activeHistory := getTaskSnapshot(t, f, path+"?history=1&tasks=1", host)
	if activeHistory.HistoryBefore != nil || activeHistory.Tasks == nil || len(activeHistory.Events) != 52 {
		t.Fatalf("进行中房间历史回退与待办投影不兼容：%+v", activeHistory)
	}
	performTaskAction(t, f, id, host, "answer", ids[0], "task_page_answer_01")
	afterAnswer := getTaskSnapshot(t, f, path+"?tasks=1", host)
	if afterAnswer.Tasks == nil || afterAnswer.Tasks.Items[0].ID != ids[1] {
		t.Fatalf("已回答问题仍在待办首位：%+v", afterAnswer.Tasks)
	}
	if ignored := getTaskSnapshot(t, f, path+"?tasksAfter=bad", host); ignored.Tasks != nil {
		t.Fatal("旧快照不再忽略未启用的 tasksAfter 参数")
	}
	for _, query := range []string{"?tasks=0", "?tasks=1&tasks=1", "?tasks=1&tasksAfter=", "?tasks=1&tasksAfter=-1", "?tasks=1&tasksAfter=bad", "?tasks=1&tasksAfter=9223372036854775808", "?tasks=1&tasksAfter=1&tasksAfter=2"} {
		res := f.call(http.MethodGet, path+query, nil, host)
		if res.Code != http.StatusBadRequest {
			t.Fatalf("无效待办参数未被拒绝：%s => %d %s", query, res.Code, res.Body.String())
		}
	}
}

func TestRoomTasksActionabilityAndPrivacy(t *testing.T) {
	f := newReplayFixture(t)
	host := f.addUser("task_rule_host")
	player := f.addUser("task_rule_player")
	former := f.addUser("task_rule_former")
	spectator := f.addUser("task_rule_spectator")
	f.addSoup(host)
	id := f.addRoom("55555555555555555555555555555555", "casual", "active", true, false, host)
	if _, err := f.db.Exec(f.ctx, `INSERT INTO room_members(room_id,user_id,role) VALUES($1,$2,'player'),($1,$3,'spectator'),($1,$4,'spectator')`, id, f.users[player], f.users[former], f.users[spectator]); err != nil {
		t.Fatal(err)
	}
	insert := func(user int, kind, body string) int64 {
		t.Helper()
		var eventID int64
		if err := f.db.QueryRow(f.ctx, `INSERT INTO room_events(room_id,user_id,kind,body) VALUES($1,$2,$3,$4) RETURNING id`, id, f.users[user], kind, body).Scan(&eventID); err != nil {
			t.Fatal(err)
		}
		return eventID
	}
	oldQuestion := insert(former, "question", "旧玩家的问题")
	oldRequest := insert(former, "answer_request", "")
	oldRestore := insert(former, "restore", "旧玩家的私人还原")
	newRestore := insert(player, "restore", "当前玩家的私人还原")
	newRequest := insert(player, "answer_request", "")
	path := fmt.Sprintf("/api/rooms/%d?tasks=1", id)
	hostView := getTaskSnapshot(t, f, path, host)
	if hostView.Tasks == nil || len(hostView.Tasks.Items) != 4 {
		t.Fatalf("主持待办数量错误：%+v", hostView.Tasks)
	}
	want := []int64{oldQuestion, oldRequest, newRestore, newRequest}
	for i, item := range hostView.Tasks.Items {
		if item.ID != want[i] || item.CreatedAt == "" {
			t.Fatalf("主持待办顺序或字段错误：%+v", hostView.Tasks.Items)
		}
		if item.ID == oldRestore {
			t.Fatal("旧玩家的还原仍可判定")
		}
	}
	playerView := getTaskSnapshot(t, f, path, player)
	if playerView.Tasks == nil || !playerView.Tasks.PendingRestore || !playerView.Tasks.PendingAnswer || len(playerView.Tasks.Items) != 0 || playerView.Tasks.NextAfter != 0 {
		t.Fatalf("玩家待办标记或列表错误：%+v", playerView.Tasks)
	}
	viewer := getTaskSnapshot(t, f, path, spectator)
	if viewer.Tasks == nil || viewer.Tasks.PendingRestore || viewer.Tasks.PendingAnswer || len(viewer.Tasks.Items) != 0 {
		t.Fatalf("旁观者拿到私人待办：%+v", viewer.Tasks)
	}
	res := f.call(http.MethodGet, path, nil, spectator)
	if strings.Contains(res.Body.String(), "当前玩家的私人还原") || strings.Contains(res.Body.String(), "旧玩家的私人还原") {
		t.Fatalf("旁观者快照泄露私人还原：%s", res.Body.String())
	}
	performTaskAction(t, f, id, host, "answer", oldQuestion, "task_rule_answer_01")
	performTaskAction(t, f, id, host, "answer_decide", oldRequest, "task_rule_decide_01")
	performTaskAction(t, f, id, host, "judge", newRestore, "task_rule_judge_01")
	performTaskAction(t, f, id, host, "answer_decide", newRequest, "task_rule_decide_02")
	hostView = getTaskSnapshot(t, f, path, host)
	if hostView.Tasks == nil || len(hostView.Tasks.Items) != 0 || hostView.Tasks.NextAfter != 0 {
		t.Fatalf("已处理事项仍在主持待办：%+v", hostView.Tasks)
	}
	playerView = getTaskSnapshot(t, f, path, player)
	if playerView.Tasks == nil || playerView.Tasks.PendingRestore || playerView.Tasks.PendingAnswer {
		t.Fatalf("已处理事项仍锁住玩家操作：%+v", playerView.Tasks)
	}
	if _, err := f.db.Exec(f.ctx, `UPDATE rooms SET status='paused',paused_at=NOW() WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	paused := getTaskSnapshot(t, f, path, host)
	if paused.Tasks == nil || len(paused.Tasks.Items) != 0 {
		t.Fatalf("暂停房间待办响应错误：%+v", paused.Tasks)
	}
	if _, err := f.db.Exec(f.ctx, `UPDATE rooms SET status='finished' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	for _, who := range []int{host, player, spectator} {
		finished := getTaskSnapshot(t, f, fmt.Sprintf("/api/rooms/%d?history=1&tasks=1", id), who)
		if finished.HistoryBefore == nil || finished.Tasks == nil || finished.Tasks.PendingRestore || finished.Tasks.PendingAnswer || len(finished.Tasks.Items) != 0 || finished.Tasks.NextAfter != 0 {
			t.Fatalf("已结束回看暴露实时待办：who=%d tasks=%+v", who, finished.Tasks)
		}
	}
}
