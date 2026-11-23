package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"turtle-soup/internal/agent"
)

type boundaryCLIRun struct {
	path    string
	url     string
	data    []byte
	count   *atomic.Int32
	elapsed time.Duration
}

type boundaryCLIGroup struct {
	Group                  string `json:"group"`
	Total                  int    `json:"total"`
	Correct                int    `json:"correct"`
	MissingDecisions       int    `json:"missingDecisions"`
	InvalidOutput          int    `json:"invalidOutput"`
	ExpectedCorrectRestore int    `json:"expectedCorrectRestore"`
	MissedCorrectRestore   int    `json:"missedCorrectRestore"`
	NonCorrectRestore      int    `json:"nonCorrectRestore"`
	FalseCorrect           int    `json:"falseCorrect"`
}

type boundaryCLIReportFields struct {
	Groups []boundaryCLIGroup `json:"boundaryGroups"`
}

// 用真实命令生成分组报告并检查比较结果，所有模型调用均走本机回环。
func TestAIEvalBoundaryCLI(t *testing.T) {
	binary := os.Getenv("AI_EVAL_BINARY")
	if binary == "" {
		t.Skip("需要设置 AI_EVAL_BINARY 指向已构建的 ai-eval")
	}
	cases, groupByID, err := agent.LoadBoundaryCases()
	if err != nil {
		t.Fatal(err)
	}
	dev, err := agent.LoadEvalCases()
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 36 || len(dev) != 144 {
		t.Fatalf("评测范围变化：边界 %d 条，开发集 %d 条", len(cases), len(dev))
	}
	wrong := slices.IndexFunc(cases, func(sample agent.EvalCase) bool {
		return sample.Kind == "restore" && sample.Expected != "正确"
	})
	if wrong < 0 {
		t.Fatal("边界评测缺少非正确还原样例")
	}
	malformed := slices.IndexFunc(cases, func(sample agent.EvalCase) bool {
		return sample.Kind == "restore" && sample.Expected == "正确" && groupByID[sample.ID] != groupByID[cases[wrong].ID]
	})
	if malformed < 0 {
		t.Fatal("边界评测缺少另一分组的正确还原样例")
	}

	base := runBoundaryCLIReport(t, binary, "boundary", "fixture-boundary-base", cases, groupByID, -1, -1)
	wantBase := expectedBoundaryCLIGroups(cases, groupByID, -1, -1)
	if len(wantBase) != 4 {
		t.Fatalf("边界故事分组数应为 4，实际 %d", len(wantBase))
	}
	assertBoundaryCLIGroups(t, base.data, wantBase)
	wrongGroup := groupByID[cases[wrong].ID]
	malformedGroup := groupByID[cases[malformed].ID]
	for _, variant := range []struct {
		name      string
		wrong     int
		malformed int
		issues    []string
	}{
		{name: "fixture-boundary-equivalent", wrong: -1, malformed: -1},
		{name: "fixture-boundary-wrong-label", wrong: wrong, malformed: -1, issues: []string{
			"错误通关数增加", "命中人工标签的总数减少", wrongGroup + "：错误通关数增加",
		}},
		{name: "fixture-boundary-invalid-json", wrong: -1, malformed: malformed, issues: []string{
			"调用失败数增加", "正确还原被命中的数量减少", "命中人工标签的总数减少",
			malformedGroup + "：无效输出数增加", malformedGroup + "：正确还原被命中的数量减少",
		}},
	} {
		t.Run(variant.name, func(t *testing.T) {
			next := runBoundaryCLIReport(t, binary, "boundary", variant.name, cases, groupByID, variant.wrong, variant.malformed)
			wantNext := expectedBoundaryCLIGroups(cases, groupByID, variant.wrong, variant.malformed)
			assertBoundaryCLIGroups(t, next.data, wantNext)
			compareBoundaryCLI(t, binary, base, next, wantBase, wantNext, variant.issues)
		})
	}
	wide := runBoundaryCLIReport(t, binary, "dev", "fixture-boundary-dev-scope", dev, nil, -1, -1)
	assertBoundaryCLIGroups(t, wide.data, nil)
	t.Logf("评测范围对照：boundary_requests=%d boundary_ms=%.3f dev_requests=%d dev_ms=%.3f",
		base.count.Load(), float64(base.elapsed)/float64(time.Millisecond), wide.count.Load(), float64(wide.elapsed)/float64(time.Millisecond))
}

func expectedBoundaryCLIGroups(cases []agent.EvalCase, groupByID map[string]string, wrong, malformed int) []boundaryCLIGroup {
	index := make(map[string]int)
	var groups []boundaryCLIGroup
	for i, sample := range cases {
		group := groupByID[sample.ID]
		position, exists := index[group]
		if !exists {
			position = len(groups)
			index[group] = position
			groups = append(groups, boundaryCLIGroup{Group: group})
		}
		row := &groups[position]
		row.Total++
		if i != wrong && i != malformed {
			row.Correct++
		}
		if sample.Kind == "restore" && sample.Expected == "正确" {
			row.ExpectedCorrectRestore++
		}
		if sample.Kind == "restore" && sample.Expected != "正确" {
			row.NonCorrectRestore++
		}
		if i == wrong {
			row.FalseCorrect++
		}
		if i == malformed {
			row.MissingDecisions++
			row.InvalidOutput++
			row.MissedCorrectRestore++
		}
	}
	return groups
}

