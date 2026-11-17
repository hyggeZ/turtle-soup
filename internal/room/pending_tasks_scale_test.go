package room_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
)

// TestPendingTasksScale 测量同一局增量快照附带独立待办后的实际成本。
func TestPendingTasksScale(t *testing.T) {
	if os.Getenv("PENDING_TASKS_SCALE") != "1" {
		t.Skip("设置 PENDING_TASKS_SCALE=1 后测量")
	}
	queries := &queryCounter{}
	f := newReplayFixtureWithCounter(t, queries)
	host := f.addUser("scale_host")
	player := f.addUser("scale_player")
	f.addSoup(host)
	for _, count := range []int{100, 1000, 5000} {
		id := f.addRoom(fmt.Sprintf("%032X", count), "casual", "active", true, false, host)
		if _, err := f.db.Exec(f.ctx, `INSERT INTO room_members(room_id,user_id,role) VALUES($1,$2,'player')`, id, f.users[player]); err != nil {
			t.Fatal(err)
		}
		// 先写仍待处理的三种事件，再写成对的问答作为后续历史。
		if _, err := f.db.Exec(f.ctx, `INSERT INTO room_events(room_id,user_id,kind,body)
			VALUES($1,$2,'question','早期问题'),($1,$2,'restore','待判还原'),($1,$2,'answer_request','')`, id, f.users[player]); err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.Exec(f.ctx, `WITH questions AS (
			INSERT INTO room_events(room_id,user_id,kind,body)
			SELECT $1,$2,'question','后来提问'||n FROM generate_series(1,$4::int) AS n RETURNING id
		) INSERT INTO room_events(room_id,user_id,kind,result,parent_id)
		SELECT $1,$3,'answer','是',id FROM questions`, id, f.users[player], f.users[host], count/2); err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.Exec(f.ctx, `ANALYZE room_events`); err != nil {
			t.Fatal(err)
		}
		var cursor int64
		if err := f.db.QueryRow(f.ctx, `SELECT max(id) FROM room_events WHERE room_id=$1`, id).Scan(&cursor); err != nil {
			t.Fatal(err)
		}
		path := fmt.Sprintf("/api/rooms/%d?after=%d", id, cursor)
		withTasks := path + "&tasks=1"
		checkScaleTasks(t, f, withTasks, host, player)
		measureSnapshotSamples(t, f, queries, count+3, host, []snapshotSample{{name: "host_delta", path: path}, {name: "host_tasks", path: withTasks}})
		measureSnapshotSamples(t, f, queries, count+3, player, []snapshotSample{{name: "player_delta", path: path}, {name: "player_tasks", path: withTasks}})
	}
}

func checkScaleTasks(t *testing.T, f *replayFixture, path string, host, player int) {
	t.Helper()
	for _, who := range []int{host, player} {
		response := f.call(http.MethodGet, path, nil, who)
		var result struct {
			Tasks *struct {
				Items          []json.RawMessage `json:"items"`
				PendingRestore bool              `json:"pendingRestore"`
				PendingAnswer  bool              `json:"pendingAnswer"`
			} `json:"tasks"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Tasks == nil {
			t.Fatalf("待办快照不可用：%d %s", response.Code, response.Body.String())
		}
		switch who {
		case host:
			if len(result.Tasks.Items) != 3 {
				t.Fatalf("主持人应看见三条待办，实际 %d 条", len(result.Tasks.Items))
			}
		case player:
			if len(result.Tasks.Items) != 0 || !result.Tasks.PendingRestore || !result.Tasks.PendingAnswer {
				t.Fatal("玩家只能获得自己的两个等待状态")
			}
		}
	}
}
