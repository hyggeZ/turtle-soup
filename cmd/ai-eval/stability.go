package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"turtle-soup/internal/agent"
)

type stabilityRound struct {
	Correct  int     `json:"correct"`
	Errors   int     `json:"errors"`
	Accuracy float64 `json:"accuracy"`
}

type stabilityMetric struct {
	Total   int     `json:"total"`
	PassAll int     `json:"passAll"`
	Rate    float64 `json:"rate"`
}

type stabilityResult struct {
	K             int              `json:"k"`
	Scope         evalScope        `json:"scope"`
	EvalSetHash   string           `json:"evalSetHash"`
	Model         string           `json:"model"`
	PromptVersion string           `json:"promptVersion"`
	Rounds        []stabilityRound `json:"rounds"`
	Overall       stabilityMetric  `json:"overall"`
	Question      stabilityMetric  `json:"question"`
	Restore       stabilityMetric  `json:"restore"`
}

// stability 离线读取各轮独立保存的完整报告，不执行模型请求。
func runStability(args []string, stdout io.Writer) error {
	if len(args) < 2 {
		return errors.New("用法：ai-eval stability 第一轮.json 第二轮.json [...]")
	}
	paths := make(map[string]bool, len(args))
	var cases []agent.EvalCase
	var hits []int
	result := stabilityResult{K: len(args), Rounds: make([]stabilityRound, 0, len(args))}
	for i, path := range args {
		abs, err := filepath.Abs(path)
		if err != nil {
			return fmt.Errorf("第 %d 轮报告路径无效", i+1)
		}
		if paths[abs] {
			return fmt.Errorf("第 %d 轮重复使用同一报告路径", i+1)
		}
		paths[abs] = true
		report, err := readOutput(abs)
		if err != nil {
			return fmt.Errorf("读取第 %d 轮报告：%w", i+1, err)
		}
		if i == 0 {
			var groups map[string]string
			cases, groups, err = loadCompleteScope(report.Scope)
			if err != nil {
				return err
			}
			hash, err := suiteHash(report.Scope, cases, groups)
			if err != nil {
				return errors.New("核对全量评测集指纹失败")
			}
			if strings.TrimSpace(report.Model) == "" || strings.TrimSpace(report.PromptVersion) == "" {
				return errors.New("报告必须包含模型和提示词版本")
			}
			result.Scope, result.EvalSetHash = report.Scope, hash
			result.Model, result.PromptVersion = report.Model, report.PromptVersion
			hits = make([]int, len(cases))
		}
		if report.Scope != result.Scope || report.EvalSetHash != result.EvalSetHash ||
			report.Model != result.Model || report.PromptVersion != result.PromptVersion {
			return fmt.Errorf("第 %d 轮报告的题集指纹、模型或提示词版本不同", i+1)
		}
		round, err := countStabilityRound(report.Report, cases, hits)
		if err != nil {
			return fmt.Errorf("第 %d 轮报告：%w", i+1, err)
		}
		result.Rounds = append(result.Rounds, round)
	}
	for i, sample := range cases {
		metric := &result.Question
		if sample.Kind == "restore" {
			metric = &result.Restore
		}
		metric.Total++
		if hits[i] == result.K {
			metric.PassAll++
		}
	}
	result.Overall.Total = len(cases)
	result.Overall.PassAll = result.Question.PassAll + result.Restore.PassAll
	for _, metric := range []*stabilityMetric{&result.Overall, &result.Question, &result.Restore} {
		metric.Rate = stabilityRate(metric.PassAll, metric.Total)
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		return errors.New("稳定性结果写入失败")
	}
	return nil
}

// 调用失败和有效错判都不会增加命中，同一题必须在每轮命中才算通过。
func countStabilityRound(report agent.EvalReport, cases []agent.EvalCase, hits []int) (stabilityRound, error) {
	var round stabilityRound
	if err := validateReportCount(report, len(cases)); err != nil {
		return round, err
	}
	for i, item := range report.Items {
		if err := validateReportItem(i, item, cases[i]); err != nil {
			return round, err
		}
		if item.Error != "" {
			round.Errors++
		} else if item.Actual == item.Expected {
			round.Correct++
			hits[i]++
		}
	}
	round.Accuracy = stabilityRate(round.Correct, len(cases))
	return round, nil
}

func stabilityRate(count, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(count) / float64(total)
}
