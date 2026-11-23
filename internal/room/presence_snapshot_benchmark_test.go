package room_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/coder/websocket"
)

// 分别用 50 和 120 名成员测快照开销，主持连接全程保持打开。
func TestRoomPresenceSnapshotScale(t *testing.T) {
	if os.Getenv("ROOM_PRESENCE_SCALE") != "1" {
		t.Skip("设置 ROOM_PRESENCE_SCALE=1 后测量")
	}
	queries := &queryCounter{}
	f := newReplayFixtureWithCounter(t, queries)
	host := f.addUser("presence_bench_host")
	f.addSoup(host)
	server := httptest.NewServer(f.router)
	t.Cleanup(server.Close)
	for _, count := range []int{50, 120} {
		id := f.addRoom(fmt.Sprintf("%032X", count), "casual", "active", true, false, host)
		if _, err := f.db.Exec(f.ctx, `WITH added AS (
			INSERT INTO users(username,password_hash)
			SELECT 'presence_'||($1::bigint)::text||'_'||n,'test' FROM generate_series(1,$2::int) AS n RETURNING id
		) INSERT INTO room_members(room_id,user_id,role) SELECT $1::bigint,id,'spectator' FROM added`, id, count-1); err != nil {
			t.Fatal(err)
		}
		path := fmt.Sprintf("/api/rooms/%d", id)
		headers := http.Header{"Cookie": {f.cookie[host].String()}}
		conn, _, err := websocket.Dial(f.ctx, "ws"+strings.TrimPrefix(server.URL, "http")+path+"/ws", &websocket.DialOptions{HTTPHeader: headers})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.CloseNow() })
		sample := snapshotSample{name: "presence", path: path + "?history=2&tasks=1", want: 0}
		for i := 0; i < 35; i++ {
			sample.request(t, f, queries, host, i >= 5)
		}
		sort.Float64s(sample.ms)
		t.Logf("members=%d bytes=%d dbq/op=%.1f p50=%.2fms p95=%.2fms", count, sample.bytes,
			float64(sample.dbq)/30, (sample.ms[14]+sample.ms[15])/2, sample.ms[28])
	}
}
