package telemetry

import (
	"context"
	"net/http"
	"os"
	"slices"
	"testing"
	"time"
)

// 用真实 PostgreSQL 和 JSONL 写入交替对照，记录新增取连接阶段的开销。
func TestPoolTraceComparison(t *testing.T) {
	if os.Getenv("TURTLE_TEST_POOL_TRACE_PERF") != "1" {
		t.Skip("设置 TURTLE_TEST_POOL_TRACE_PERF=1 可运行取连接追踪对照")
	}
	var variants = []struct {
		name string
		f    *poolTraceFixture
	}{{"query_only", newPoolTraceFixture(t, false)}, {"query_and_acquire", newPoolTraceFixture(t, true)}}
	ctx := context.Background()
	var version string
	if err := variants[0].f.pool.QueryRow(ctx, "SHOW server_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	const warmups = 24
	const pairs = 4
	const samples = 128
	t.Logf("PostgreSQL %s，每种模式预热 %d 次，%d 对交替，每组 %d 个顺序请求", version, warmups, pairs, samples)
	for _, item := range variants {
		for range warmups {
			if res := item.f.request(ctx); res.Code != http.StatusNoContent {
				t.Fatalf("预热失败：%s HTTP %d", item.name, res.Code)
			}
		}
	}
	for pair := range pairs {
		for index := range variants {
			item := variants[(pair+index)%len(variants)]
			before := item.f.store.Snapshot().LogBytes
			times := make([]time.Duration, 0, samples)
			for range samples {
				start := time.Now()
				res := item.f.request(ctx)
				times = append(times, time.Since(start))
				if res.Code != http.StatusNoContent {
					t.Fatalf("第 %d 对 %s 失败：HTTP %d", pair+1, item.name, res.Code)
				}
			}
			snapshot := item.f.store.Snapshot()
			if snapshot.WriteError != "" || snapshot.LogBytes <= before {
				t.Fatal("没有成功写入 JSONL")
			}
			slices.Sort(times)
			t.Logf("pair=%d mode=%s n=%d p50_ms=%.6f p95_ms=%.6f total_ms=%.6f bytes_per_record=%.3f",
				pair+1, item.name, len(times), ms(times[samples/2-1]), ms(times[(95*samples+99)/100-1]),
				ms(sumPoolSamples(times)), float64(snapshot.LogBytes-before)/samples)
		}
	}
}

func sumPoolSamples(samples []time.Duration) time.Duration {
	var total time.Duration
	for _, elapsed := range samples {
		total += elapsed
	}
	return total
}
