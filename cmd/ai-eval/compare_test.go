package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"turtle-soup/internal/agent"
)

func TestCompareFullReports(t *testing.T) {
	cases, _, err := loadSuite("dev")
	if err != nil {
		t.Fatal(err)
	}
	base := correctReport(t, "dev", cases)
	dir := t.TempDir()
	basePath := saveReport(t, dir, "base", base)
	t.Setenv("DASHSCOPE_API_KEY", "")
	for _, sample := range []struct {
		name   string
		change func(*output)
		issue  string
	}{
		{"相同结果", func(next *output) { next.Model = "other-model" }, ""},
		{"既有报告无范围", func(next *output) { next.Scope = evalScope{} }, "需要同一题集"},
		{"缺少全量结果", func(next *output) { next.Report.Items = next.Report.Items[:2] }, "逐例结果数量"},
		{"样例顺序不同", func(next *output) { next.Report.Items[0].ID = "other" }, "报告结果与题集不一致"},
		{"新增调用失败", func(next *output) {
			next.Report.Items[0].Actual, next.Report.Items[0].Error = "", "timeout"
		}, "调用失败数增加"},
		{"新增误放行", func(next *output) {
			for i, item := range next.Report.Items {
				if item.Kind == "restore" && item.Expected != "正确" {
					next.Report.Items[i].Actual = "正确"
					return
				}
			}
		}, "错误通关数增加"},
		{"漏掉正确还原", func(next *output) {
			for i, item := range next.Report.Items {
				if item.Kind == "restore" && item.Expected == "正确" {
					next.Report.Items[i].Actual = "不完整"
					return
				}
			}
		}, "正确还原被命中的数量减少"},
		{"无有效还原结果", func(next *output) {
			for i, item := range next.Report.Items {
				if item.Kind == "restore" {
					next.Report.Items[i].Actual, next.Report.Items[i].Error = "", "timeout"
				}
			}
		}, "均需要有效判定"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			next := correctReport(t, "dev", cases)
			sample.change(&next)
			nextPath := saveReport(t, dir, "next", next)
			var stdout bytes.Buffer
			err := run([]string{"compare", basePath, nextPath}, &stdout)
			if sample.issue == "" {
				if err != nil || !strings.Contains(stdout.String(), `"passed": true`) {
					t.Fatalf("等价报告应当通过：%v %s", err, stdout.String())
				}
				return
			}
			if err == nil || !strings.Contains(err.Error()+stdout.String(), sample.issue) {
				t.Fatalf("应拒绝本候选：%v %s", err, stdout.String())
			}
		})
	}
}

func TestCompareRefusesMatchingPartialReports(t *testing.T) {
	cases, _, err := loadSuite("dev")
	if err != nil {
		t.Fatal(err)
	}
	report := correctReport(t, "dev", cases[:2])
	report.Scope, err = describeScope("dev", len(cases), cases[:2], nil)
	if err != nil {
		t.Fatal(err)
	}
	path := saveReport(t, t.TempDir(), "partial", report)
	var stdout bytes.Buffer
	if err := run([]string{"compare", path, path}, &stdout); err == nil || stdout.Len() != 0 {
		t.Fatalf("两份相同的抽样报告也不能通过：%v %s", err, stdout.String())
	}
}

func correctReport(t *testing.T, suite string, cases []agent.EvalCase) output {
	t.Helper()
	scope, err := describeScope(suite, len(cases), cases, nil)
	if err != nil {
		t.Fatal(err)
	}
	report := agent.EvalReport{Total: len(cases), Correct: len(cases)}
	for _, sample := range cases {
		report.Items = append(report.Items, agent.EvalItem{ID: sample.ID, Kind: sample.Kind, Expected: sample.Expected, Actual: sample.Expected})
	}
	return output{Model: "fake-model", Scope: scope, Report: report}
}

func saveReport(t testing.TB, dir, name string, report output) string {
	t.Helper()
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name+".json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
