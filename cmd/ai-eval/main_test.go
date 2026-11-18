package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"turtle-soup/internal/agent"
)

func TestRunWithLocalModelReplay(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer local-secret" {
			t.Errorf("模型请求地址或鉴权错误：%s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"qwen-test","choices":[{"finish_reason":"stop","message":{"content":"{\"result\":\"是\"}"}}],"usage":{"prompt_tokens":12,"completion_tokens":2}}`))
	}))
	defer server.Close()
	t.Setenv("DASHSCOPE_API_KEY", "local-secret")
	t.Setenv("QWEN_BASE_URL", server.URL)
	t.Setenv("QWEN_MODEL", "qwen-local-secret")
	var stdout bytes.Buffer
	if err := run([]string{"-limit", "2", "-timeout", "5s"}, &stdout); err != nil {
		t.Fatal(err)
	}
	var result output
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || result.Report.Total != 2 || result.Report.Errors != 0 || len(result.EvalSetHash) != 64 {
		t.Fatalf("限制样例或报告内容错误：calls=%d report=%+v", calls.Load(), result.Report)
	}
	if result.Scope.Suite != "dev" || result.Scope.Complete || result.Scope.Selected != 2 || result.Scope.Available <= 2 ||
		len(result.Scope.SelectedHash) != 64 || result.EvalSetHash != agent.EvalSetHash() {
		t.Fatalf("抽样报告错误：%+v", result.Scope)
	}
	if strings.Contains(stdout.String(), "local-secret") || strings.Contains(stdout.String(), "小林是剧院灯光师") || !strings.Contains(result.Model, "[REDACTED]") {
		t.Fatal("报告泄露了密钥或汤底")
	}

	path := filepath.Join(t.TempDir(), "eval.json")
	if err := run([]string{"-limit", "1", "-out", path}, &stdout); err != nil {
		t.Fatal(err)
	}
	file, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 || strings.Contains(string(file), "local-secret") || strings.Contains(string(file), "小林是剧院灯光师") {
		t.Fatal("文件报告内容错误或包含敏感信息")
	}
}

func TestRunRejectsInvalidOptions(t *testing.T) {
	for _, args := range [][]string{
		{"-limit", "-1"},
		{"-timeout", "0s"},
		{"-out", ""},
		{"-suite", "invalid"},
		{"-unknown"},
	} {
		if err := run(args, &bytes.Buffer{}); err == nil {
			t.Fatalf("无效参数被接受：%v", args)
		}
	}
	t.Setenv("DASHSCOPE_API_KEY", "")
	if err := run([]string{"-limit", "1"}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "DASHSCOPE_API_KEY") {
		t.Fatalf("缺少密钥未被拒绝：%v", err)
	}
}

func TestRunWithHoldout(t *testing.T) {
	cases, err := agent.LoadHoldoutCases()
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := int(calls.Add(1)) - 1
		if i >= len(cases) {
			t.Error("请求数超过验证集大小")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		content := fmt.Sprintf(`{"result":%q}`, cases[i].Expected)
		fmt.Fprintf(w, `{"choices":[{"finish_reason":"stop","message":{"content":%q}}]}`, content)
	}))
	defer server.Close()
	t.Setenv("DASHSCOPE_API_KEY", "local-secret")
	t.Setenv("QWEN_BASE_URL", server.URL)
	var stdout bytes.Buffer
	if err := run([]string{"-suite", "holdout", "-limit", "999"}, &stdout); err != nil {
		t.Fatal(err)
	}
	var result output
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Scope.Suite != "holdout" || !result.Scope.Complete || result.Scope.Selected != len(cases) ||
		result.Scope.SelectedHash != result.EvalSetHash || result.Report.Correct != len(cases) || int(calls.Load()) != len(cases) {
		t.Fatalf("验证集全量报告错误：%+v，命中=%d，调用=%d", result.Scope, result.Report.Correct, calls.Load())
	}
	for _, sample := range cases {
		if strings.Contains(stdout.String(), sample.Soup.Answer) {
			t.Fatalf("报告包含验证样例 %s 的汤底", sample.ID)
		}
	}
}

func TestRunTimesOut(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(30 * time.Millisecond)
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":"{\"result\":\"是\"}"}}]}`))
	}))
	defer server.Close()
	t.Setenv("DASHSCOPE_API_KEY", "local-secret")
	t.Setenv("QWEN_BASE_URL", server.URL)
	var stdout bytes.Buffer
	if err := run([]string{"-limit", "1", "-timeout", "1ms"}, &stdout); err == nil || stdout.Len() != 0 {
		t.Fatalf("超时后仍输出不完整报告：%v %s", err, stdout.String())
	}
}