func assertBoundaryCLIGroups(t *testing.T, data []byte, want []boundaryCLIGroup) {
	t.Helper()
	if want == nil && bytes.Contains(data, []byte(`"boundaryGroups"`)) {
		t.Fatal("非边界题集改变了原来的报告字段")
	}
	var fields boundaryCLIReportFields
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(fields.Groups, want) {
		t.Fatalf("分组报告不符：得到 %+v，预期 %+v", fields.Groups, want)
	}
}

func compareBoundaryCLI(t *testing.T, binary string, base, next boundaryCLIRun, wantBase, wantNext []boundaryCLIGroup, issues []string) {
	t.Helper()
	before := next.count.Load()
	code, stdout, stderr := runHarnessCLI(t, binary, next.url, "boundary-compare", "", "compare", base.path, next.path)
	if next.count.Load() != before {
		t.Fatal("报告比较阶段调用了模型")
	}
	passed := len(issues) == 0
	if passed && (code != 0 || stderr != "") || !passed && (code != 1 || !strings.Contains(stderr, "候选未通过质量对照")) {
		t.Fatalf("报告比较退出结果不符（%d）：%s", code, stderr)
	}
	var result struct {
		Passed    bool                    `json:"passed"`
		Issues    []string                `json:"issues"`
		Baseline  boundaryCLIReportFields `json:"baseline"`
		Candidate boundaryCLIReportFields `json:"candidate"`
	}
	if err := json.Unmarshal(stdout, &result); err != nil {
		t.Fatal(err)
	}
	if result.Passed != passed || passed && len(result.Issues) > 0 {
		t.Fatalf("报告比较状态不符：%v，原因 %v", result.Passed, result.Issues)
	}
	for _, issue := range issues {
		if !slices.Contains(result.Issues, issue) {
			t.Fatalf("报告缺少原因 %q，得到 %v", issue, result.Issues)
		}
	}
	if !slices.Equal(result.Baseline.Groups, wantBase) || !slices.Equal(result.Candidate.Groups, wantNext) {
		t.Fatalf("比较报告未保留双方分组指标：基线 %+v，候选 %+v", result.Baseline.Groups, result.Candidate.Groups)
	}
}

// 回环服务复用既有模型夹具，只改变指定样例的判定或返回格式。
func runBoundaryCLIReport(t *testing.T, binary, suite, name string, cases []agent.EvalCase, groups map[string]string, wrong, malformed int) boundaryCLIRun {
	t.Helper()
	model := &harnessModel{name: name}
	if wrong >= 0 {
		model.changeID = cases[wrong].ID
		model.result = "正确"
	}
	models := map[string]*harnessModel{name: model}
	count := new(atomic.Int32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		index := int(count.Add(1)) - 1
		if index != malformed {
			replayHarnessModel(t, w, r, cases, models)
			return
		}
		// 先走既有请求校验，再模拟判定 JSON 不完整的普通故障。
		reply := httptest.NewRecorder()
		replayHarnessModel(t, reply, r, cases, models)
		if reply.Code != http.StatusOK {
			w.WriteHeader(reply.Code)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"choices":[{"finish_reason":"stop","message":{"content":"{"}}]}`)
	}))
	t.Cleanup(server.Close)

	path := filepath.Join(t.TempDir(), name+".json")
	start := time.Now()
	code, stdout, stderr := runHarnessCLI(t, binary, server.URL, name, harnessKey, "-suite", suite, "-timeout", "8s", "-out", path)
	elapsed := time.Since(start)
	if code != 0 || len(stdout) != 0 || stderr != "" {
		t.Fatalf("%s 生成报告失败（%d）：%s", suite, code, stderr)
	}
	report, err := readOutput(path)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := describeScope(suite, len(cases), cases, groups)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := suiteHash(scope, cases, groups)
	if err != nil {
		t.Fatal(err)
	}
	if report.Scope != scope || report.EvalSetHash != hash || report.Model != name || report.PromptVersion != agent.PromptVersion ||
		report.Report.Total != len(cases) || len(report.Report.Items) != len(cases) || int(count.Load()) != len(cases) {
		t.Fatalf("%s 的报告范围、版本或调用数不符，调用数=%d", suite, count.Load())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(harnessKey)) {
		t.Fatal("评测报告包含夹具密钥")
	}
	for i, sample := range cases {
		if bytes.Contains(data, []byte(sample.Soup.Answer)) {
			t.Fatalf("报告包含第 %d 条原始汤底", i+1)
		}
		label, errorKind := sample.Expected, ""
		if i == wrong {
			label = model.result
		}
		if i == malformed {
			label, errorKind = "", "invalid_output"
		}
		item := report.Report.Items[i]
		if item.ID != sample.ID || item.Kind != sample.Kind || item.Expected != sample.Expected || item.Actual != label || item.Error != errorKind {
			t.Fatalf("第 %d 条评测判定不符：实际 %q，错误类型 %q", i+1, item.Actual, item.Error)
		}
	}
	return boundaryCLIRun{path: path, url: server.URL, data: data, count: count, elapsed: elapsed}
}
