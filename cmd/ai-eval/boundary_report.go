package main

import "turtle-soup/internal/agent"

type boundaryGroupReport struct {
	Group                  string `json:"group"`
	Total                  int    `json:"total"`
	Correct                int    `json:"correct"`
	MissingDecisions       int    `json:"missingDecisions"`
	InvalidOutput          int    `json:"invalidOutput"`
	ExpectedCorrectRestore int    `json:"expectedCorrectRestore"`
	MissedCorrectRestore   int    `json:"missedCorrectRestore"`
	NonCorrectRestore      int    `json:"nonCorrectRestore"`
	FalseCorrect           int    `json:"falseCorrect"`
}

// 按样例的原始顺序汇总。调用失败也计入应通关和不应通关的标注总数。
func summarizeBoundary(items []agent.EvalItem, groups map[string]string) []boundaryGroupReport {
	if len(groups) == 0 {
		return nil
	}
	positions := make(map[string]int)
	var reports []boundaryGroupReport
	for _, item := range items {
		group := groups[item.ID]
		index, found := positions[group]
		if !found {
			index = len(reports)
			positions[group] = index
			reports = append(reports, boundaryGroupReport{Group: group})
		}
		report := &reports[index]
		report.Total++
		valid := item.Error == ""
		if !valid {
			report.MissingDecisions++
			if item.Error == "invalid_output" {
				report.InvalidOutput++
			}
		}
		matched := valid && item.Actual == item.Expected
		if matched {
			report.Correct++
		}
		if item.Kind != "restore" {
			continue
		}
		if item.Expected == "正确" {
			report.ExpectedCorrectRestore++
			if !matched {
				report.MissedCorrectRestore++
			}
		} else {
			report.NonCorrectRestore++
			if valid && item.Actual == "正确" {
				report.FalseCorrect++
			}
		}
	}
	return reports
}

// 报告已经逐例核对相同的题集和顺序，两边分组可按同一位置比较。
func appendBoundaryIssues(issues []string, baseline, candidate []boundaryGroupReport) []string {
	for i, next := range candidate {
		old := baseline[i]
		prefix := next.Group + "："
		if next.MissingDecisions > old.MissingDecisions {
			issues = append(issues, prefix+"调用失败数增加")
		}
		if next.InvalidOutput > old.InvalidOutput {
			issues = append(issues, prefix+"无效输出数增加")
		}
		if next.FalseCorrect > old.FalseCorrect {
			issues = append(issues, prefix+"错误通关数增加")
		}
		if next.MissedCorrectRestore > old.MissedCorrectRestore {
			issues = append(issues, prefix+"正确还原被命中的数量减少")
		}
		if next.Correct < old.Correct {
			issues = append(issues, prefix+"命中人工标签的总数减少")
		}
	}
	return issues
}
