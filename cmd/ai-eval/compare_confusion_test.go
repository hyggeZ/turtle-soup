package main

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"testing"
	"time"

	"turtle-soup/internal/agent"
)

var confusionQuestionLabels = []string{"是", "否", "无关"}
var confusionRestoreLabels = []string{"正确", "不完整", "错误"}

type confusionCompareScore struct {
	Correct           int                   `json:"correct"`
	Errors            int                   `json:"errors"`
	QuestionF1        agent.ClassF1Summary  `json:"questionF1"`
	QuestionConfusion agent.ConfusionMatrix `json:"questionConfusion"`
	RestoreConfusion  agent.ConfusionMatrix `json:"restoreConfusion"`
}

// 相同的总分和宏 F1 可能掩盖误判方向相反，矩阵应准确指出这种差异。
func TestCompareChangesInConfusionCells(t *testing.T) {
	t.Setenv("DASHSCOPE_API_KEY", "")
	paths, wantQuestions, wantRestores, correct := confusionCompareInputs(t)
	var stdout bytes.Buffer
	if err := runCompare(paths, &stdout); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Baseline               confusionCompareScore `json:"baseline"`
		Candidate              confusionCompareScore `json:"candidate"`
		QuestionConfusionDelta agent.ConfusionMatrix `json:"questionConfusionDelta"`
		RestoreConfusionDelta  agent.ConfusionMatrix `json:"restoreConfusionDelta"`
		QuestionF1Delta        f1Delta               `json:"questionF1Delta"`
		Passed                 bool                  `json:"passed"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Baseline.Correct != correct || result.Candidate.Correct != correct || result.Baseline.Errors != 1 || result.Candidate.Errors != 1 || !result.Passed {
		t.Error("总正确数、失败数和质量门应保持一致")
	}
	if got := result.QuestionF1Delta.MacroF1; got == nil || *got != 0 || !reflect.DeepEqual(result.Baseline.QuestionF1, result.Candidate.QuestionF1) {
		t.Error("改变方向后的各标签 F1 和宏 F1 应保持一致")
	}
	checkCompareMatrix(t, "基线问答", result.Baseline.QuestionConfusion, confusionQuestionLabels, wantQuestions[0])
	checkCompareMatrix(t, "候选问答", result.Candidate.QuestionConfusion, confusionQuestionLabels, wantQuestions[1])
	checkCompareMatrix(t, "问答差值", result.QuestionConfusionDelta, confusionQuestionLabels, [][]int{{0, -1, 1}, {1, 0, -1}, {-1, 1, 0}})
	checkCompareMatrix(t, "基线还原", result.Baseline.RestoreConfusion, confusionRestoreLabels, wantRestores)
	checkCompareMatrix(t, "候选还原", result.Candidate.RestoreConfusion, confusionRestoreLabels, wantRestores)
	checkCompareMatrix(t, "还原差值", result.RestoreConfusionDelta, confusionRestoreLabels, [][]int{{0, 0, 0}, {0, 0, 0}, {0, 0, 0}})
}

func checkCompareMatrix(t *testing.T, name string, actual agent.ConfusionMatrix, labels []string, counts [][]int) {
	t.Helper()
	if !slices.Equal(actual.Labels, labels) || !reflect.DeepEqual(actual.Counts, counts) {
		t.Errorf("%s：标签 %v、计数 %v，应为 %v、%v", name, actual.Labels, actual.Counts, labels, counts)
	}
}

func confusionCompareInputs(t *testing.T) (paths []string, questions [2][][]int, restores [][]int, correct int) {
	t.Helper()
	cases, _, err := loadSuite("holdout")
	if err != nil {
		t.Fatal(err)
	}
	base := correctReport(t, "holdout", cases)
	next := correctReport(t, "holdout", cases)
	questions = [2][][]int{{{0, 1, 0}, {0, 0, 1}, {1, 0, 0}}, {{0, 0, 1}, {1, 0, 0}, {0, 1, 0}}}
	restores = [][]int{{0, 0, 0}, {0, 0, 0}, {0, 0, 0}}
	var changed [3]bool
	failed := false
	for i, item := range base.Report.Items {
		if item.Kind == "question" {
			row := slices.Index(confusionQuestionLabels, item.Expected)
			if !changed[row] {
				base.Report.Items[i].Actual = confusionQuestionLabels[(row+1)%3]
				next.Report.Items[i].Actual = confusionQuestionLabels[(row+2)%3]
				changed[row] = true
			} else {
				questions[0][row][row]++
				questions[1][row][row]++
			}
			continue
		}
		if !failed && item.Expected == "不完整" {
			base.Report.Items[i].Actual, base.Report.Items[i].Error = "", "timeout"
			next.Report.Items[i].Actual, next.Report.Items[i].Error = "", "timeout"
			failed = true
			continue
		}
		row := slices.Index(confusionRestoreLabels, item.Expected)
		restores[row][row]++
	}
	if changed != [3]bool{true, true, true} || !failed {
		t.Fatal("留出集缺少用于测试的问答或还原样例")
	}
	// 输入聚合可能来自较早版本或不可信；两边必须从逐例结果重新统计。
	next.Report.QuestionConfusion = agent.ConfusionMatrix{Labels: []string{"合成错误聚合"}, Counts: [][]int{{999}}}
	next.Report.RestoreConfusion = next.Report.QuestionConfusion
	dir := t.TempDir()
	basePath := saveReport(t, dir, "baseline", base)
	removeLegacyMatrices(t, basePath)
	return []string{basePath, saveReport(t, dir, "candidate", next)}, questions, restores, len(cases) - 4
}

func removeLegacyMatrices(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report map[string]json.RawMessage
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	var aggregate map[string]json.RawMessage
	if err := json.Unmarshal(report["report"], &aggregate); err != nil {
		t.Fatal(err)
	}
	delete(aggregate, "questionConfusion")
	delete(aggregate, "restoreConfusion")
	data, err = json.Marshal(aggregate)
	if err != nil {
		t.Fatal(err)
	}
	report["report"] = data
	data, err = json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// 复用同一离线输入测量实际文件读取、逐例核对和 JSON 输出。
func TestCompareConfusionReportCost(t *testing.T) {
	if os.Getenv("AI_EVAL_CONFUSION_COMPARE") != "1" {
		t.Skip("设置 AI_EVAL_CONFUSION_COMPARE=1 后测量")
	}
	paths, _, _, _ := confusionCompareInputs(t)
	const warmups, samples = 8, 64
	times := make([]time.Duration, 0, samples)
	var reportBytes int
	for i := range warmups + samples {
		var stdout bytes.Buffer
		start := time.Now()
		err := runCompare(paths, &stdout)
		elapsed := time.Since(start)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			reportBytes = stdout.Len()
		} else if stdout.Len() != reportBytes {
			t.Fatal("相同评测报告的输出长度不稳定")
		}
		if i >= warmups {
			times = append(times, elapsed)
		}
	}
	slices.Sort(times)
	t.Logf("n=%d JSON_bytes=%d p50_us=%.3f p95_us=%.3f", samples, reportBytes,
		float64(times[samples/2-1])/float64(time.Microsecond), float64(times[(95*samples+99)/100-1])/float64(time.Microsecond))
}
