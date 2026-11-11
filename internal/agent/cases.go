package agent

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

//go:embed testdata/eval_cases.json
var baseEvalData []byte

//go:embed testdata/restore_cases.json
var restoreEvalData []byte

//go:embed testdata/boundary_cases.json
var boundaryEvalData []byte

type restoreGroup struct {
	ID    string `json:"id"`
	Soup  Soup   `json:"soup"`
	Cases []struct {
		ID       string `json:"id"`
		Input    string `json:"input"`
		Expected string `json:"expected"`
	} `json:"cases"`
}

// 边界题按故事分组，避免同一汤底在多条问答中重复书写。
type boundaryGroup struct {
	ID    string `json:"id"`
	Soup  Soup   `json:"soup"`
	Cases []struct {
		ID       string     `json:"id"`
		Kind     string     `json:"kind"`
		Input    string     `json:"input"`
		Expected string     `json:"expected"`
		History  []Exchange `json:"history,omitempty"`
	} `json:"cases"`
}

// LoadEvalCases 从程序内嵌文件读取固定评测集，不依赖部署机器的工作目录。
func LoadEvalCases() ([]EvalCase, error) {
	var cases []EvalCase
	if err := json.Unmarshal(baseEvalData, &cases); err != nil {
		return nil, fmt.Errorf("基础评测集无效: %w", err)
	}
	var groups []restoreGroup
	if err := json.Unmarshal(restoreEvalData, &groups); err != nil {
		return nil, fmt.Errorf("还原评测集无效: %w", err)
	}
	var boundaries []boundaryGroup
	if err := json.Unmarshal(boundaryEvalData, &boundaries); err != nil {
		return nil, fmt.Errorf("边界评测集无效: %w", err)
	}
	if len(cases) < 8 || len(groups) < 20 || len(boundaries) < 4 {
		return nil, errors.New("评测集样例数量不足")
	}
	ids := make(map[string]bool, len(cases)+len(groups)*5)
	for _, sample := range cases {
		if !validEvalCase(sample) || ids[sample.ID] {
			return nil, fmt.Errorf("评测样例无效或重复: %s", sample.ID)
		}
		ids[sample.ID] = true
	}
	groupIDs := make(map[string]bool, len(groups))
	for _, group := range groups {
		if group.ID == "" || groupIDs[group.ID] || group.Soup.Title == "" || group.Soup.Surface == "" || group.Soup.Answer == "" || len(group.Cases) < 5 {
			return nil, fmt.Errorf("还原题目无效或重复: %s", group.ID)
		}
		groupIDs[group.ID] = true
		labels := map[string]int{}
		for _, item := range group.Cases {
			sample := EvalCase{ID: item.ID, Soup: group.Soup, Kind: "restore", Input: item.Input, Expected: item.Expected}
			if !validEvalCase(sample) || ids[item.ID] {
				return nil, fmt.Errorf("还原样例无效或重复: %s", item.ID)
			}
			ids[item.ID] = true
			labels[item.Expected]++
			cases = append(cases, sample)
		}
		if labels["正确"] == 0 || labels["不完整"] == 0 || labels["错误"] == 0 {
			return nil, fmt.Errorf("还原题目缺少判定类别: %s", group.ID)
		}
	}
	for _, group := range boundaries {
		if group.ID == "" || groupIDs[group.ID] || group.Soup.Title == "" || group.Soup.Surface == "" || group.Soup.Answer == "" || len(group.Cases) < 9 {
			return nil, fmt.Errorf("边界题目无效或重复: %s", group.ID)
		}
		groupIDs[group.ID] = true
		for _, item := range group.Cases {
			sample := EvalCase{ID: item.ID, Soup: group.Soup, History: item.History, Kind: item.Kind, Input: item.Input, Expected: item.Expected}
			if !validEvalCase(sample) || ids[item.ID] {
				return nil, fmt.Errorf("边界样例无效或重复: %s", item.ID)
			}
			ids[item.ID] = true
			cases = append(cases, sample)
		}
	}
	return cases, nil
}

func validEvalCase(sample EvalCase) bool {
	if sample.ID == "" || sample.Soup.Title == "" || sample.Soup.Surface == "" || sample.Soup.Answer == "" || sample.Input == "" {
		return false
	}
	if sample.Kind == "question" {
		return oneOf(sample.Expected, "是", "否", "无关")
	}
	return sample.Kind == "restore" && oneOf(sample.Expected, "正确", "不完整", "错误")
}

// EvalSetHash 标记本次运行所用的三份固定样例文件，便于比较不同模型结果。
func EvalSetHash() string {
	hash := sha256.New()
	_, _ = hash.Write(baseEvalData)
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(restoreEvalData)
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(boundaryEvalData)
	return hex.EncodeToString(hash.Sum(nil))
}
