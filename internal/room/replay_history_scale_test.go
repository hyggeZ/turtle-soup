package room_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"testing"
	"time"
)

// TestReplayHistoryScale 用同一份对局数据交替测量全量、最近页和中段页。
func TestReplayHistoryScale(t *testing.T) {
	if os.Getenv("REPLAY_HISTORY_SCALE") != "1" {
		t.Skip("设置 REPLAY_HISTORY_SCALE=1 后测量")
	}
	queries := &queryCounter{}
	f := newReplayFixtureWithCounter(t, queries)
	host := f.addUser("scale_host")
	player := f.addUser("scale_player")
	viewer := f.addUser("scale_viewer")
	f.addSoup(host)
	for _, count := range []int{100, 1000, 5000} {
		id := f.addRoom(fmt.Sprintf("%032X", count), "casual", "finished", true, false, host)
		if _, err := f.db.Exec(f.ctx, `INSERT INTO room_members(room_id,user_id,role)
			VALUES($1,$2,'player'),($1,$3,'spectator')`, id, f.users[player], f.users[viewer]); err != nil {
			t.Fatal(err)
		}
		// 所有提问先入库，答案紧随其后。最近页会触发最多 50 次父问题回补。
		if _, err := f.db.Exec(f.ctx, `INSERT INTO room_events(room_id,user_id,kind,body)
			SELECT $1,$2,'question',repeat('问题内容',12)||n FROM generate_series(1,$3::int) AS n`, id, f.users[player], count/2); err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.Exec(f.ctx, `INSERT INTO room_events(room_id,user_id,kind,result,parent_id)
			SELECT $1,$2,'answer','是',id FROM room_events WHERE room_id=$1 AND kind='question' ORDER BY id`, id, f.users[host]); err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.Exec(f.ctx, `ANALYZE room_events`); err != nil {
			t.Fatal(err)
		}
		var before int64
		if err := f.db.QueryRow(f.ctx, `SELECT id FROM room_events WHERE room_id=$1 ORDER BY id OFFSET $2 LIMIT 1`, id, count/2).Scan(&before); err != nil {
			t.Fatal(err)
		}
		path := fmt.Sprintf("/api/rooms/%d", id)
		cases := []snapshotSample{
			{name: "full", path: path, want: count},
			{name: "latest", path: path + "?history=1", want: 100},
			{name: "middle", path: fmt.Sprintf("%s?history=1&before=%d", path, before), want: 50},
		}
		measureSnapshotSamples(t, f, queries, count, viewer, cases)
	}
}

type snapshotSample struct {
	name  string
	path  string
	want  int
	bytes int
	dbq   int64
	ms    []float64
}

func measureSnapshotSamples(t *testing.T, f *replayFixture, queries *queryCounter, count, viewer int, cases []snapshotSample) {
	t.Helper()
	for i := 0; i < 35; i++ {
		for j := range cases {
			cases[j].request(t, f, queries, viewer, i >= 5)
		}
	}
	for _, sample := range cases {
		sort.Float64s(sample.ms)
		t.Logf("events=%d mode=%s bytes=%d dbq/op=%.1f p50=%.2fms p95=%.2fms", count, sample.name,
			sample.bytes, float64(sample.dbq)/30, (sample.ms[14]+sample.ms[15])/2, sample.ms[28])
	}
}

func (s *snapshotSample) request(t *testing.T, f *replayFixture, queries *queryCounter, viewer int, measure bool) {
	t.Helper()
	queries.count.Store(0)
	start := time.Now()
	response := f.call(http.MethodGet, s.path, nil, viewer)
	duration := time.Since(start)
	if response.Code != http.StatusOK {
		t.Fatalf("%s 请求失败（%d）：%s", s.name, response.Code, response.Body.String())
	}
	if !measure {
		return
	}
	if len(s.ms) == 0 {
		var snapshot struct {
			Events []json.RawMessage `json:"events"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil || len(snapshot.Events) != s.want {
			t.Fatalf("%s 事件数=%d，期望 %d，错误=%v", s.name, len(snapshot.Events), s.want, err)
		}
	}
	s.bytes = response.Body.Len()
	s.dbq += queries.count.Load()
	s.ms = append(s.ms, float64(duration)/float64(time.Millisecond))
}
