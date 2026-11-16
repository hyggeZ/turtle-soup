package room_test

import (
	"fmt"
	"net/http"
	"testing"
)

// 每次请求使用独立账号，计入认证、事务、入场和响应；用固定迭代数避免准备过多账号。
func BenchmarkFinishedReplayJoin(b *testing.B) {
	for _, scenario := range []struct {
		name      string
		code      string
		public    bool
		published bool
		want      int
	}{
		{"legacy_rejected_proxy", "ABCDEF12", false, false, http.StatusNotFound},
		{"unpublished", "00112233445566778899AABBCCDDEEFF", true, false, http.StatusOK},
		{"published", "00112233445566778899AABBCCDDEEFF", true, true, http.StatusOK},
	} {
		b.Run(scenario.name, func(b *testing.B) {
			queries := &queryCounter{}
			f := newReplayFixtureWithCounter(b, queries)
			host := f.addUser("replay_bench_host")
			f.addSoup(host)
			f.addRoom(scenario.code, "casual", "finished", scenario.public, scenario.published, host)
			visitors := make([]int, b.N)
			for i := range visitors {
				visitors[i] = f.addUser(fmt.Sprintf("replay_bench_visitor_%d", i))
			}
			queries.count.Store(0)
			b.ReportAllocs()
			b.ResetTimer()
			for _, visitor := range visitors {
				res := f.call(http.MethodPost, "/api/rooms/join", map[string]any{"code": scenario.code}, visitor)
				if res.Code != scenario.want {
					b.Fatalf("回看入场基准返回 %d：%s", res.Code, res.Body.String())
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(queries.count.Load())/float64(b.N), "dbq/op")
		})
	}
}

func BenchmarkFinishedReplayList(b *testing.B) {
	queries := &queryCounter{}
	f := newReplayFixtureWithCounter(b, queries)
	host := f.addUser("replay_bench_host")
	f.addSoup(host)
	for i := 1; i <= 50; i++ {
		f.addRoom(fmt.Sprintf("%032X", i), "casual", "finished", true, false, host)
	}
	queries.count.Store(0)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res := f.call(http.MethodGet, "/api/rooms/replays", nil, -1)
		if res.Code != http.StatusOK {
			b.Fatalf("回看列表基准返回 %d：%s", res.Code, res.Body.String())
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(queries.count.Load())/float64(b.N), "dbq/op")
}
