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

func loadSuite(name string) ([]agent.EvalCase, error) {
	switch name {
	case "dev":
		return agent.LoadEvalCases()
	case "holdout":
		return agent.LoadHoldoutCases()
	default:
		return nil, errors.New("-suite 只支持 dev 或 holdout")
	}
}

func describeScope(suite string, available int, cases []agent.EvalCase) (evalScope, error) {
	// 指纹基于有序样例内容，与 JSON 文件的换行、缩进无关。
	data, err := json.Marshal(cases)
	if err != nil {
		return evalScope{}, err
	}
	return evalScope{
		Suite: suite, Available: available, Selected: len(cases),
		SelectedHash: fmt.Sprintf("%x", sha256.Sum256(data)), Complete: len(cases) == available,
	}, nil
}

func suiteHash(scope evalScope, cases []agent.EvalCase) (string, error) {
	// 保留开发集旧指纹，供已有报告核对原始题集文件。
	if scope.Suite == "dev" {
		return agent.EvalSetHash(), nil
	}
	if scope.Complete {
		return scope.SelectedHash, nil
	}
	full, err := describeScope(scope.Suite, len(cases), cases)
	return full.SelectedHash, err
}
