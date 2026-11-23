package main

import (
	"bytes"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"

	"turtle-soup/internal/agent"
)

func TestBoundaryGroupReportCounts(t *testing.T) {
	cases, groups, err := loadSuite("boundary")
	if err != nil {
		t.Fatal(err)
	}
	items := correctReport(t, "boundary", cases).Report.Items
	for i := range items {
		item := &items[i]
		switch item.ID {
		case "boundary_weather_restore_correct_inject":
			item.Actual, item.Error = "", "invalid_output"
		case "boundary_weather_restore_partial":
			item.Actual = "正确"
		case "boundary_museum_no":
			item.Actual, item.Error = "", "timeout"
		}
	}
	want := []boundaryGroupReport{
		{Group: "boundary_weather", Total: 9, Correct: 7, MissingDecisions: 1, InvalidOutput: 1,
			ExpectedCorrectRestore: 1, MissedCorrectRestore: 1, NonCorrectRestore: 2, FalseCorrect: 1},
		{Group: "boundary_museum", Total: 9, Correct: 8, MissingDecisions: 1, ExpectedCorrectRestore: 1, NonCorrectRestore: 2},
		{Group: "boundary_runner", Total: 9, Correct: 9, ExpectedCorrectRestore: 1, NonCorrectRestore: 2},
		{Group: "boundary_backup", Total: 9, Correct: 9, ExpectedCorrectRestore: 1, NonCorrectRestore: 2},
	}
	if got := summarizeBoundary(items, groups); !slices.Equal(got, want) {
		t.Fatalf("边界分组不符：%+v", got)
	}
	if got := summarizeBoundary(items[:1], groups); len(got) != 1 || got[0].Group != "boundary_weather" || got[0].Total != 1 {
		t.Fatalf("抽样分组不符：%+v", got)
	}
	if got := summarizeBoundary(items, nil); got != nil {
		t.Fatalf("没有分组的评测返回了额外字段：%+v", got)
	}
}

func TestBoundaryScopeTracksGroupAndOrder(t *testing.T) {
	cases, groups, err := loadSuite("boundary")
	if err != nil {
		t.Fatal(err)
	}
	full, err := describeScope("boundary", len(cases), cases, groups)
	if err != nil {
		t.Fatal(err)
	}
	partial, err := describeScope("boundary", len(cases), cases[:2], groups)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := suiteHash(partial, cases, groups)
	if err != nil || partial.Complete || partial.SelectedHash == full.SelectedHash || hash != full.SelectedHash {
		t.Fatalf("抽样报告的题集指纹不符：%+v %s %v", partial, hash, err)
	}
	reordered := slices.Clone(cases)
	reordered[0], reordered[1] = reordered[1], reordered[0]
	changed := maps.Clone(groups)
	changed[cases[0].ID] = "another_group"
	for _, variant := range []struct {
		cases  []agent.EvalCase
		groups map[string]string
	}{{reordered, groups}, {cases, changed}} {
		scope, err := describeScope("boundary", len(cases), variant.cases, variant.groups)
		if err != nil || scope.SelectedHash == full.SelectedHash {
			t.Fatalf("样例顺序或分组变化没有反映到指纹：%+v %v", scope, err)
		}
	}
}

func TestBoundaryCompareRejectsOffsettingGroupRegression(t *testing.T) {
	cases, groups, err := loadSuite("boundary")
	if err != nil {
		t.Fatal(err)
	}
	scope, err := describeScope("boundary", len(cases), cases, groups)
	if err != nil {
		t.Fatal(err)
	}
	base, next := correctReport(t, "boundary", cases), correctReport(t, "boundary", cases)
	base.Scope, next.Scope = scope, scope
	for i, item := range base.Report.Items {
		switch item.ID {
		case "boundary_weather_restore_partial":
			base.Report.Items[i].Actual = "正确"
		case "boundary_museum_restore_partial":
			next.Report.Items[i].Actual = "正确"
		}
	}
	dir := t.TempDir()
	basePath := saveReport(t, dir, "base", base)
	var stdout bytes.Buffer
	err = runCompare([]string{basePath, saveReport(t, dir, "next", next)}, &stdout)
	if err == nil {
		t.Fatal("另一组改善不能抵消本组误放行增加")
	}
	var result struct {
		Baseline  evalScore `json:"baseline"`
		Candidate evalScore `json:"candidate"`
		Passed    bool      `json:"passed"`
		Issues    []string  `json:"issues"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Passed || result.Baseline.FalseCorrect != 1 || result.Candidate.FalseCorrect != 1 ||
		!slices.Contains(result.Issues, "boundary_museum：错误通关数增加") ||
		slices.Contains(result.Issues, "错误通关数增加") {
		t.Fatalf("分组退化报告不符：%+v", result)
	}

	next.Report.Items[0], next.Report.Items[1] = next.Report.Items[1], next.Report.Items[0]
	stdout.Reset()
	err = runCompare([]string{basePath, saveReport(t, dir, "reordered", next)}, &stdout)
	if err == nil || !strings.Contains(err.Error(), "报告结果与题集不一致") || stdout.Len() != 0 {
		t.Fatalf("对照接受了顺序不符的报告：%v %s", err, stdout.String())
	}
}

func BenchmarkBoundaryGrouping(b *testing.B) {
	cases, groups, err := loadSuite("boundary")
	if err != nil {
		b.Fatal(err)
	}
	report := agent.EvalReport{Total: len(cases), Items: make([]agent.EvalItem, 0, len(cases))}
	for _, item := range cases {
		report.Items = append(report.Items, agent.EvalItem{ID: item.ID, Kind: item.Kind, Expected: item.Expected, Actual: item.Expected})
	}
	for _, variant := range []struct {
		name   string
		groups map[string]string
	}{{"Baseline", nil}, {"WithGroups", groups}} {
		b.Run(variant.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				score, err := scoreReport(report, cases)
				if err != nil || score.Correct != report.Total {
					b.Fatal("计算对照得分失败", err)
				}
				if variant.groups != nil {
					if got := summarizeBoundary(report.Items, variant.groups); len(got) != 4 {
						b.Fatal("计算分组结果失败")
					}
				}
			}
		})
	}
}
