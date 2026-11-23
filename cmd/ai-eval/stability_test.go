package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"turtle-soup/internal/agent"
)

func stabilityFixture(t testing.TB, suite string) output {
	t.Helper()
	cases, groups, err := loadSuite(suite)
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
	// 报告级汇总故意留零，稳定性必须使用逐例结果重算。
	report := agent.EvalReport{Total: len(cases)}
	for _, item := range cases {
		report.Items = append(report.Items, agent.EvalItem{ID: item.ID, Kind: item.Kind, Expected: item.Expected, Actual: item.Expected})
	}
	return output{Model: "fixture-model", PromptVersion: "fixture-v1", EvalSetHash: hash, Scope: scope, Report: report}
}

func stabilityOutput(t *testing.T, rounds ...output) stabilityResult {
	t.Helper()
	t.Setenv("DASHSCOPE_API_KEY", "")
	dir := t.TempDir()
	args := []string{"stability"}
	for i, round := range rounds {
		args = append(args, saveReport(t, dir, fmt.Sprint(i+1), round))
	}
	var stdout bytes.Buffer
	if err := run(args, &stdout); err != nil {
		t.Fatal(err)
	}
	var result stabilityResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout.String(), dir) || strings.Contains(stdout.String(), rounds[0].Report.Items[0].ID) {
		t.Fatal("稳定性报告不应输出输入文件名或逐例数据")
	}
	return result
}

func TestStabilityIntersectCorrectCases(t *testing.T) {
	first, second := stabilityFixture(t, "holdout"), stabilityFixture(t, "holdout")
	for i, item := range first.Report.Items {
		if item.Kind == "question" && item.Expected == "是" {
			first.Report.Items[i].Actual = "否"
			break
		}
	}
	for i, item := range second.Report.Items {
		if item.Kind == "restore" {
			second.Report.Items[i].Actual, second.Report.Items[i].Error = "", "timeout"
			break
		}
	}
	got := stabilityOutput(t, first, second)
	if got.K != 2 || got.Scope != first.Scope || got.EvalSetHash != first.EvalSetHash || got.Model != first.Model || got.PromptVersion != first.PromptVersion {
		t.Fatalf("评测范围或轮次错误：%+v", got)
	}
	if got.Overall != (stabilityMetric{24, 22, 22.0 / 24}) || got.Question != (stabilityMetric{12, 11, 11.0 / 12}) || got.Restore != (stabilityMetric{12, 11, 11.0 / 12}) {
		t.Fatalf("每轮各错一条且不同，稳定通过应是 22/24：%+v", got)
	}
	if len(got.Rounds) != 2 || got.Rounds[0] != (stabilityRound{23, 0, 23.0 / 24}) || got.Rounds[1] != (stabilityRound{23, 1, 23.0 / 24}) {
		t.Fatalf("每轮正确或调用失败数量错误：%+v", got.Rounds)
	}
}

func TestStabilityAllErrorRound(t *testing.T) {
	good, failed := stabilityFixture(t, "holdout"), stabilityFixture(t, "holdout")
	for i := range failed.Report.Items {
		failed.Report.Items[i].Actual, failed.Report.Items[i].Error = "", "provider_unavailable"
	}
	got := stabilityOutput(t, good, failed)
	if len(got.Rounds) != 2 || got.Rounds[0] != (stabilityRound{24, 0, 1}) || got.Rounds[1] != (stabilityRound{0, 24, 0}) ||
		got.Overall != (stabilityMetric{24, 0, 0}) || got.Question != (stabilityMetric{12, 0, 0}) || got.Restore != (stabilityMetric{12, 0, 0}) {
		t.Fatalf("全失败轮应保留并使稳定通过率为零：%+v", got)
	}
}

func TestStabilityFullScopes(t *testing.T) {
	for _, suite := range []string{"dev", "holdout", "boundary"} {
		t.Run(suite, func(t *testing.T) {
			report := stabilityFixture(t, suite)
			got := stabilityOutput(t, report, report, report)
			if got.K != 3 || len(got.Rounds) != 3 || got.Overall.PassAll != len(report.Report.Items) || got.Overall.Rate != 1 {
				t.Fatalf("完整同设置报告应全部通过：%+v", got)
			}
		})
	}
}

func TestStabilityRejectsIncomparableReports(t *testing.T) {
	dir := t.TempDir()
	base := saveReport(t, dir, "base", stabilityFixture(t, "holdout"))
	for _, sample := range []struct {
		name   string
		issue  string
		change func(*output)
	}{
		{"partial", "完整题集", func(r *output) { r.Scope.Complete = false }},
		{"scope_hash", "全量题集不一致", func(r *output) { r.Scope.SelectedHash = "other" }},
		{"dataset_hash", "题集指纹", func(r *output) { r.EvalSetHash = "other" }},
		{"model_absent", "必须包含模型", func(r *output) { r.Model = "" }},
		{"model_changed", "模型或提示词版本不同", func(r *output) { r.Model = "other" }},
		{"prompt_changed", "模型或提示词版本不同", func(r *output) { r.PromptVersion = "other" }},
		{"missing_item", "逐例结果数量", func(r *output) { r.Report.Items = r.Report.Items[1:] }},
		{"item_order", "结果与题集不一致", func(r *output) { r.Report.Items[0], r.Report.Items[1] = r.Report.Items[1], r.Report.Items[0] }},
		{"invalid_result", "结果格式无效", func(r *output) { r.Report.Items[0].Actual = "非有效枚举" }},
		{"conflicting_result", "结果格式无效", func(r *output) { r.Report.Items[0].Error = "timeout" }},
	} {
		t.Run(sample.name, func(t *testing.T) {
			report := stabilityFixture(t, "holdout")
			sample.change(&report)
			changed := saveReport(t, dir, sample.name, report)
			var stdout bytes.Buffer
			err := run([]string{"stability", changed, base}, &stdout)
			if err == nil || !strings.Contains(err.Error(), sample.issue) || stdout.Len() != 0 {
				t.Fatalf("不兼容报告应被拒绝且无部分结果：%v %s", err, stdout.String())
			}
		})
	}
	for _, args := range [][]string{{"stability", base}, {"stability", base, base}} {
		var stdout bytes.Buffer
		if err := run(args, &stdout); err == nil || stdout.Len() != 0 {
			t.Fatalf("至少需要两份不同路径的报告：%v %s", err, stdout.String())
		}
	}
}

func BenchmarkStabilityReports(b *testing.B) {
	dir := b.TempDir()
	report := stabilityFixture(b, "holdout")
	var paths []string
	for i := range 5 {
		paths = append(paths, saveReport(b, dir, fmt.Sprint(i+1), report))
	}
	for _, k := range []int{2, 5} {
		b.Run(fmt.Sprintf("k%d", k), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if err := runStability(paths[:k], io.Discard); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
