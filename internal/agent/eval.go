package agent

import "context"

type EvalCase struct {
	ID       string `json:"id"`
	Soup     Soup   `json:"soup"`
	Kind     string `json:"kind"`
	Input    string `json:"input"`
	Expected string `json:"expected"`
}

type EvalItem struct {
	ID       string `json:"id"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
	Error    string `json:"error,omitempty"`
}

type EvalReport struct {
	Total            int        `json:"total"`
	Correct          int        `json:"correct"`
	Errors           int        `json:"errors"`
	Accuracy         float64    `json:"accuracy"`
	AverageLatencyMS float64    `json:"averageLatencyMs"`
	PromptTokens     int        `json:"promptTokens"`
	CompletionTokens int        `json:"completionTokens"`
	Items            []EvalItem `json:"items"`
}

// Evaluate 用固定题目计算准确率、错误率、延迟和 Token 开销；不输出汤底。
func Evaluate(ctx context.Context, decider Decider, cases []EvalCase) EvalReport {
	report := EvalReport{Total: len(cases), Items: make([]EvalItem, 0, len(cases))}
	for _, sample := range cases {
		var decision Decision
		var err error
		switch sample.Kind {
		case "question":
			decision, err = decider.DecideQuestion(ctx, sample.Soup, nil, sample.Input)
		case "restore":
			decision, err = decider.JudgeRestore(ctx, sample.Soup, nil, sample.Input)
		default:
			err = ErrInvalidOutput
		}
		item := EvalItem{ID: sample.ID, Expected: sample.Expected, Actual: decision.Result}
		if err != nil {
			report.Errors++
			item.Error = err.Error()
		} else {
			report.AverageLatencyMS += decision.LatencyMS
			report.PromptTokens += decision.PromptTokens
			report.CompletionTokens += decision.CompletionTokens
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
	return report
}
