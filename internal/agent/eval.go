package agent

import (
	"context"
	"errors"
	"math"
	"sort"
)

type EvalCase struct {
	ID       string     `json:"id"`
	Soup     Soup       `json:"soup"`
	History  []Exchange `json:"history,omitempty"`
	Kind     string     `json:"kind"`
	Input    string     `json:"input"`
	Expected string     `json:"expected"`
}

type EvalItem struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
	Error    string `json:"error,omitempty"`
}

// ConfusionMatrix 的行是人工标签，列是模型标签，顺序由 Labels 指定。
type ConfusionMatrix struct {
	Labels []string `json:"labels"`
	Counts [][]int  `json:"counts"`
}

type MetricDistribution struct {
	Count int     `json:"count"`
	Min   float64 `json:"min"`
	P50   float64 `json:"p50"`
	P95   float64 `json:"p95"`
	Max   float64 `json:"max"`
}

// RestorePassMetrics 衡量人工标注和模型判定中的正确还原。分母为零时指标为 null。
type RestorePassMetrics struct {
	ExpectedCorrect  int      `json:"expectedCorrect"`
	PredictedCorrect int      `json:"predictedCorrect"`
	TrueCorrect      int      `json:"trueCorrect"`
	Recall           *float64 `json:"recall"`
	Precision        *float64 `json:"precision"`
}

type EvalReport struct {
	Total                       int                `json:"total"`
	Correct                     int                `json:"correct"`
	Errors                      int                `json:"errors"`
	Accuracy                    float64            `json:"accuracy"`
	AverageLatencyMS            float64            `json:"averageLatencyMs"`
	PromptTokens                int                `json:"promptTokens"`
	CompletionTokens            int                `json:"completionTokens"`
	QuestionConfusion           ConfusionMatrix    `json:"questionConfusion"`
	RestoreConfusion            ConfusionMatrix    `json:"restoreConfusion"`
	QuestionF1                  ClassF1Summary     `json:"questionF1"`
	RestoreF1                   ClassF1Summary     `json:"restoreF1"`
	NonCorrectRestore           int                `json:"nonCorrectRestore"`
	NonCorrectEvaluated         int                `json:"nonCorrectEvaluated"`
	FalseCorrectCount           int                `json:"falseCorrectCount"`
	FalseCorrectRate            float64            `json:"falseCorrectRate"`
	RestorePass                 RestorePassMetrics `json:"restorePass"`
	LatencyDistribution         MetricDistribution `json:"latencyDistributionMs"`
	PromptTokenDistribution     MetricDistribution `json:"promptTokenDistribution"`
	CompletionTokenDistribution MetricDistribution `json:"completionTokenDistribution"`
	TotalTokenDistribution      MetricDistribution `json:"totalTokenDistribution"`
	Items                       []EvalItem         `json:"items"`
}

func newConfusion(labels ...string) ConfusionMatrix {
	matrix := ConfusionMatrix{Labels: labels, Counts: make([][]int, len(labels))}
	for i := range matrix.Counts {
		matrix.Counts[i] = make([]int, len(labels))
	}
	return matrix
}

func questionConfusion() ConfusionMatrix { return newConfusion("是", "否", "无关") }

func restoreConfusion() ConfusionMatrix { return newConfusion("正确", "不完整", "错误") }

// ClassificationConfusion 从逐例判定重算模型的预测流向，调用失败由错误数单独统计。
func ClassificationConfusion(items []EvalItem) (question ConfusionMatrix, restore ConfusionMatrix) {
	question, restore = questionConfusion(), restoreConfusion()
	for _, item := range items {
		if item.Error != "" {
			continue
		}
		switch item.Kind {
		case "question":
			question.add(item.Expected, item.Actual)
		case "restore":
			restore.add(item.Expected, item.Actual)
		}
	}
	return question, restore
}

func (m *ConfusionMatrix) add(expected, actual string) {
	row, column := -1, -1
	for i, label := range m.Labels {
		if expected == label {
			row = i
		}
		if actual == label {
			column = i
		}
	}
	if row >= 0 && column >= 0 {
		m.Counts[row][column]++
	}
}

func distribution(values []float64) MetricDistribution {
	if len(values) == 0 {
		return MetricDistribution{}
	}
	sort.Float64s(values)
	percentile := func(p float64) float64 {
		index := int(math.Ceil(p*float64(len(values)))) - 1
		return values[index]
	}
	return MetricDistribution{Count: len(values), Min: values[0], P50: percentile(.5), P95: percentile(.95), Max: values[len(values)-1]}
}

