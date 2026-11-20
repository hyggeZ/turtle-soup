package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	readLoadWorkersWarmup = 5
	readLoadRequests      = 600
	readLoadRepeats       = 3
	readLoadTimeout       = 3 * time.Second
	readLoadBatchTimeout  = 15 * time.Second
)

type readLoadCase struct {
	name, path, cookie string
	verify             func(io.Reader) error
}

type readLoadSample struct {
	status   int
	duration time.Duration
	failure  string
}

type readLoadResult struct {
	Scenario            string         `json:"scenario"`
	Workers             int            `json:"workers"`
	Round               int            `json:"round"`
	Requests            int            `json:"requests"`
	Success             int            `json:"success"`
	Errors              int            `json:"errors"`
	ErrorRate           float64        `json:"errorRate"`
	WarmupErrors        int            `json:"warmupErrors"`
	Status              map[int]int    `json:"status"`
	FailureTypes        map[string]int `json:"failureTypes"`
	ElapsedMS           float64        `json:"elapsedMs"`
	SuccessQPS          float64        `json:"successQps"`
	P50MS               *float64       `json:"p50Ms"`
	P95MS               *float64       `json:"p95Ms"`
	P99MS               *float64       `json:"p99Ms"`
	PoolWaitCount       int64          `json:"poolWaitCount"`
	PoolWaitMS          float64        `json:"poolWaitMs"`
	PoolAcquireCanceled int64          `json:"poolAcquireCanceled"`
}

// TestReadHTTPThroughput 固定数据与并发度，统计真实回环 HTTP 读取的成功吞吐。
func TestReadHTTPThroughput(t *testing.T) {
	if os.Getenv("ROOM_HTTP_LOAD") != "1" {
		t.Skip("设置 ROOM_HTTP_LOAD=1 后才执行 HTTP 吞吐基准")
	}
	server, db, cases, pgVersion := newReadLoadFixture(t)
	logReadLoad(t, "READ_LOAD_ENV", map[string]any{
		"go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH,
		"cpu": runtime.NumCPU(), "gomaxprocs": runtime.GOMAXPROCS(0), "postgres": pgVersion,
		"poolMaxConns": readLoadPoolConns, "requestTimeoutMs": readLoadMS(readLoadTimeout),
		"batchTimeoutMs": readLoadMS(readLoadBatchTimeout),
		"soups":          readLoadSoups, "answeredPairs": readLoadPairs, "pendingTasks": readLoadPending,
	})
	for _, sample := range cases {
		if err := verifyReadLoadCase(server.URL, sample); err != nil {
			t.Fatalf("%s 正式采样前的检查失败：%v", sample.name, err)
		}
	}
	for _, sample := range cases {
		for _, workers := range []int{1, 4, 16} {
			for round := 1; round <= readLoadRepeats; round++ {
				client := newReadLoadClient(workers)
				warmup, _ := sendReadLoad(client, server.URL, sample, workers, workers*readLoadWorkersWarmup)
				before := db.Stat()
				responses, elapsed := sendReadLoad(client, server.URL, sample, workers, readLoadRequests)
				after := db.Stat()
				client.CloseIdleConnections()

				result := summarizeReadLoad(sample.name, workers, round, elapsed, responses, warmup)
				result.PoolWaitCount = after.EmptyAcquireCount() - before.EmptyAcquireCount()
				result.PoolWaitMS = readLoadMS(after.EmptyAcquireWaitTime() - before.EmptyAcquireWaitTime())
				result.PoolAcquireCanceled = after.CanceledAcquireCount() - before.CanceledAcquireCount()
				logReadLoad(t, "READ_LOAD_RESULT", result)
				if result.Errors != 0 || result.WarmupErrors != 0 {
					t.Errorf("%s %d 并发第 %d 轮：预热失败 %d 次，正式请求失败 %d 次", sample.name, workers, round, result.WarmupErrors, result.Errors)
				}
			}
		}
	}
}

func verifyReadLoadCase(base string, sample readLoadCase) error {
	client := newReadLoadClient(1)
	defer client.CloseIdleConnections()
	res, err := client.Do(newReadLoadRequest(context.Background(), base, sample))
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if failure := readLoadResponseFailure(res); failure != "" {
		return errors.New(failure)
	}
	return sample.verify(res.Body)
}

