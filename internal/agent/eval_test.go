package agent

import (
	"context"
	"encoding/json"
	"os"
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
	if len(cases) < 6 {
		t.Fatal("评测集过小")
	}
	for _, sample := range cases {
		if sample.ID == "" || sample.Soup.Answer == "" || sample.Input == "" ||
			!oneOf(sample.Kind, "question", "restore") ||
			!oneOf(sample.Expected, "是", "否", "无关", "正确", "错误", "不完整") {
			t.Fatalf("评测样例无效：%s", sample.ID)
		}
	}
	return cases
}

// 只有显式开启才调用付费模型；结果用于比较不同模型和提示词版本。
func TestLiveEval(t *testing.T) {
	cases := loadEvalCases(t)
	if os.Getenv("RUN_QWEN_EVAL") != "1" {
		t.Skip("设置 RUN_QWEN_EVAL=1 才运行在线评测")
	}
	model := NewQwenFromEnv()
	if !model.Ready() {
		t.Fatal("在线评测需要 DASHSCOPE_API_KEY")
	}
	report := Evaluate(context.Background(), model, cases)
	t.Logf("model=%s prompt=%s cases=%d accuracy=%.2f errors=%d averageMs=%.1f promptTokens=%d completionTokens=%d", model.Model, PromptVersion, report.Total, report.Accuracy, report.Errors, report.AverageLatencyMS, report.PromptTokens, report.CompletionTokens)
	for _, item := range report.Items {
		t.Logf("case=%s expected=%s actual=%s error=%s", item.ID, item.Expected, item.Actual, item.Error)
	}
}
