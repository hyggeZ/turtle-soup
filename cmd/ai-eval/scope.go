package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"

	"turtle-soup/internal/agent"
)

// 记录实际送入模型的样例范围，供离线比较检查是否为同一套全量评测。
type evalScope struct {
	Suite        string `json:"suite"`
	Available    int    `json:"available"`
	Selected     int    `json:"selected"`
	SelectedHash string `json:"selectedHash"`
	Complete     bool   `json:"complete"`
}

func loadSuite(name string) ([]agent.EvalCase, map[string]string, error) {
	switch name {
	case "dev":
		cases, err := agent.LoadEvalCases()
		return cases, nil, err
	case "holdout":
		cases, err := agent.LoadHoldoutCases()
		return cases, nil, err
	case "boundary":
		return agent.LoadBoundaryCases()
	default:
		return nil, nil, errors.New("-suite 只支持 dev、holdout 或 boundary")
	}
}

func describeScope(suite string, available int, cases []agent.EvalCase, groups map[string]string) (evalScope, error) {
	// 指纹基于有序样例内容，与 JSON 文件的换行、缩进无关。
	var content any = cases
	if len(groups) != 0 {
		type groupedCase struct {
			Group string         `json:"group"`
			Case  agent.EvalCase `json:"case"`
		}
		grouped := make([]groupedCase, 0, len(cases))
		for _, item := range cases {
			group := groups[item.ID]
			if group == "" {
				return evalScope{}, errors.New("边界样例缺少分组")
			}
			grouped = append(grouped, groupedCase{Group: group, Case: item})
		}
		content = grouped
	}
	data, err := json.Marshal(content)
	if err != nil {
		return evalScope{}, err
	}
	return evalScope{
		Suite: suite, Available: available, Selected: len(cases),
		SelectedHash: fmt.Sprintf("%x", sha256.Sum256(data)), Complete: len(cases) == available,
	}, nil
}

func suiteHash(scope evalScope, cases []agent.EvalCase, groups map[string]string) (string, error) {
	// 保留开发集旧指纹，供已有报告核对原始题集文件。
	if scope.Suite == "dev" {
		return agent.EvalSetHash(), nil
	}
	if scope.Complete {
		return scope.SelectedHash, nil
	}
	full, err := describeScope(scope.Suite, len(cases), cases, groups)
	return full.SelectedHash, err
}