func newReadLoadClient(workers int) *http.Client {
	return &http.Client{
		Timeout: readLoadTimeout,
		Transport: &http.Transport{
			MaxConnsPerHost: workers, MaxIdleConnsPerHost: workers, MaxIdleConns: workers,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func newReadLoadRequest(ctx context.Context, base string, sample readLoadCase) *http.Request {
	// 地址由 httptest 回环监听生成，路径来自固定夹具。
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+sample.path, nil)
	if sample.cookie != "" {
		req.AddCookie(&http.Cookie{Name: "turtle_session", Value: sample.cookie})
	}
	return req
}

func readLoadResponseFailure(res *http.Response) string {
	if res.StatusCode != http.StatusOK {
		return "http_status"
	}
	if !strings.HasPrefix(res.Header.Get("Content-Type"), "application/json") {
		return "content_type"
	}
	return ""
}

func sendReadLoad(client *http.Client, base string, sample readLoadCase, workers, total int) ([]readLoadSample, time.Duration) {
	responses := make([]readLoadSample, total)
	ctx, cancel := context.WithTimeout(context.Background(), readLoadBatchTimeout)
	defer cancel()
	start := make(chan struct{})
	var wait sync.WaitGroup
	for worker := range workers {
		wait.Go(func() {
			<-start
			for i := worker; i < total; i += workers {
				responses[i] = singleReadLoad(ctx, client, base, sample)
			}
		})
	}
	started := time.Now()
	close(start)
	wait.Wait()
	return responses, time.Since(started)
}

func singleReadLoad(ctx context.Context, client *http.Client, base string, sample readLoadCase) readLoadSample {
	// 某一批完全不可用时仍记录所有请求位置，并继续其他并发组。
	if ctx.Err() != nil {
		return readLoadSample{failure: "batch_timeout"}
	}
	started := time.Now()
	res, err := client.Do(newReadLoadRequest(ctx, base, sample))
	if err != nil {
		return readLoadSample{failure: readLoadErrorKind(err, "transport")}
	}
	failure := readLoadResponseFailure(res)
	if _, err = io.Copy(io.Discard, res.Body); err != nil && failure == "" {
		failure = readLoadErrorKind(err, "response_body")
	}
	_ = res.Body.Close()
	return readLoadSample{status: res.StatusCode, duration: time.Since(started), failure: failure}
}

func readLoadErrorKind(err error, other string) string {
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return "timeout"
	}
	return other
}

func summarizeReadLoad(name string, workers, round int, elapsed time.Duration, responses, warmup []readLoadSample) readLoadResult {
	result := readLoadResult{
		Scenario: name, Workers: workers, Round: round, Requests: len(responses), ElapsedMS: readLoadMS(elapsed),
		Status: make(map[int]int), FailureTypes: make(map[string]int),
	}
	for _, sample := range warmup {
		if sample.failure != "" {
			result.WarmupErrors++
		}
	}
	latency := make([]time.Duration, 0, len(responses))
	for _, sample := range responses {
		if sample.status > 0 {
			result.Status[sample.status]++
		}
		if sample.failure != "" {
			result.FailureTypes[sample.failure]++
			continue
		}
		latency = append(latency, sample.duration)
	}
	slices.Sort(latency)
	result.Success, result.Errors = len(latency), len(responses)-len(latency)
	result.ErrorRate = float64(result.Errors) / float64(result.Requests)
	result.SuccessQPS = float64(result.Success) / elapsed.Seconds()
	result.P50MS, result.P95MS, result.P99MS = readLoadPercentile(latency, 50), readLoadPercentile(latency, 95), readLoadPercentile(latency, 99)
	return result
}

func readLoadPercentile(sorted []time.Duration, percent int) *float64 {
	if len(sorted) == 0 {
		return nil
	}
	index := (len(sorted)*percent+99)/100 - 1
	value := readLoadMS(sorted[index])
	return &value
}

func readLoadMS(duration time.Duration) float64 { return float64(duration) / float64(time.Millisecond) }

func logReadLoad(t *testing.T, key string, result any) {
	t.Helper()
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s %s", key, data)
}
