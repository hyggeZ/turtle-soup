package forum_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

type editTraceKey struct{}
type editQueryTracer struct{}

func (editQueryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	if count, ok := ctx.Value(editTraceKey{}).(*int); ok {
		*count = *count + 1
	}
	return ctx
}

func (editQueryTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// 同一测试可在旧版和候选版运行。论坛连接池统计 SQL，认证用的独立连接池不计入。
func TestSoupEditVersionPerformance(t *testing.T) {
	if os.Getenv("SOUP_EDIT_PERF") != "1" {
		t.Skip("设置 SOUP_EDIT_PERF=1 后测量")
	}
	f := newDeletionFixture(t, editQueryTracer{}, nil)
	warmPath := fmt.Sprintf("/api/soups/%d", f.soup())
	start := readSoupEditResult(t, f.call(http.MethodGet, warmPath, 0, nil).Body.Bytes())
	mode := "baseline"
	if start.Version > 0 {
		mode = "versioned"
	}
	useVersion := true
	warmInput := soupEditInput("预热保存", max(1, start.Version))
	warm := f.call(http.MethodPatch, warmPath, 0, warmInput)
	if mode == "baseline" && warm.Code == http.StatusBadRequest {
		useVersion = false
		delete(warmInput, "version")
		warm = f.call(http.MethodPatch, warmPath, 0, warmInput)
	}
	if warm.Code != http.StatusOK {
		t.Fatalf("预热保存失败：%d %s", warm.Code, warm.Body.String())
	}

	path := fmt.Sprintf("/api/soups/%d", f.soup())
	before := f.call(http.MethodGet, path, 0, nil)
	version := readSoupEditResult(t, before.Body.Bytes()).Version
	if before.Code != http.StatusOK || version != start.Version {
		t.Fatalf("实验需要具有同一起始版本的两道题：%d %d / %d", before.Code, version, start.Version)
	}
	const size = 16
	type sample struct {
		status, sql int
		latency     time.Duration
		body        []byte
	}
	samples := make([]sample, size)
	requests := make([]*http.Request, size)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	for i := range size {
		input := soupEditInput(fmt.Sprintf("同版本草稿%d", i), max(1, version))
		if !useVersion {
			delete(input, "version")
		}
		data, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		requestCtx := context.WithValue(ctx, editTraceKey{}, &samples[i].sql)
		req := httptest.NewRequest(http.MethodPatch, path, bytes.NewReader(data)).WithContext(requestCtx)
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(f.cookies[0])
		requests[i] = req
	}
	gate := make(chan struct{})
	var ready, done sync.WaitGroup
	ready.Add(size)
	done.Add(size)
	for i, req := range requests {
		go func() {
			defer done.Done()
			res := httptest.NewRecorder()
			ready.Done()
			<-gate
			start := time.Now()
			f.router.ServeHTTP(res, req)
			samples[i].latency = time.Since(start)
			samples[i].status, samples[i].body = res.Code, res.Body.Bytes()
		}()
	}
	ready.Wait()
	startAt := time.Now()
	close(gate)
	done.Wait()
	wall := time.Since(startAt)

	accepted := make(map[string]bool)
	latencies, sql := make(map[int][]int64), make(map[int]int)
	for _, sample := range samples {
		result := readSoupEditResult(t, sample.body)
		switch sample.status {
		case http.StatusOK:
			accepted[result.Title] = true
		case http.StatusConflict:
			if result.Code != "SOUP_VERSION_CONFLICT" {
				t.Fatalf("意外冲突：%s", sample.body)
			}
		default:
			t.Fatalf("意外响应 %d：%s", sample.status, sample.body)
		}
		latencies[sample.status] = append(latencies[sample.status], sample.latency.Microseconds())
		sql[sample.status] += sample.sql
	}
	now := readSoupEditResult(t, f.call(http.MethodGet, path, 0, nil).Body.Bytes())
	if !accepted[now.Title] || len(accepted) != len(latencies[http.StatusOK]) {
		t.Fatalf("最终题目与成功响应不一致：%+v", now)
	}
	if mode == "versioned" && (len(accepted) != 1 || now.Version != version+1) || mode == "baseline" && len(accepted) != size {
		t.Fatalf("同版本竞写结果不符：%s 成功 %d，版本 %+v", mode, len(accepted), now)
	}
	t.Logf("edit_burst mode=%s sent_version=%v time_ms=%.2f accepted=%d conflicts=%d overwrite_opportunities=%d",
		mode, useVersion, float64(wall)/float64(time.Millisecond), len(accepted), size-len(accepted), len(accepted)-1)
	for _, status := range []int{http.StatusOK, http.StatusConflict} {
		values := latencies[status]
		if len(values) > 0 {
			sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
			t.Logf("edit_samples status=%d sql_total=%d latency_us=%v", status, sql[status], values)
		}
	}
}