func evalError(err error) string {
	switch {
	case errors.Is(err, ErrInvalidOutput):
		return "invalid_output"
	case errors.Is(err, ErrUnavailable):
		return "provider_unavailable"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	default:
		return "evaluation_error"
	}
}

// Evaluate 计算分类混淆、错误完结风险和调用开销；结果不包含汤底。
func Evaluate(ctx context.Context, decider Decider, cases []EvalCase) EvalReport {
	report := EvalReport{
		Total: len(cases), Items: make([]EvalItem, 0, len(cases)),
		QuestionConfusion: questionConfusion(),
		RestoreConfusion:  restoreConfusion(),
	}
	var latencies, promptTokens, completionTokens, totalTokens []float64
	for _, sample := range cases {
		if sample.Kind == "restore" {
			if sample.Expected == "正确" {
				report.RestorePass.ExpectedCorrect++
			} else {
				report.NonCorrectRestore++
			}
		}
		var decision Decision
		var err error
		switch sample.Kind {
		case "question":
			decision, err = decider.DecideQuestion(ctx, sample.Soup, sample.History, sample.Input)
		case "restore":
			decision, err = decider.JudgeRestore(ctx, sample.Soup, sample.History, sample.Input)
		default:
			err = ErrInvalidOutput
		}
		valid := sample.Kind == "question" && oneOf(decision.Result, "是", "否", "无关") ||
			sample.Kind == "restore" && oneOf(decision.Result, "正确", "不完整", "错误")
		if err == nil && !valid {
			err = ErrInvalidOutput
		}
		item := EvalItem{ID: sample.ID, Kind: sample.Kind, Expected: sample.Expected, Actual: decision.Result}
		if err != nil {
			report.Errors++
			item.Actual = ""
			item.Error = evalError(err)
		} else {
			report.AverageLatencyMS += decision.LatencyMS
			report.PromptTokens += decision.PromptTokens
			report.CompletionTokens += decision.CompletionTokens
			latencies = append(latencies, decision.LatencyMS)
			promptTokens = append(promptTokens, float64(decision.PromptTokens))
			completionTokens = append(completionTokens, float64(decision.CompletionTokens))
			totalTokens = append(totalTokens, float64(decision.PromptTokens+decision.CompletionTokens))
			if sample.Kind == "restore" {
				report.RestoreConfusion.add(sample.Expected, decision.Result)
				if sample.Expected != "正确" {
					report.NonCorrectEvaluated++
					if decision.Result == "正确" {
						report.FalseCorrectCount++
					}
				}
			} else {
				report.QuestionConfusion.add(sample.Expected, decision.Result)
			}
			if decision.Result == sample.Expected {
				report.Correct++
			}
		}
		report.Items = append(report.Items, item)
	}
	if report.Total > 0 {
		report.Accuracy = float64(report.Correct) / float64(report.Total)
	}
	if completed := report.Total - report.Errors; completed > 0 {
		report.AverageLatencyMS /= float64(completed)
	}
	if report.NonCorrectEvaluated > 0 {
		report.FalseCorrectRate = float64(report.FalseCorrectCount) / float64(report.NonCorrectEvaluated)
	}
	// 召回包含所有人工标注为正确的还原，即使模型超时或输出无效。
	pass := &report.RestorePass
	pass.TrueCorrect = report.RestoreConfusion.Counts[0][0]
	pass.PredictedCorrect = pass.TrueCorrect + report.FalseCorrectCount
	pass.Recall = metricRatio(pass.TrueCorrect, pass.ExpectedCorrect)
	pass.Precision = metricRatio(pass.TrueCorrect, pass.PredictedCorrect)
	report.QuestionF1, report.RestoreF1 = ClassificationF1(report.Items)
	report.LatencyDistribution = distribution(latencies)
	report.PromptTokenDistribution = distribution(promptTokens)
	report.CompletionTokenDistribution = distribution(completionTokens)
	report.TotalTokenDistribution = distribution(totalTokens)
	return report
}

func metricRatio(numerator, denominator int) *float64 {
	if denominator == 0 {
		return nil
	}
	ratio := float64(numerator) / float64(denominator)
	return &ratio
}
