package agent

import (
	"context"
	"math"
	"testing"
)

func TestClassificationF1CountsFailuresInSupport(t *testing.T) {
	question, restore := ClassificationF1([]EvalItem{
		{Kind: "question", Expected: "是", Actual: "是"},
		{Kind: "question", Expected: "是", Actual: "是"},
		{Kind: "question", Expected: "是", Actual: "是"},
		{Kind: "question", Expected: "是", Actual: "否"},
		{Kind: "question", Expected: "否", Actual: "是"},
		{Kind: "question", Expected: "否", Error: "timeout"},
		{Kind: "question", Expected: "无关", Actual: "是", Error: "invalid_output"},
		{Kind: "restore", Expected: "不完整", Actual: "错误"},
	})
	for _, want := range []struct {
		index, support, predicted, tp int
		label                         string
		f1                            float64
	}{
		{0, 4, 4, 3, "是", .75},
		{1, 2, 1, 0, "否", 0},
		{2, 1, 0, 0, "无关", 0},
	} {
		got := question.Classes[want.index]
		if got.Label != want.label || got.Support != want.support || got.Predicted != want.predicted || got.TruePositive != want.tp {
			t.Fatalf("标签计数错误：%+v", got)
		}
		wantF1(t, got.F1, want.f1)
	}
	wantF1(t, question.MacroF1, .25)
	if restore.Classes[0].F1 != nil || restore.Classes[2].Support != 0 || restore.Classes[2].Predicted != 1 {
		t.Fatalf("缺类或无样本预测的统计错误：%+v", restore.Classes)
	}
	wantF1(t, restore.Classes[1].F1, 0)
	wantF1(t, restore.Classes[2].F1, 0)
	wantF1(t, restore.MacroF1, 0)
}

func TestEvaluateF1ExcludesOnlyUndefinedClasses(t *testing.T) {
	replay := &evalReplay{
		results: map[string]Decision{"hit": {Result: "正确"}},
		errors:  map[string]error{"timeout": context.DeadlineExceeded},
		history: map[string]int{},
	}
	report := Evaluate(context.Background(), replay, []EvalCase{
		{Kind: "restore", Input: "hit", Expected: "正确"},
		{Kind: "restore", Input: "timeout", Expected: "正确"},
	})
	if report.QuestionF1.MacroF1 != nil || report.RestoreF1.Classes[1].F1 != nil || report.RestoreF1.Classes[2].F1 != nil {
		t.Fatalf("无观测类别的 F1 应为 null：%+v %+v", report.QuestionF1, report.RestoreF1)
	}
	wantF1(t, report.RestoreF1.Classes[0].F1, 2.0/3)
	wantF1(t, report.RestoreF1.MacroF1, 2.0/3)
}

func wantF1(t *testing.T, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Fatalf("F1 缺失，期望 %v", want)
	}
	if math.Abs(*got-want) > 1e-12 {
		t.Fatalf("F1=%v，期望 %v", *got, want)
	}
}
