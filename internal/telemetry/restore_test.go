package telemetry

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// 日志恢复按写入顺序取尾部，时间字段只供展示。
func TestRestoreTraceOrderAndLimit(t *testing.T) {
	t.Run("rotation_keeps_append_order", func(t *testing.T) {
		previous := restoreLine(t, "old-first", 4, "") + "\n" + restoreLine(t, "old-last", 3, "") + "\n"
		current := restoreLine(t, "new-first", 2, "") + "\n" + restoreLine(t, "new-last", 1, "") + "\n"
		store := openRestoreLogs(t, previous, current)
		checkRestoreIDs(t, store, []string{"old-first", "old-last", "new-first", "new-last"})
		recent := store.Snapshot().Recent
		if len(recent) != 4 || recent[0].TraceID != "new-last" || recent[3].TraceID != "old-first" {
			t.Fatalf("近期请求未按写入逆序展示：%+v", recent)
		}
	})
	t.Run("rotation_keeps_only_last_300", func(t *testing.T) {
		oldIDs, previous := numberedRestoreLines(t, "old", 45)
		newIDs, current := numberedRestoreLines(t, "new", 270)
		store := openRestoreLogs(t, previous, current)
		checkRestoreIDs(t, store, append(oldIDs[15:], newIDs...))
	})
	t.Run("full_current_needs_no_previous", func(t *testing.T) {
		previous := restoreLine(t, "do-not-restore", 999, "") + "\n"
		ids, current := numberedRestoreLines(t, "current", 305)
		checkRestoreIDs(t, openRestoreLogs(t, previous, current), ids[5:])
	})
}

// 缺少行尾的旧记录不应与第一次重启后写入的新记录粘在一起。
func TestRestoreTraceAppendAfterIncompleteTail(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name  string
		tail  string
		valid bool
	}{
		{"complete", restoreLine(t, "tail", 3, ""), true},
		{"partial", `{"traceId":"half-written","spans":[`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			previous := restoreLine(t, "old", 1, "")
			current := restoreLine(t, "first", 2, "") + "\n" + test.tail
			store := openRestoreLogs(t, previous, current)
			want := []string{"old", "first"}
			if test.valid {
				want = append(want, "tail")
			}
			checkRestoreIDs(t, store, want)

			router := gin.New()
			router.Use(store.Middleware())
			router.GET("/health", func(c *gin.Context) { c.Status(http.StatusNoContent) })
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health", nil))
			traceID := response.Header().Get("X-Trace-ID")
			if response.Code != http.StatusNoContent || len(traceID) != 32 {
				t.Fatalf("新记录未取得追踪标识：HTTP %d", response.Code)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}

			reopened, err := NewStore(store.dir)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			checkRestoreIDs(t, reopened, append(want, traceID))
			old, err := os.ReadFile(filepath.Join(store.dir, "requests.previous.jsonl"))
			if err != nil || string(old) != previous {
				t.Fatalf("恢复不应改变上一代文件，读取错误：%v", err)
			}
		})
	}
}

// 兼顾旧日志、意外半写和跨读块记录。
func TestRestoreTraceLineBoundaries(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		checkRestoreIDs(t, openRestoreLogs(t, "", ""), nil)
	})
	t.Run("previous_file_only", func(t *testing.T) {
		previous := restoreLine(t, "previous", 1, "") + "\n"
		checkRestoreIDs(t, openRestoreLogs(t, previous, ""), []string{"previous"})
	})
	t.Run("crlf_and_invalid_rows", func(t *testing.T) {
		first := restoreLine(t, "first", 2, "")
		last := restoreLine(t, "last", 1, "")
		current := strings.Join([]string{"", first, "{}", "{invalid}", last, ""}, "\r\n")
		checkRestoreIDs(t, openRestoreLogs(t, "", current), []string{"first", "last"})
	})
	t.Run("partial_tail", func(t *testing.T) {
		current := restoreLine(t, "complete", 1, "") + "\n" + `{"traceId":"half-written","spans":[`
		checkRestoreIDs(t, openRestoreLogs(t, "", current), []string{"complete"})
	})
	t.Run("valid_tail_without_newline", func(t *testing.T) {
		current := restoreLine(t, "first", 1, "") + "\n" + restoreLine(t, "last", 2, "")
		checkRestoreIDs(t, openRestoreLogs(t, "", current), []string{"first", "last"})
	})
	t.Run("line_spanning_blocks", func(t *testing.T) {
		name := strings.Repeat("长", 70001)
		current := restoreLine(t, "first", 3, "") + "\n" + restoreLine(t, "long", 2, name) + "\n" + restoreLine(t, "last", 1, "") + "\n"
		store := openRestoreLogs(t, "", current)
		checkRestoreIDs(t, store, []string{"first", "long", "last"})
		if spans := store.records[1].Spans; len(spans) != 1 || spans[0].Name != name {
			t.Fatal("跨块拼接未还原完整阶段字段")
		}
	})
	t.Run("oversized_corruption_is_skipped", func(t *testing.T) {
		old := restoreLine(t, "old", 4, "") + "\n"
		first := restoreLine(t, "first", 3, "")
		last := restoreLine(t, "last", 1, "")
		tooLong := strings.Repeat("x", (1<<20)+17)
		current := strings.Join([]string{first, tooLong, last, tooLong, ""}, "\n")
		checkRestoreIDs(t, openRestoreLogs(t, old, current), []string{"old", "first", "last"})
	})
}

func openRestoreLogs(t *testing.T, previous, current string) *Store {
	t.Helper()
	dir := t.TempDir()
	if previous != "" {
		if err := os.WriteFile(filepath.Join(dir, "requests.previous.jsonl"), []byte(previous), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if current != "" {
		if err := os.WriteFile(filepath.Join(dir, "requests.jsonl"), []byte(current), 0600); err != nil {
			t.Fatal(err)
		}
	}
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func restoreLine(t *testing.T, id string, second int64, span string) string {
	t.Helper()
	record := Record{TraceID: id, Time: time.Unix(second, 0).UTC(), Route: "/health"}
	if span != "" {
		record.Spans = []Span{{Name: span, DurationMS: 0.25}}
	}
	line, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	return string(line)
}

func numberedRestoreLines(t *testing.T, prefix string, count int) ([]string, string) {
	t.Helper()
	ids := make([]string, 0, count)
	var data strings.Builder
	for i := range count {
		id := fmt.Sprintf("%s-%03d", prefix, i)
		ids = append(ids, id)
		data.WriteString(restoreLine(t, id, int64(count-i), ""))
		data.WriteByte('\n')
	}
	return ids, data.String()
}

func checkRestoreIDs(t *testing.T, store *Store, want []string) {
	t.Helper()
	ids := make([]string, 0, len(store.records))
	for _, record := range store.records {
		ids = append(ids, record.TraceID)
	}
	if !slices.Equal(ids, want) {
		t.Fatalf("恢复的 Trace ID 为 %v，期望 %v", ids, want)
	}
}
