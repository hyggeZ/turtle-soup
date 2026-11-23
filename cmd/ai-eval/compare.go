package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"turtle-soup/internal/agent"
)

type evalScore struct {
	Model             string                `json:"model"`
	PromptVersion     string                `json:"promptVersion"`
	Correct           int                   `json:"correct"`
	Errors            int                   `json:"errors"`
	FalseCorrect      int                   `json:"falseCorrect"`
	CorrectRestores   int                   `json:"correctRestores"`
	PromptTokens      int                   `json:"promptTokens"`
	CompletionTokens  int                   `json:"completionTokens"`
	LatencyP95MS      float64               `json:"latencyP95Ms"`
	QuestionF1        agent.ClassF1Summary  `json:"questionF1"`
	RestoreF1         agent.ClassF1Summary  `json:"restoreF1"`
	QuestionConfusion agent.ConfusionMatrix `json:"questionConfusion"`
	RestoreConfusion  agent.ConfusionMatrix `json:"restoreConfusion"`
	BoundaryGroups    []boundaryGroupReport `json:"boundaryGroups,omitempty"`
}

// compare 只读两份报告，无需密钥或重新请求模型。
func runCompare(args []string, stdout io.Writer) error {
	if len(args) != 2 {
		return errors.New("用法：ai-eval compare 基线报告.json 候选报告.json")
	}
	base, err := readOutput(args[0])
	if err != nil {
		return fmt.Errorf("读取基线报告：%w", err)
	}
	next, err := readOutput(args[1])
	if err != nil {
		return fmt.Errorf("读取候选报告：%w", err)
	}
	if !base.Scope.Complete || base.Scope != next.Scope {
		return errors.New("需要同一题集和指纹的两份全量报告")
	}
	cases, groups, err := loadCompleteScope(base.Scope)
	if err != nil {
		return err
	}
	oldScore, err := scoreReport(base.Report, cases)
	if err != nil {
		return fmt.Errorf("基线报告：%w", err)
	}
	newScore, err := scoreReport(next.Report, cases)
	if err != nil {
		return fmt.Errorf("候选报告：%w", err)
	}
	oldScore.Model, oldScore.PromptVersion = base.Model, base.PromptVersion
	newScore.Model, newScore.PromptVersion = next.Model, next.PromptVersion
	oldScore.BoundaryGroups = summarizeBoundary(base.Report.Items, groups)
	newScore.BoundaryGroups = summarizeBoundary(next.Report.Items, groups)
	issues := make([]string, 0, 4)
	if newScore.Errors > oldScore.Errors {
		issues = append(issues, "调用失败数增加")
	}
	if newScore.FalseCorrect > oldScore.FalseCorrect {
		issues = append(issues, "错误通关数增加")
	}
	if newScore.CorrectRestores < oldScore.CorrectRestores {
		issues = append(issues, "正确还原被命中的数量减少")
	}
	if newScore.Correct < oldScore.Correct {
		issues = append(issues, "命中人工标签的总数减少")
	}
	issues = appendBoundaryIssues(issues, oldScore.BoundaryGroups, newScore.BoundaryGroups)
	result := struct {
		Scope                  evalScope             `json:"scope"`
		Baseline               evalScore             `json:"baseline"`
		Candidate              evalScore             `json:"candidate"`
		QuestionF1Delta        f1Delta               `json:"questionF1Delta"`
		RestoreF1Delta         f1Delta               `json:"restoreF1Delta"`
		QuestionConfusionDelta agent.ConfusionMatrix `json:"questionConfusionDelta"`
		RestoreConfusionDelta  agent.ConfusionMatrix `json:"restoreConfusionDelta"`
		Passed                 bool                  `json:"passed"`
		Issues                 []string              `json:"issues"`
	}{
		Scope: base.Scope, Baseline: oldScore, Candidate: newScore,
		QuestionF1Delta:        compareF1(oldScore.QuestionF1, newScore.QuestionF1),
		RestoreF1Delta:         compareF1(oldScore.RestoreF1, newScore.RestoreF1),
		QuestionConfusionDelta: differenceMatrix(oldScore.QuestionConfusion, newScore.QuestionConfusion),
		RestoreConfusionDelta:  differenceMatrix(oldScore.RestoreConfusion, newScore.RestoreConfusion),
		Passed:                 len(issues) == 0, Issues: issues,
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		return errors.New("比较结果写入失败")
	}
	if !result.Passed {
		return errors.New("候选未通过质量对照")
	}
	return nil
}

func readOutput(path string) (output, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return output{}, err
	}
	var report output
	err = json.Unmarshal(data, &report)
	return report, err
}

func scoreReport(report agent.EvalReport, cases []agent.EvalCase) (evalScore, error) {
	if err := validateReportCount(report, len(cases)); err != nil {
		return evalScore{}, err
	}
	score := evalScore{
		PromptTokens: report.PromptTokens, CompletionTokens: report.CompletionTokens,
		LatencyP95MS: report.LatencyDistribution.P95,
	}
	var questions, correctRestores, otherRestores int
	for i, item := range report.Items {
		if err := validateReportItem(i, item, cases[i]); err != nil {
			return evalScore{}, err
		}
		if item.Error != "" {
			score.Errors++
			continue
		}
		if item.Actual == item.Expected {
			score.Correct++
		}
		switch {
		case item.Kind == "question":
			questions++
		case item.Expected == "正确":
			correctRestores++
			if item.Actual == "正确" {
				score.CorrectRestores++
			}
		default:
			otherRestores++
			if item.Actual == "正确" {
				score.FalseCorrect++
			}
		}
	}
	if questions == 0 || correctRestores == 0 || otherRestores == 0 {
		return evalScore{}, errors.New("问答、正确还原、非正确还原均需要有效判定")
	}
	// 旧报告也能比较，分类汇总只使用已核对的逐例结果。
	score.QuestionF1, score.RestoreF1 = agent.ClassificationF1(report.Items)
	score.QuestionConfusion, score.RestoreConfusion = agent.ClassificationConfusion(report.Items)
	return score, nil
}

// 行列标签由评测程序固定，差值表示候选较基线增加或减少的预测次数。
func differenceMatrix(base, next agent.ConfusionMatrix) agent.ConfusionMatrix {
	delta := agent.ConfusionMatrix{Labels: next.Labels, Counts: make([][]int, len(next.Counts))}
	for row, counts := range next.Counts {
		delta.Counts[row] = make([]int, len(counts))
		for column, count := range counts {
			delta.Counts[row][column] = count - base.Counts[row][column]
		}
	}
	return delta
}

func validateReportCount(report agent.EvalReport, total int) error {
	if report.Total != total || len(report.Items) != total {
		return errors.New("逐例结果数量与全量题集不一致")
	}
	return nil
}

func validateReportItem(i int, item agent.EvalItem, sample agent.EvalCase) error {
	if item.ID != sample.ID || item.Kind != sample.Kind || item.Expected != sample.Expected {
		return fmt.Errorf("第 %d 条报告结果与题集不一致", i+1)
	}
	if item.Error == "" && validOutcome(item) || item.Error != "" && item.Actual == "" {
		return nil
	}
	return fmt.Errorf("第 %d 条报告结果格式无效", i+1)
}

func validOutcome(item agent.EvalItem) bool {
	if item.Kind == "question" {
		return item.Actual == "是" || item.Actual == "否" || item.Actual == "无关"
	}
	return item.Actual == "正确" || item.Actual == "不完整" || item.Actual == "错误"
}
