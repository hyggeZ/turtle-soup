package telemetry

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// 输入使用同一组固定阶段和相同文件尺寸，生成与校验均在计时前完成。
func BenchmarkRestoreTrace(b *testing.B) {
	for _, test := range []struct {
		name     string
		previous int
		current  int
	}{{"small", 0, 64 << 10}, {"two_generations", 10 << 20, 10 << 20}} {
		b.Run(test.name, func(b *testing.B) {
			dir := b.TempDir()
			oldBytes, oldCount := writeRestoreBenchmarkLog(b, dir, "requests.previous.jsonl", test.previous, 0)
			newBytes, newCount := writeRestoreBenchmarkLog(b, dir, "requests.jsonl", test.current, oldCount)
			want := min(oldCount+newCount, maxRecords)
			store, err := NewStore(dir)
			if err != nil {
				b.Fatal(err)
			}
			if err = store.Close(); err != nil {
				b.Fatal(err)
			}
			if len(store.records) != want || store.records[want-1].TraceID != restoreBenchmarkID(oldCount+newCount) {
				b.Fatalf("恢复记录不正确：期望记录 %d，实际 %d", want, len(store.records))
			}

			times := make([]time.Duration, b.N)
			b.ReportAllocs()
			b.ResetTimer()
			for i := range b.N {
				start := time.Now()
				store, err := NewStore(dir)
				times[i] = time.Since(start)
				if err != nil {
					b.Fatal(err)
				}
				if err := store.Close(); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			slices.Sort(times)
			b.ReportMetric(float64(oldBytes+newBytes), "input_B")
			b.ReportMetric(float64(oldCount+newCount), "source_rows")
			b.ReportMetric(float64(want), "restored_rows")
			b.ReportMetric(float64(times[(b.N-1)/2])/float64(time.Millisecond), "p50_ms")
		})
	}
}

func restoreBenchmarkID(id int) string {
	return fmt.Sprintf("%032x", id)
}

func writeRestoreBenchmarkLog(b *testing.B, dir, name string, limit, offset int) (size, count int) {
	b.Helper()
	if limit == 0 {
		return 0, 0
	}
	file, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		b.Fatal(err)
	}
	defer file.Close()
	writer := bufio.NewWriterSize(file, 64<<10)
	record := Record{
		Time: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC), Method: http.MethodPost,
		Route: "/api/ai/solo/:id/actions", Status: http.StatusOK, DurationMS: 17.5, AuthMS: 1, DBMS: 4.5, DBCount: 4,
		Spans: []Span{
			{Name: "db.acquire", StartMS: 0.02, DurationMS: 0.3},
			{Name: "db.query", StartMS: 0.32, DurationMS: 0.55},
			{Name: "auth.session", StartMS: 0.02, DurationMS: 1},
			{Name: "ai.claim", StartMS: 1.5, DurationMS: 0.9},
			{Name: "ai.history", StartMS: 2.4, DurationMS: 2.6},
			{Name: "ai.model", StartMS: 5, DurationMS: 8.84},
			{Name: "ai.validate", StartMS: 13.84, DurationMS: 0.16},
			{Name: "ai.persist", StartMS: 14.6, DurationMS: 2.5},
		},
	}
	for {
		record.TraceID = restoreBenchmarkID(offset + count + 1)
		line, err := json.Marshal(record)
		if err != nil {
			b.Fatal(err)
		}
		if size+len(line)+1 > limit {
			break
		}
		if _, err := writer.Write(line); err != nil {
			b.Fatal(err)
		}
		if err := writer.WriteByte('\n'); err != nil {
			b.Fatal(err)
		}
		size += len(line) + 1
		count++
	}
	if err := writer.Flush(); err != nil {
		b.Fatal(err)
	}
	return size, count
}
