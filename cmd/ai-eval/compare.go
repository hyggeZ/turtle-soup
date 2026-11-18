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
	Correct          int     `json:"correct"`
	Errors           int     `json:"errors"`
	FalseCorrect     int     `json:"falseCorrect"`
	CorrectRestores  int     `json:"correctRestores"`
	PromptTokens     int     `json:"promptTokens"`
	CompletionTokens int     `json:"completionTokens"`
	LatencyP95MS     float64 `json:"latencyP95Ms"`
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
	cases, err := loadSuite(base.Scope.Suite)
	if err != nil {
		return err
	}
	full, err := describeScope(base.Scope.Suite, len(cases), cases)
	if err != nil || full != base.Scope {
		return errors.New("报告范围与本版本的全量题集不一致")
	}
	oldScore, err := scoreReport(base.Report, cases)
	if err != nil {
		return fmt.Errorf("基线报告：%w", err)
	}
	newScore, err := scoreReport(next.Report, cases)
	if err != nil {
		return fmt.Errorf("候选报告：%w", err)
	}
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
	result := struct {
		Scope     evalScope `json:"scope"`
		Baseline  evalScore `json:"baseline"`
		Candidate evalScore `json:"candidate"`
		Passed    bool      `json:"passed"`
		Issues    []string  `json:"issues"`
	}{Scope: full, Baseline: oldScore, Candidate: newScore, Passed: len(issues) == 0, Issues: issues}
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
	if report.Total != len(cases) || len(report.Items) != len(cases) {
		return evalScore{}, errors.New("逐例结果数量与全量题集不一致")
	}
	score := evalScore{
		PromptTokens: report.PromptTokens, CompletionTokens: report.CompletionTokens,
		LatencyP95MS: report.LatencyDistribution.P95,
	}
	var questions, correctRestores, otherRestores int
	for i, item := range report.Items {
		sample := cases[i]
		if item.ID != sample.ID || item.Kind != sample.Kind || item.Expected != sample.Expected {
			return evalScore{}, fmt.Errorf("第 %d 条报告结果与题集不一致", i+1)
		}
		if item.Error != "" && item.Actual == "" {
			score.Errors++
			continue
		}
		if item.Error != "" || !validOutcome(item) {
			return evalScore{}, fmt.Errorf("第 %d 条报告结果格式无效", i+1)
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
	return score, nil
}

func validOutcome(item agent.EvalItem) bool {
	if item.Kind == "question" {
		return item.Actual == "是" || item.Actual == "否" || item.Actual == "无关"
	}
	return item.Actual == "正确" || item.Actual == "不完整" || item.Actual == "错误"
}
