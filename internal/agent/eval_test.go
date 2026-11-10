package agent

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"testing"
)

func loadEvalCases(t *testing.T) []EvalCase {
	t.Helper()
	data, err := os.ReadFile("testdata/eval_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []EvalCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	restoreData, err := os.ReadFile("testdata/restore_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var groups []struct {
		ID    string `json:"id"`
		Soup  Soup   `json:"soup"`
		Cases []struct {
			ID       string `json:"id"`
			Input    string `json:"input"`
			Expected string `json:"expected"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(restoreData, &groups); err != nil {
		t.Fatal(err)
	}
	if len(groups) < 20 {
		t.Fatalf("还原评测题目不足 20 道：%d", len(groups))
	}
	for _, group := range groups {
		if group.ID == "" || group.Soup.Title == "" || group.Soup.Surface == "" || group.Soup.Answer == "" || len(group.Cases) < 5 {
			t.Fatalf("还原评测题目无效：%s", group.ID)
		}
		labels := map[string]int{}
		for _, item := range group.Cases {
			if item.ID == "" || item.Input == "" || !oneOf(item.Expected, "正确", "不完整", "错误") {
				t.Fatalf("还原评测样例无效：%s/%s", group.ID, item.ID)
			}
			labels[item.Expected]++
			cases = append(cases, EvalCase{ID: item.ID, Soup: group.Soup, Kind: "restore", Input: item.Input, Expected: item.Expected})
		}
		if labels["正确"] == 0 || labels["不完整"] == 0 || labels["错误"] == 0 {
			t.Fatalf("还原评测类别不全：%s", group.ID)
		}
	}
	seen := map[string]bool{}
	for _, sample := range cases {
		if sample.ID == "" || sample.Soup.Answer == "" || sample.Input == "" ||
			!oneOf(sample.Kind, "question", "restore") ||
			sample.Kind == "question" && !oneOf(sample.Expected, "是", "否", "无关") ||
			sample.Kind == "restore" && !oneOf(sample.Expected, "正确", "错误", "不完整") || seen[sample.ID] {
			t.Fatalf("评测样例无效：%s", sample.ID)
		}
		seen[sample.ID] = true
	}
	return cases
}

func TestGoldenSet(t *testing.T) {
	cases := loadEvalCases(t)
	if len(cases) < 108 {
		t.Fatalf("评测样例不足：%d", len(cases))
	}
}

type evalReplay struct {
	results map[string]Decision
	errors  map[string]error
	history map[string]int
}

func (r *evalReplay) DecideQuestion(_ context.Context, _ Soup, history []Exchange, input string) (Decision, error) {
	r.history[input] = len(history)
	return r.results[input], r.errors[input]
}

func (r *evalReplay) JudgeRestore(_ context.Context, _ Soup, history []Exchange, input string) (Decision, error) {
	r.history[input] = len(history)
	return r.results[input], r.errors[input]
}

func TestEvaluateReportFromOfflineReplay(t *testing.T) {
	replay := &evalReplay{
		results: map[string]Decision{
			"q_yes":   {Result: "是", LatencyMS: 10, PromptTokens: 10, CompletionTokens: 1},
			"q_no":    {Result: "是", LatencyMS: 20, PromptTokens: 20, CompletionTokens: 2},
			"partial": {Result: "正确", LatencyMS: 30, PromptTokens: 30, CompletionTokens: 3},
			"wrong":   {Result: "错误", LatencyMS: 40, PromptTokens: 40, CompletionTokens: 4},
		},
		errors:  map[string]error{"bad_format": ErrInvalidOutput},
		history: map[string]int{},
	}
	cases := []EvalCase{
		{ID: "1", Kind: "question", Input: "q_yes", Expected: "是", History: []Exchange{{Question: "此前问题", Answer: "否"}}},
		{ID: "2", Kind: "question", Input: "q_no", Expected: "否"},
		{ID: "3", Kind: "restore", Input: "partial", Expected: "不完整"},
		{ID: "4", Kind: "restore", Input: "wrong", Expected: "错误"},
		{ID: "5", Kind: "restore", Input: "bad_format", Expected: "正确"},
	}
	report := Evaluate(context.Background(), replay, cases)
	if report.Total != 5 || report.Correct != 2 || report.Errors != 1 || report.Accuracy != .4 {
		t.Fatalf("基础统计错误：%+v", report)
	}
	if report.QuestionConfusion.Counts[0][0] != 1 || report.QuestionConfusion.Counts[1][0] != 1 ||
		report.RestoreConfusion.Counts[1][0] != 1 || report.RestoreConfusion.Counts[2][2] != 1 {
		t.Fatalf("混淆矩阵错误：%+v %+v", report.QuestionConfusion, report.RestoreConfusion)
	}
	if report.NonCorrectRestore != 2 || report.NonCorrectEvaluated != 2 || report.FalseCorrectCount != 1 || report.FalseCorrectRate != .5 {
		t.Fatalf("错误完结率错误：%+v", report)
	}
	if report.LatencyDistribution.Count != 4 || report.LatencyDistribution.P50 != 20 || report.LatencyDistribution.P95 != 40 ||
		report.PromptTokenDistribution.Max != 40 || report.TotalTokenDistribution.P50 != 22 {
		t.Fatalf("分布统计错误：%+v", report)
	}
	if replay.history["q_yes"] != 1 {
		t.Fatal("评测没有向模型传入历史问答")
	}
}

func TestFalseCorrectRateExcludesFailedClassifications(t *testing.T) {
	tests := []struct {
		name       string
		results    map[string]Decision
		errors     map[string]error
		evaluated  int
		falseGood  int
		errorsWant int
		rate       float64
	}{
		{
			name: "全部失败", errors: map[string]error{"partial": ErrUnavailable, "wrong": ErrInvalidOutput, "counter": ErrUnavailable},
			evaluated: 0, falseGood: 0, errorsWant: 3, rate: 0,
		},
		{
			name: "部分失败", results: map[string]Decision{"partial": {Result: "正确"}, "wrong": {Result: "错误"}},
			errors: map[string]error{"counter": ErrUnavailable}, evaluated: 2, falseGood: 1, errorsWant: 1, rate: .5,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			replay := &evalReplay{results: test.results, errors: test.errors, history: map[string]int{}}
			cases := []EvalCase{
				{ID: "1", Kind: "restore", Input: "partial", Expected: "不完整"},
				{ID: "2", Kind: "restore", Input: "wrong", Expected: "错误"},
				{ID: "3", Kind: "restore", Input: "counter", Expected: "错误"},
			}
			report := Evaluate(context.Background(), replay, cases)
			if report.NonCorrectRestore != 3 || report.NonCorrectEvaluated != test.evaluated ||
				report.FalseCorrectCount != test.falseGood || report.Errors != test.errorsWant || report.FalseCorrectRate != test.rate {
				t.Fatalf("失败样例影响了错误完结率：%+v", report)
			}
		})
	}
}

// 只有显式开启才调用付费模型；QWEN_EVAL_LIMIT 可限制本次样例数。
func TestLiveEval(t *testing.T) {
	cases := loadEvalCases(t)
	if os.Getenv("RUN_QWEN_EVAL") != "1" {
		t.Skip("设置 RUN_QWEN_EVAL=1 才运行在线评测")
	}
	model := NewQwenFromEnv()
	if !model.Ready() {
		t.Fatal("在线评测需要 DASHSCOPE_API_KEY")
	}
	if raw := os.Getenv("QWEN_EVAL_LIMIT"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 {
			t.Fatal("QWEN_EVAL_LIMIT 必须是正整数")
		}
		if limit < len(cases) {
			cases = cases[:limit]
		}
	}
	report := Evaluate(context.Background(), model, cases)
	t.Logf("model=%s prompt=%s cases=%d accuracy=%.2f errors=%d falseCorrect=%d/%d rate=%.3f nonCorrectTotal=%d latency=%+v promptTokens=%+v completionTokens=%+v totalTokens=%+v", model.Model, PromptVersion, report.Total, report.Accuracy, report.Errors, report.FalseCorrectCount, report.NonCorrectEvaluated, report.FalseCorrectRate, report.NonCorrectRestore, report.LatencyDistribution, report.PromptTokenDistribution, report.CompletionTokenDistribution, report.TotalTokenDistribution)
	t.Logf("questionConfusion=%+v restoreConfusion=%+v", report.QuestionConfusion, report.RestoreConfusion)
	for _, item := range report.Items {
		t.Logf("case=%s expected=%s actual=%s error=%s", item.ID, item.Expected, item.Actual, item.Error)
	}
}
