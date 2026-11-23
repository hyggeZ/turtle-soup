package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"turtle-soup/internal/agent"
)

const harnessKey = "offline-quality-fixture-key"

type harnessModel struct {
	name, changeID, result, issue string
	fail                          bool
	falseCorrect, correctRestores int
	calls                         atomic.Int32
}

type harnessComparison struct {
	Scope     evalScope `json:"scope"`
	Baseline  evalScore `json:"baseline"`
	Candidate evalScore `json:"candidate"`
	Passed    bool      `json:"passed"`
	Issues    []string  `json:"issues"`
}

// 通过实际进程检验报告生成、质量门槛和退出码，模型只走本地假服务。
func TestAIEvalCLIQualityGate(t *testing.T) {
	binary := os.Getenv("AI_EVAL_BINARY")
	if binary == "" {
		t.Skip("需要设置 AI_EVAL_BINARY 指向已构建的 ai-eval")
	}
	cases, err := agent.LoadHoldoutCases()
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 24 {
		t.Fatalf("独立验证集应有 24 条样例，得到 %d 条", len(cases))
	}
	scope, err := describeScope("holdout", len(cases), cases)
	if err != nil {
		t.Fatal(err)
	}
	base := &harnessModel{name: "qwen-fixture-base-v1", correctRestores: 4}
	candidates := []*harnessModel{
		{name: "qwen-fixture-equivalent-v1", correctRestores: 4},
		{name: "qwen-fixture-provider-failure-v1", changeID: "holdout_clock_question_yes", fail: true, issue: "调用失败数增加", correctRestores: 4},
		{name: "qwen-fixture-false-pass-v1", changeID: "holdout_clock_restore_wrong", result: "正确", issue: "错误通关数增加", falseCorrect: 1, correctRestores: 4},
		{name: "qwen-fixture-missed-restore-v1", changeID: "holdout_clock_restore_correct", result: "不完整", issue: "正确还原被命中的数量减少", correctRestores: 3},
	}
	models := map[string]*harnessModel{base.name: base}
	for _, candidate := range candidates {
		models[candidate.name] = candidate
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		replayHarnessModel(t, w, r, cases, models)
	}))
	defer server.Close()
	dir := t.TempDir()
	basePath, baseReport := runHarnessReport(t, binary, server.URL, dir, base, scope, cases)
	for _, candidate := range candidates {
		t.Run(candidate.name, func(t *testing.T) {
			path, report := runHarnessReport(t, binary, server.URL, dir, candidate, scope, cases)
			before := requests.Load()
			// 比较阶段只读两份报告，清除密钥并保持上游地址为回环服务。
			code, stdout, stderr := runHarnessCLI(t, binary, server.URL, candidate.name, "", "compare", basePath, path)
			if requests.Load() != before {
				t.Fatal("比较报告时向模型发出了请求")
			}
			passed := candidate.issue == ""
			var issues []string
			if passed {
				if code != 0 || stderr != "" {
					t.Fatalf("同等质量候选比较失败（%d）：%s", code, stderr)
				}
			} else {
				issues = []string{candidate.issue, "命中人工标签的总数减少"}
				if code != 1 || !strings.Contains(stderr, "候选未通过质量对照") {
					t.Fatalf("退化候选未正确退出（%d）：%s", code, stderr)
				}
			}
			var compared harnessComparison
			if err := json.Unmarshal(stdout, &compared); err != nil {
				t.Fatalf("解析比较报告失败：%v", err)
			}
			if compared.Scope != scope || compared.Passed != passed || !slices.Equal(compared.Issues, issues) {
				t.Fatalf("比较结果不符，passed=%v，issues=%v", compared.Passed, compared.Issues)
			}
			checkHarnessScore(t, compared.Baseline, baseReport, base)
			checkHarnessScore(t, compared.Candidate, report, candidate)
		})
	}
	if got, want := int(requests.Load()), len(models)*len(cases); got != want {
		t.Fatalf("本地模型调用 %d 次，期望 %d 次", got, want)
	}
	t.Logf("5 份完整报告，120 次本地模型请求；4 次比较新增模型请求 0 次；3 类退化均被拒绝")
}

