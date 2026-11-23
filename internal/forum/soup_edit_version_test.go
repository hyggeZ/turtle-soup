package forum_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type soupEditResult struct {
	Title   string `json:"title"`
	Surface string `json:"surface"`
	Answer  string `json:"answer"`
	Version int64  `json:"version"`
	Code    string `json:"code"`
}

func soupEditInput(title string, version any) map[string]any {
	return map[string]any{
		"title": title, "surface": title + "的完整汤面", "answer": title + "的完整汤底",
		"genre": "本格", "difficulty": 2, "horror": 1, "version": version,
	}
}

func readSoupEditResult(t *testing.T, body []byte) soupEditResult {
	t.Helper()
	var result soupEditResult
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("解析编辑响应：%v，响应 %s", err, body)
	}
	return result
}

func TestSoupEditVersionPreconditions(t *testing.T) {
	f := newDeletionFixture(t, nil, nil)
	id := f.soup()
	path := fmt.Sprintf("/api/soups/%d", id)
	owner := f.call(http.MethodGet, path, 0, nil)
	original := readSoupEditResult(t, owner.Body.Bytes())
	if owner.Code != http.StatusOK || original.Version != 1 || original.Answer == "" {
		t.Fatalf("作者没有读到默认版本与汤底：%d %+v", owner.Code, original)
	}
	if rule := owner.Header().Get("Cache-Control"); rule != "private, no-store" {
		t.Fatalf("作者详情缺少缓存隔离：%q", rule)
	}
	for _, view := range []struct {
		path string
		user int
	}{{path, -1}, {path, 1}, {"/api/soups", 0}} {
		res := f.call(http.MethodGet, view.path, view.user, nil)
		if res.Code != http.StatusOK || bytes.Contains(res.Body.Bytes(), []byte(`"version"`)) ||
			strings.Contains(res.Body.String(), original.Answer) {
			t.Fatalf("非编辑读取暴露了版本或汤底：%d %s", res.Code, res.Body.String())
		}
	}
	input := soupEditInput("编辑草稿", original.Version)
	delete(input, "version")
	missing := f.call(http.MethodPatch, path, 0, input)
	if missing.Code != http.StatusPreconditionRequired || readSoupEditResult(t, missing.Body.Bytes()).Code != "SOUP_VERSION_REQUIRED" {
		t.Fatalf("缺少版本未触发前置条件：%d %s", missing.Code, missing.Body.String())
	}
	for _, version := range []any{nil, 0, -1, "1", 1.25, json.Number("9223372036854775808")} {
		invalid := f.call(http.MethodPatch, path, 0, soupEditInput("编辑草稿", version))
		if invalid.Code != http.StatusBadRequest {
			t.Fatalf("非法版本 %v 返回 %d：%s", version, invalid.Code, invalid.Body.String())
		}
	}
	forbidden := f.call(http.MethodPatch, path, 1, soupEditInput("非作者修改", int64(99)))
	if forbidden.Code != http.StatusForbidden || readSoupEditResult(t, forbidden.Body.Bytes()).Code != "" {
		t.Fatalf("非作者可以尝试编辑版本：%d %s", forbidden.Code, forbidden.Body.String())
	}
	ctx := context.Background()
	if _, err := f.db.Exec(ctx, `UPDATE soups SET hidden_at=NOW(),hidden_by=$2 WHERE id=$1`, id, f.users[1]); err != nil {
		t.Fatal(err)
	}
	hidden := f.call(http.MethodPatch, path, 0, soupEditInput("已隐藏的草稿", int64(99)))
	if hidden.Code != http.StatusConflict || readSoupEditResult(t, hidden.Body.Bytes()).Code != "" {
		t.Fatalf("隐藏题的审核限制失效：%d %s", hidden.Code, hidden.Body.String())
	}
	if _, err := f.db.Exec(ctx, `UPDATE soups SET hidden_at=NULL,hidden_by=NULL WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	f.room(id, "active")
	active := f.call(http.MethodPatch, path, 0, soupEditInput("对局期间的草稿", original.Version))
	if active.Code != http.StatusConflict || readSoupEditResult(t, active.Body.Bytes()).Code != "" {
		t.Fatalf("活跃对局的编辑限制失效：%d %s", active.Code, active.Body.String())
	}
	after := f.call(http.MethodGet, path, 0, nil)
	if result := readSoupEditResult(t, after.Body.Bytes()); after.Code != http.StatusOK || result != original {
		t.Fatalf("被拒绝的保存更改了题目：%d %+v", after.Code, result)
	}
}

func TestSoupEditVersionConcurrentHTTP(t *testing.T) {
	for _, size := range []int{8, 16} {
		t.Run(fmt.Sprintf("编辑者%d", size), func(t *testing.T) {
			f := newDeletionFixture(t, nil, nil)
			path := fmt.Sprintf("/api/soups/%d", f.soup())
			before := f.call(http.MethodGet, path, 0, nil)
			startVersion := readSoupEditResult(t, before.Body.Bytes()).Version
			if before.Code != http.StatusOK || startVersion != 1 {
				t.Fatalf("无法取得初始编辑版本：%d %d", before.Code, startVersion)
			}
			// 每个编辑者都已经读取同一个版本，在同一个屏障后通过 HTTP 保存不同正文。
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			type editResponse struct {
				status int
				body   []byte
				err    error
			}
			responses := make([]editResponse, size)
			requests := make([]*http.Request, size)
			for i := range size {
				data, err := json.Marshal(soupEditInput(fmt.Sprintf("并发草稿%d", i), startVersion))
				if err != nil {
					t.Fatal(err)
				}
				req, err := http.NewRequestWithContext(ctx, http.MethodPatch, f.server.URL+path, bytes.NewReader(data))
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Content-Type", "application/json")
				req.AddCookie(f.cookies[0])
				requests[i] = req
			}
			start := make(chan struct{})
			var ready, done sync.WaitGroup
			ready.Add(size)
			done.Add(size)
			for i, req := range requests {
				go func() {
					defer done.Done()
					ready.Done()
					<-start
					res, err := f.server.Client().Do(req)
					if err != nil {
						responses[i].err = err
						return
					}
					defer res.Body.Close()
					body, err := io.ReadAll(res.Body)
					responses[i] = editResponse{status: res.StatusCode, body: body, err: err}
				}()
			}
			ready.Wait()
			close(start)
			done.Wait()
			var saved soupEditResult
			success, conflicts := 0, 0
			for i, response := range responses {
				if response.err != nil {
					t.Fatal(response.err)
				}
				result := readSoupEditResult(t, response.body)
				switch response.status {
				case http.StatusOK:
					success++
					saved = result
					if result.Title != fmt.Sprintf("并发草稿%d", i) || result.Version != startVersion+1 {
						t.Fatalf("保存返回错误的正文或版本：%+v", result)
					}
				case http.StatusConflict:
					conflicts++
					if result.Code != "SOUP_VERSION_CONFLICT" || result.Version != 0 {
						t.Fatalf("版本冲突响应不符：%+v", result)
					}
				default:
					t.Fatalf("并发保存返回 %d：%s", response.status, response.body)
				}
			}
			if success != 1 || conflicts != size-1 {
				t.Fatalf("%d 路保存成功 %d 次，冲突 %d 次", size, success, conflicts)
			}
			current := f.call(http.MethodGet, path, 0, nil)
			result := readSoupEditResult(t, current.Body.Bytes())
			if current.Code != http.StatusOK || result != saved || result.Surface != saved.Title+"的完整汤面" || result.Answer != saved.Title+"的完整汤底" {
				t.Fatalf("落库结果与唯一成功写入不一致：%d %+v", current.Code, result)
			}
			retry := f.call(http.MethodPatch, path, 0, soupEditInput("重新核对后保存", result.Version))
			next := readSoupEditResult(t, retry.Body.Bytes())
			if retry.Code != http.StatusOK || next.Version != startVersion+2 || next.Title != "重新核对后保存" {
				t.Fatalf("读取新版本后无法重试：%d %+v", retry.Code, next)
			}
		})
	}
}
