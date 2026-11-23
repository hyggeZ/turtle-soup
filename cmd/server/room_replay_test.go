package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"
)

const (
	roomReplayRooms        = 16
	roomReplayRounds       = 3
	roomReplaySteps        = 6
	roomReplayBatchTimeout = 45 * time.Second
	roomReplayQuestion     = "车站的时钟停了吗？"
)

type roomReplayAction struct {
	Type        string `json:"type"`
	OperationID string `json:"operationId"`
	Body        string `json:"body,omitempty"`
	EventID     int64  `json:"eventId,omitempty"`
	Result      string `json:"result,omitempty"`
}

type roomReplayEvent struct {
	ID       int64  `json:"id"`
	Kind     string `json:"kind"`
	Body     string `json:"body"`
	Result   string `json:"result"`
	ParentID int64  `json:"parentId"`
}

type roomReplayView struct {
	ID      int64             `json:"id"`
	Role    string            `json:"myRole"`
	Status  string            `json:"status"`
	Answer  string            `json:"answer"`
	Cursor  int64             `json:"eventCursor"`
	HasMore bool              `json:"hasMoreNew"`
	Events  []roomReplayEvent `json:"events"`
	Tasks   *struct {
		Items []roomReplayEvent `json:"items"`
	} `json:"tasks"`
	OK        bool `json:"ok"`
	Duplicate bool `json:"duplicate"`
}

type roomReplaySample struct {
	kind string
	readLoadSample
}

// TestRoomHTTPReplay 以两个角色重放固定混合流量，每次使用新的房间。
func TestRoomHTTPReplay(t *testing.T) {
	if os.Getenv("ROOM_HTTP_REPLAY") != "1" {
		t.Skip("设置 ROOM_HTTP_REPLAY=1 后才执行合成流量回放")
	}
	server, db, pgVersion := openRoomReplayFixture(t)
	logReadLoad(t, "ROOM_REPLAY_ENV", map[string]any{
		"go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH,
		"cpu": runtime.NumCPU(), "gomaxprocs": runtime.GOMAXPROCS(0), "postgres": pgVersion,
		"poolMaxConns": readLoadPoolConns, "roomsPerBatch": roomReplayRooms, "stepsPerRoom": roomReplaySteps,
		"requestTimeoutMs": readLoadMS(readLoadTimeout), "batchTimeoutMs": readLoadMS(roomReplayBatchTimeout),
	})
	for round := 1; round <= roomReplayRounds; round++ {
		for _, workers := range []int{1, 4} {
			label := fmt.Sprintf("%d_%d", round, workers)
			warmRooms := seedRoomReplay(t, db, label+"w", workers)
			rooms := seedRoomReplay(t, db, label, roomReplayRooms)
			client := newReadLoadClient(workers)
			warm, _ := runRoomReplayBatch(client, server.URL, warmRooms, workers)
			before := db.Stat()
			results, elapsed := runRoomReplayBatch(client, server.URL, rooms, workers)
			after := db.Stat()
			client.CloseIdleConnections()

			result := summarizeRoomReplay(workers, round, elapsed, results, warm)
			result.PoolWaitCount = after.EmptyAcquireCount() - before.EmptyAcquireCount()
			result.PoolWaitMS = readLoadMS(after.EmptyAcquireWaitTime() - before.EmptyAcquireWaitTime())
			result.PoolAcquireCanceled = after.CanceledAcquireCount() - before.CanceledAcquireCount()
			logReadLoad(t, "ROOM_REPLAY_RESULT", result)
			if result.Completed != len(rooms) || result.Errors != 0 || result.WarmupErrors != 0 {
				t.Errorf("第 %d 轮、%d 并发：完成 %d/%d 局，预热错误 %d，正式错误 %d",
					round, workers, result.Completed, len(rooms), result.WarmupErrors, result.Errors)
			}
			checkRoomReplayRows(t, db, rooms)
			checkRoomReplayRows(t, db, warmRooms)
		}
	}
}

// 不同房间可并行；每个房间的六步依赖前一响应，始终按序发出。
func runRoomReplayBatch(client *http.Client, base string, rooms []roomReplayCase, workers int) ([][]roomReplaySample, time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), roomReplayBatchTimeout)
	defer cancel()
	results := make([][]roomReplaySample, len(rooms))
	start := make(chan struct{})
	var wait sync.WaitGroup
	for worker := range workers {
		wait.Go(func() {
			<-start
			for i := worker; i < len(rooms); i += workers {
				results[i] = runRoomReplay(ctx, client, base, rooms[i])
			}
		})
	}
	started := time.Now()
	close(start)
	wait.Wait()
	return results, time.Since(started)
}

