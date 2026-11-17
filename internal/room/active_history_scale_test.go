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

// TestActiveHistoryScale 比较长局首读和掉线后追赶 500 条事件的成本。
func TestActiveHistoryScale(t *testing.T) {
	if os.Getenv("ACTIVE_HISTORY_SCALE") != "1" {
		t.Skip("设置 ACTIVE_HISTORY_SCALE=1 后测量")
	}
	queries := &queryCounter{}
	f := newReplayFixtureWithCounter(t, queries)
	host := f.addUser("active_host")
	player := f.addUser("active_player")
	f.addSoup(host)
	id := f.addRoom("00000000000000000000000000005003", "casual", "active", true, false, host)
	if _, err := f.db.Exec(f.ctx, `INSERT INTO room_members(room_id,user_id,role) VALUES($1,$2,'player')`, id, f.users[player]); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(f.ctx, `INSERT INTO room_events(room_id,user_id,kind,body)
		VALUES($1,$2,'question','早期待答'),($1,$2,'restore','早期待判'),($1,$2,'answer_request','')`, id, f.users[player]); err != nil {
		t.Fatal(err)
	}
	// 统一写完问题再写答案，模拟增量批次需要补取旧问题的情况。
	if _, err := f.db.Exec(f.ctx, `INSERT INTO room_events(room_id,user_id,kind,body)
		SELECT $1,$2,'question',repeat('问题内容',12)||n FROM generate_series(1,2500) AS n`, id, f.users[player]); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(f.ctx, `INSERT INTO room_events(room_id,user_id,kind,result,parent_id)
		SELECT $1,$2,'answer','是',id FROM room_events WHERE room_id=$1 AND kind='question' AND body<>'早期待答' ORDER BY id`, id, f.users[host]); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(f.ctx, `ANALYZE room_events`); err != nil {
		t.Fatal(err)
	}
	var before, catchupAfter, latest int64
	if err := f.db.QueryRow(f.ctx, `SELECT id FROM room_events WHERE room_id=$1 ORDER BY id OFFSET 2503 LIMIT 1`, id).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(f.ctx, `SELECT id FROM room_events WHERE room_id=$1 ORDER BY id DESC OFFSET 500 LIMIT 1`, id).Scan(&catchupAfter); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(f.ctx, `SELECT max(id) FROM room_events WHERE room_id=$1`, id).Scan(&latest); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/rooms/%d", id)
	empty := fmt.Sprintf("%s?after=%d&tasks=1", path, latest)
	measureSnapshotSamples(t, f, queries, 5003, host, []snapshotSample{
		{name: "active_full", path: path + "?history=1&tasks=1", want: 5003},
		{name: "active_latest", path: path + "?history=2&tasks=1", want: 100},
		{name: "active_older", path: fmt.Sprintf("%s?history=2&before=%d", path, before), want: 50},
		{name: "empty_legacy_delta", path: empty},
		{name: "empty_paged_delta", path: empty + "&history=2"},
	})
	measureActiveCatchup(t, f, queries, path, catchupAfter, host)
}

type catchupSample struct {
	pages, totalBytes, maxPageBytes int
	dbq                             int64
	ms                              []float64
}

func measureActiveCatchup(t *testing.T, f *replayFixture, queries *queryCounter, path string, after int64, host int) {
	t.Helper()
	for _, history := range []bool{false, true} {
		var sample catchupSample
		for i := 0; i < 23; i++ {
			start := time.Now()
			queries.count.Store(0)
			pages, totalBytes, maxPageBytes := readActiveCatchup(t, f, path, after, host, history)
			duration := time.Since(start)
			if i >= 3 {
				sample.pages, sample.totalBytes, sample.maxPageBytes = pages, totalBytes, maxPageBytes
				sample.dbq += queries.count.Load()
				sample.ms = append(sample.ms, float64(duration)/float64(time.Millisecond))
			}
		}
		sort.Float64s(sample.ms)
		t.Logf("catchup history=%v pages=%d totalBytes=%d maxPageBytes=%d dbq/drain=%.1f p50=%.2fms p95=%.2fms",
			history, sample.pages, sample.totalBytes, sample.maxPageBytes, float64(sample.dbq)/20,
			(sample.ms[9]+sample.ms[10])/2, sample.ms[18])
	}
}

func readActiveCatchup(t *testing.T, f *replayFixture, path string, after int64, host int, history bool) (pages, totalBytes, maxPageBytes int) {
	t.Helper()
	seen := make(map[int64]struct{}, 1000)
	for pages < 11 {
		url := fmt.Sprintf("%s?after=%d&tasks=1", path, after)
		if history {
			url += "&history=2"
		}
		response := f.call(http.MethodGet, url, nil, host)
		var data struct {
			EventCursor int64 `json:"eventCursor"`
			HasMoreNew  bool  `json:"hasMoreNew"`
			Events      []struct {
				ID int64 `json:"id"`
			} `json:"events"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &data) != nil || data.EventCursor <= after {
			t.Fatalf("追赶请求无效：%d %s", response.Code, response.Body.String())
		}
		for _, event := range data.Events {
			seen[event.ID] = struct{}{}
		}
		pages++
		bytes := response.Body.Len()
		totalBytes += bytes
		maxPageBytes = max(maxPageBytes, bytes)
		after = data.EventCursor
		if !data.HasMoreNew {
			wantPages := 1
			if history {
				wantPages = 10
			}
			if len(seen) != 1000 || pages != wantPages {
				t.Fatalf("追赶结果不全：事件 %d，页数 %d，期望 %d 页", len(seen), pages, wantPages)
			}
			return pages, totalBytes, maxPageBytes
		}
	}
	t.Fatal("追赶没有结束")
	return 0, 0, 0
}
