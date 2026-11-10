// Package telemetry 记录请求链路、阶段耗时和有大小上限的结构化日志。
package telemetry

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

const maxFileBytes = 10 << 20
const maxRecords = 300

type Span struct {
	Name       string  `json:"name"`
	DurationMS float64 `json:"durationMs"`
}

type Record struct {
	TraceID    string    `json:"traceId"`
	Time       time.Time `json:"time"`
	Method     string    `json:"method"`
	Route      string    `json:"route"`
	Status     int       `json:"status"`
	DurationMS float64   `json:"durationMs"`
	AuthMS     float64   `json:"authMs"`
	DBMS       float64   `json:"dbMs"`
	DBCount    int       `json:"dbCount"`
	Spans      []Span    `json:"spans"`
}

type requestTrace struct {
	mu     sync.Mutex
	record Record
}

type contextKey struct{}
type queryKey struct{}

// Store 同时充当 Gin 中间件和 pgx 查询追踪器。
type Store struct {
	mu         sync.Mutex
	dir        string
	file       *os.File
	records    []Record
	writeError string
}

func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(dir, "requests.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	store := &Store{dir: dir, file: file, records: make([]Record, 0, maxRecords)}
	store.loadRecent()
	return store, nil
}

// 重启后从有限大小的日志文件恢复最近请求，供后台继续查看。
func (s *Store) loadRecent() {
	for _, name := range []string{"requests.previous.jsonl", "requests.jsonl"} {
		file, err := os.Open(filepath.Join(s.dir, name))
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			var record Record
			if json.Unmarshal(scanner.Bytes(), &record) != nil || record.TraceID == "" {
				continue
			}
			s.records = append(s.records, record)
			if len(s.records) > maxRecords {
				s.records = s.records[len(s.records)-maxRecords:]
			}
		}
		file.Close()
	}
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.file.Close()
}

func newID() string {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return ""
	}
	return hex.EncodeToString(data)
}

func (s *Store) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		started := time.Now()
		trace := &requestTrace{record: Record{TraceID: newID(), Time: started, Method: c.Request.Method, Spans: make([]Span, 0, 4)}}
		ctx := context.WithValue(c.Request.Context(), contextKey{}, trace)
		c.Request = c.Request.WithContext(ctx)
		c.Header("X-Trace-ID", trace.record.TraceID)
		c.Next()
		trace.mu.Lock()
		trace.record.Route = c.FullPath()
		trace.record.Status = c.Writer.Status()
		trace.record.DurationMS = ms(time.Since(started))
		record := trace.record
		trace.mu.Unlock()
		if record.Route == "" {
			record.Route = "unmatched"
		}
		s.append(record)
	}
}

func ms(duration time.Duration) float64 {
	return float64(duration) / float64(time.Millisecond)
}

// Begin 用于业务阶段；结束函数必须在该阶段返回时调用。
func Begin(ctx context.Context, name string) func() {
	trace, _ := ctx.Value(contextKey{}).(*requestTrace)
	if trace == nil {
		return func() {}
	}
	start := time.Now()
	return func() {
		duration := ms(time.Since(start))
		trace.mu.Lock()
		trace.record.Spans = append(trace.record.Spans, Span{Name: name, DurationMS: duration})
		if name == "auth.session" {
			trace.record.AuthMS += duration
		}
		trace.mu.Unlock()
	}
}

// 数据库追踪只记录耗时和错误标记，不记录 SQL、参数或用户资料。
func (s *Store) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	if ctx.Value(contextKey{}) == nil {
		return ctx
	}
	return context.WithValue(ctx, queryKey{}, time.Now())
}

func (s *Store) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	trace, _ := ctx.Value(contextKey{}).(*requestTrace)
	start, ok := ctx.Value(queryKey{}).(time.Time)
	if trace == nil || !ok {
		return
	}
	name := "db.query"
	if data.Err != nil {
		name = "db.error"
	}
	duration := ms(time.Since(start))
	trace.mu.Lock()
	trace.record.DBCount++
	trace.record.DBMS += duration
	trace.record.Spans = append(trace.record.Spans, Span{Name: name, DurationMS: duration})
	trace.mu.Unlock()
}

func (s *Store) append(record Record) {
	data, err := json.Marshal(record)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, record)
	if len(s.records) > maxRecords {
		s.records = s.records[len(s.records)-maxRecords:]
	}
	info, err := s.file.Stat()
	if err == nil && info.Size()+int64(len(data)+1) > maxFileBytes {
		if err = s.rotate(); err != nil {
			s.writeError = err.Error()
			return
		}
	}
	if _, err = s.file.Write(append(data, '\n')); err != nil {
		s.writeError = err.Error()
	}
}

func (s *Store) rotate() error {
	if err := s.file.Close(); err != nil {
		return err
	}
	current := filepath.Join(s.dir, "requests.jsonl")
	old := filepath.Join(s.dir, "requests.previous.jsonl")
	_ = os.Remove(old)
	if err := os.Rename(current, old); err != nil {
		return err
	}
	file, err := os.OpenFile(current, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	s.file = file
	return nil
}

type RouteStats struct {
	Route  string  `json:"route"`
	Count  int     `json:"count"`
	Errors int     `json:"errors"`
	AvgMS  float64 `json:"avgMs"`
	P95MS  float64 `json:"p95Ms"`
}

type Snapshot struct {
	LogBytes    int64        `json:"logBytes"`
	MaxLogBytes int64        `json:"maxLogBytes"`
	WriteError  string       `json:"writeError,omitempty"`
	Routes      []RouteStats `json:"routes"`
	Recent      []Record     `json:"recent"`
}

// Snapshot 只汇总最近 300 次请求；磁盘大小统计两个日志文件。
func (s *Store) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := Snapshot{MaxLogBytes: 2 * maxFileBytes, WriteError: s.writeError, Routes: []RouteStats{}, Recent: []Record{}}
	for _, name := range []string{"requests.jsonl", "requests.previous.jsonl"} {
		if info, err := os.Stat(filepath.Join(s.dir, name)); err == nil {
			result.LogBytes += info.Size()
		}
	}
	groups := map[string][]float64{}
	errors := map[string]int{}
	for i := len(s.records) - 1; i >= 0; i-- {
		record := s.records[i]
		groups[record.Route] = append(groups[record.Route], record.DurationMS)
		if record.Status >= 400 {
			errors[record.Route]++
		}
		if len(result.Recent) < 50 {
			result.Recent = append(result.Recent, record)
		}
	}
	for route, times := range groups {
		sort.Float64s(times)
		total := 0.0
		for _, value := range times {
			total += value
		}
		index := (95*len(times)+99)/100 - 1
		result.Routes = append(result.Routes, RouteStats{Route: route, Count: len(times), Errors: errors[route], AvgMS: total / float64(len(times)), P95MS: times[index]})
	}
	sort.Slice(result.Routes, func(i, j int) bool { return result.Routes[i].Count > result.Routes[j].Count })
	return result
}