func replayHarnessModel(t *testing.T, w http.ResponseWriter, r *http.Request, cases []agent.EvalCase, models map[string]*harnessModel) {
	t.Helper()
	var request struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if r.Method != http.MethodPost || r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer "+harnessKey ||
		json.NewDecoder(r.Body).Decode(&request) != nil || len(request.Messages) != 2 || request.Messages[0].Role != "system" ||
		request.Messages[0].Content == "" || request.Messages[1].Role != "user" {
		t.Error("模型请求的路径、凭证或消息结构不符")
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	model := models[request.Model]
	if model == nil {
		t.Error("收到未配置的测试模型")
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	index := int(model.calls.Add(1)) - 1
	var input struct {
		Soup  agent.Soup `json:"soup"`
		Input string     `json:"input"`
	}
	if index >= len(cases) || json.Unmarshal([]byte(request.Messages[1].Content), &input) != nil ||
		input.Soup != cases[index].Soup || input.Input != cases[index].Input {
		t.Errorf("模型 %s 的第 %d 条请求与独立验证集不符", model.name, index+1)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	sample := cases[index]
	result := sample.Expected
	if sample.ID == model.changeID {
		if model.fail {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		result = model.result
	}
	decision := fmt.Sprintf(`{"result":%q}`, result)
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"model":%q,"choices":[{"finish_reason":"stop","message":{"content":%q}}],"usage":{"prompt_tokens":12,"completion_tokens":2}}`, model.name, decision)
}

func runHarnessReport(t *testing.T, binary, url, dir string, model *harnessModel, scope evalScope, cases []agent.EvalCase) (string, output) {
	t.Helper()
	path := filepath.Join(dir, model.name+".json")
	code, stdout, stderr := runHarnessCLI(t, binary, url, model.name, harnessKey, "-suite", "holdout", "-timeout", "8s", "-out", path)
	if code != 0 || len(stdout) != 0 || stderr != "" {
		t.Fatalf("写评测报告失败（%d）：%s", code, stderr)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report output
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if report.Scope != scope || report.EvalSetHash != scope.SelectedHash || report.Model != model.name || report.PromptVersion != agent.PromptVersion ||
		report.Report.Total != len(cases) || len(report.Report.Items) != len(cases) || int(model.calls.Load()) != len(cases) {
		t.Fatalf("报告版本、范围或样例数量错误：模型 %s，提示词 %s，范围 %+v", report.Model, report.PromptVersion, report.Scope)
	}
	if bytes.Contains(data, []byte(harnessKey)) {
		t.Fatal("报告包含假模型密钥")
	}
	for i, sample := range cases {
		if bytes.Contains(data, []byte(sample.Soup.Answer)) {
			t.Fatalf("报告包含样例 %s 的汤底", sample.ID)
		}
		result, errorKind := sample.Expected, ""
		if sample.ID == model.changeID {
			result = model.result
			if model.fail {
				errorKind = "provider_unavailable"
			}
		}
		item := report.Report.Items[i]
		if item.ID != sample.ID || item.Kind != sample.Kind || item.Expected != sample.Expected || item.Actual != result || item.Error != errorKind {
			t.Fatalf("逐例报告与已知判定不符：%s", sample.ID)
		}
	}
	return path, report
}

func runHarnessCLI(t *testing.T, binary, url, model, key string, args ...string) (int, []byte, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = append(os.Environ(), "QWEN_BASE_URL="+url, "QWEN_MODEL="+model, "DASHSCOPE_API_KEY="+key)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err == nil {
		return 0, stdout, stderr.String()
	}
	var exit *exec.ExitError
	if ctx.Err() != nil || !errors.As(err, &exit) {
		t.Fatalf("运行 ai-eval 二进制失败：%v", err)
	}
	return exit.ExitCode(), stdout, stderr.String()
}

func checkHarnessScore(t *testing.T, score evalScore, report output, model *harnessModel) {
	t.Helper()
	correct, answered, failures := report.Scope.Selected, report.Scope.Selected, 0
	if model.changeID != "" {
		correct--
	}
	if model.fail {
		answered--
		failures++
	}
	if score.Model != model.name || score.PromptVersion != agent.PromptVersion || score.Correct != correct || score.Errors != failures ||
		score.FalseCorrect != model.falseCorrect || score.CorrectRestores != model.correctRestores ||
		score.PromptTokens != 12*answered || score.CompletionTokens != 2*answered {
		t.Fatalf("比较报告统计不符：模型 %s，正确 %d，调用失败 %d，误通关 %d，正确还原 %d，Token %d/%d",
			score.Model, score.Correct, score.Errors, score.FalseCorrect, score.CorrectRestores, score.PromptTokens, score.CompletionTokens)
	}
}
