package main

import (
	"bytes"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"testing"

	"turtle-soup/internal/agent"
)

// 在 4 个故事中的 1 条问答制造调用失败，其他样例保持一致。
func pairedBootstrapFixture(t testing.TB) (output, output) {
	t.Helper()
	next := stabilityFixture(t, "holdout")
	base := next
	base.Report.Items = slices.Clone(next.Report.Items)
	base.Report.Items[0].Actual, base.Report.Items[0].Error = "", "timeout"
	return base, next
}

func TestComparePairedBootstrap(t *testing.T) {
	base, next := pairedBootstrapFixture(t)
	dir := t.TempDir()
	basePath, nextPath := saveReport(t, dir, "base", base), saveReport(t, dir, "next", next)
	for _, sample := range []struct {
		name     string
		old, new string
		want     pairedAccuracyInterval
		passed   bool
		issues   []string
	}{
		{"improved", basePath, nextPath, pairedAccuracyInterval{1.0 / 24, 0, 3.0 / 24, 0.95, 4, 256}, true, nil},
		{"regressed", nextPath, basePath, pairedAccuracyInterval{-1.0 / 24, -3.0 / 24, 0, 0.95, 4, 256}, false,
			[]string{"调用失败数增加", "命中人工标签的总数减少"}},
		{"unchanged", nextPath, nextPath, pairedAccuracyInterval{0, 0, 0, 0.95, 4, 256}, true, nil},
	} {
		t.Run(sample.name, func(t *testing.T) {
			var stdout bytes.Buffer
			err := run([]string{"compare", sample.old, sample.new}, &stdout)
			if (err == nil) != sample.passed {
				t.Fatalf("质量门槛意外变化：%v", err)
			}
			var result struct {
				Interval *pairedAccuracyInterval `json:"pairedAccuracyInterval"`
				Passed   bool                    `json:"passed"`
				Issues   []string                `json:"issues"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.Interval == nil || *result.Interval != sample.want {
				t.Fatalf("区间应为 %+v，实际为 %+v", sample.want, result.Interval)
			}
			if result.Passed != sample.passed || !slices.Equal(result.Issues, sample.issues) {
				t.Fatalf("区间不应改变质量门槛：passed=%t issues=%v", result.Passed, result.Issues)
			}
		})
	}
}

func TestCompareOmitsBootstrapOutsideHoldout(t *testing.T) {
	for _, suite := range []string{"dev", "boundary"} {
		t.Run(suite, func(t *testing.T) {
			report := stabilityFixture(t, suite)
			path := saveReport(t, t.TempDir(), "report", report)
			var stdout bytes.Buffer
			if err := run([]string{"compare", path, path}, &stdout); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(stdout.String(), `"pairedAccuracyInterval"`) {
				t.Fatalf("%s 不应输出留出集区间", suite)
			}
		})
	}
}

func TestBootstrapAccuracySkipsUnsupportedClusterCounts(t *testing.T) {
	cases := make([]agent.EvalCase, maxBootstrapClusters+1)
	items := make([]agent.EvalItem, len(cases))
	for i := range cases {
		cases[i].Soup = agent.Soup{Surface: "合成汤面", Answer: "合成答案 " + strconv.Itoa(i)}
		items[i] = agent.EvalItem{Expected: "是", Actual: "是"}
	}
	if bootstrapAccuracy(cases[:1], items[:1], items[:1]) != nil || bootstrapAccuracy(cases, items, items) != nil {
		t.Fatal("只有 2 到 6 个故事才枚举区间")
	}
}

func BenchmarkPairedBootstrapHoldout(b *testing.B) {
	cases, _, err := loadSuite("holdout")
	if err != nil {
		b.Fatal(err)
	}
	base, next := pairedBootstrapFixture(b)
	b.ReportAllocs()
	for b.Loop() {
		if bootstrapAccuracy(cases, base.Report.Items, next.Report.Items) == nil {
			b.Fatal("留出集缺少 bootstrap 区间")
		}
	}
}