func runRoomReplay(ctx context.Context, client *http.Client, base string, room roomReplayCase) []roomReplaySample {
	samples := make([]roomReplaySample, 0, roomReplaySteps)
	step := func(name, kind, path, token string, action *roomReplayAction, check func(roomReplayView) bool) (roomReplayView, bool) {
		view, sample := callRoomReplay(ctx, client, base+path, token, action)
		if sample.failure == "" && !check(view) {
			sample.failure = "response_contract"
		}
		if sample.failure != "" {
			sample.failure = name + ":" + sample.failure
		}
		samples = append(samples, roomReplaySample{kind, sample})
		return view, sample.failure == ""
	}
	path := fmt.Sprintf("/api/rooms/%d", room.id)
	initial, ok := step("player_initial", "read", path+"?history=2", room.player, nil, func(v roomReplayView) bool {
		return v.ID == room.id && v.Role == "player" && v.Status == "active" && v.Answer == "" && len(v.Events) == 0 && v.Cursor == 0
	})
	if !ok {
		return samples
	}

	path = fmt.Sprintf("/api/rooms/%d", initial.ID)
	actions := path + "/actions"
	question := &roomReplayAction{Type: "question", OperationID: "replay-question", Body: roomReplayQuestion}
	if _, ok := step("player_question", "write", actions, room.player, question, roomReplayNewAction); !ok {
		return samples
	}
	hostPath := fmt.Sprintf("%s?history=2&after=%d&tasks=1", path, initial.Cursor)
	host, ok := step("host_delta", "read", hostPath, room.host, nil, func(v roomReplayView) bool {
		return v.ID == initial.ID && v.Role == "host" && v.Answer == roomReplayAnswer && !v.HasMore &&
			len(v.Events) == 1 && roomReplayMatchesQuestion(v.Events[0], v.Cursor) && v.Cursor > initial.Cursor &&
			v.Tasks != nil && len(v.Tasks.Items) == 1 && roomReplayMatchesQuestion(v.Tasks.Items[0], v.Cursor)
	})
	if !ok {
		return samples
	}
	if _, ok := step("player_retry", "retry", actions, room.player, question, func(v roomReplayView) bool {
		return v.OK && v.Duplicate
	}); !ok {
		return samples
	}
	answer := &roomReplayAction{Type: "answer", OperationID: "replay-answer", EventID: host.Events[0].ID, Result: "是"}
	if _, ok := step("host_answer", "write", actions, room.host, answer, roomReplayNewAction); !ok {
		return samples
	}
	// 新回答需要附带原问题，即使原问题已经落在增量游标之前。
	playerPath := fmt.Sprintf("%s?history=2&after=%d", path, host.Cursor)
	step("player_delta", "read", playerPath, room.player, nil, func(v roomReplayView) bool {
		return v.ID == initial.ID && v.Role == "player" && v.Answer == "" && !v.HasMore &&
			len(v.Events) == 2 && roomReplayMatchesQuestion(v.Events[0], host.Cursor) && v.Cursor > host.Cursor &&
			v.Events[1].Kind == "answer" && v.Events[1].ID == v.Cursor && v.Events[1].ParentID == host.Cursor && v.Events[1].Result == "是"
	})
	return samples
}

func roomReplayMatchesQuestion(event roomReplayEvent, id int64) bool {
	return event.ID == id && event.Kind == "question" && event.Body == roomReplayQuestion
}

func roomReplayNewAction(view roomReplayView) bool { return view.OK && !view.Duplicate }

// 计时从请求发出到读完正文，构造请求、解码和断言另计入整批响应时间。
func callRoomReplay(ctx context.Context, client *http.Client, url, token string, action *roomReplayAction) (roomReplayView, readLoadSample) {
	var view roomReplayView
	var sample readLoadSample
	method := http.MethodGet
	var body io.Reader
	if action != nil {
		method = http.MethodPost
		data, _ := json.Marshal(action)
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		sample.failure = "request_build"
		return view, sample
	}
	req.AddCookie(&http.Cookie{Name: "turtle_session", Value: token})
	if action != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	started := time.Now()
	res, err := client.Do(req)
	if err != nil {
		sample.failure = readLoadErrorKind(err, "transport")
		return view, sample
	}
	data, err := io.ReadAll(res.Body)
	sample.duration = time.Since(started)
	_ = res.Body.Close()
	sample.status = res.StatusCode
	sample.failure = readLoadResponseFailure(res)
	if sample.failure == "" && err != nil {
		sample.failure = readLoadErrorKind(err, "response_body")
	}
	if sample.failure == "" && json.Unmarshal(data, &view) != nil {
		sample.failure = "response_json"
	}
	return view, sample
}

type roomReplayResult struct {
	readLoadResult
	Rooms     int                 `json:"rooms"`
	Completed int                 `json:"completedRooms"`
	KindP95MS map[string]*float64 `json:"kindP95Ms"`
}

func summarizeRoomReplay(workers, round int, elapsed time.Duration, results, warm [][]roomReplaySample) roomReplayResult {
	var completed int
	var samples, warmup []readLoadSample
	groups := make(map[string][]readLoadSample)
	for _, room := range results {
		if len(room) == roomReplaySteps && room[len(room)-1].failure == "" {
			completed++
		}
		for _, sample := range room {
			samples = append(samples, sample.readLoadSample)
			groups[sample.kind] = append(groups[sample.kind], sample.readLoadSample)
		}
	}
	for _, room := range warm {
		for _, sample := range room {
			warmup = append(warmup, sample.readLoadSample)
		}
	}
	p95 := make(map[string]*float64)
	for kind, group := range groups {
		p95[kind] = summarizeReadLoad(kind, workers, round, elapsed, group, nil).P95MS
	}
	return roomReplayResult{
		readLoadResult: summarizeReadLoad("room_mixed_http_replay", workers, round, elapsed, samples, warmup),
		Rooms:          len(results), Completed: completed, KindP95MS: p95,
	}
}
