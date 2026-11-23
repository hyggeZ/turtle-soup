package agent

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed testdata/holdout_cases.json
var holdoutData []byte

// LoadHoldoutCases 加载独立验证题，不改变默认开发评测集。
func LoadHoldoutCases() ([]EvalCase, error) {
	dev, err := LoadEvalCases()
	if err != nil {
		return nil, err
	}
	return parseHoldoutCases(holdoutData, dev)
}

// 忽略空白和标题，避免同一个故事改标题后同时出现在两个题集里。
func holdoutStory(soup Soup) [2]string {
	return [2]string{strings.Join(strings.Fields(soup.Surface), ""), strings.Join(strings.Fields(soup.Answer), "")}
}

func parseHoldoutCases(data []byte, dev []EvalCase) ([]EvalCase, error) {
	var groups []boundaryGroup
	if err := json.Unmarshal(data, &groups); err != nil {
		return nil, fmt.Errorf("独立验证题集无效: %w", err)
	}
	if len(groups) == 0 {
		return nil, fmt.Errorf("独立验证题集不能为空")
	}
	stories := make(map[[2]string]bool)
	ids := make(map[string]bool)
	for _, sample := range dev {
		stories[holdoutStory(sample.Soup)] = true
		ids[sample.ID] = true
	}
	groupIDs := make(map[string]bool, len(groups))
	cases := make([]EvalCase, 0, len(groups)*6)
	for _, group := range groups {
		story := holdoutStory(group.Soup)
		if group.ID == "" || groupIDs[group.ID] || stories[story] {
			return nil, fmt.Errorf("独立验证故事无效或重复: %s", group.ID)
		}
		groupIDs[group.ID] = true
		stories[story] = true
		labels := make(map[string]bool, 6)
		for _, item := range group.Cases {
			sample := EvalCase{ID: item.ID, Soup: group.Soup, History: item.History, Kind: item.Kind, Input: item.Input, Expected: item.Expected}
			if !validEvalCase(sample) || !strings.HasPrefix(sample.ID, "holdout_") || ids[sample.ID] {
				return nil, fmt.Errorf("独立验证样例无效或重复: %s", item.ID)
			}
			ids[sample.ID] = true
			labels[sample.Expected] = true
			cases = append(cases, sample)
		}
		// 合法问答和还原各有三个不重叠的标签，共六种。
		if len(labels) != 6 {
			return nil, fmt.Errorf("独立验证故事缺少判定类别: %s", group.ID)
		}
	}
	if err := validateUniqueEvalContent(cases); err != nil {
		return nil, err
	}
	return cases, nil
}
