package agent

import (
	"encoding/json"
	"fmt"
	"strings"
)

// 校验内嵌题集里的模型输入，题目的标题不影响已有的故事去重规则。
func validateUniqueEvalContent(cases []EvalCase) error {
	type inputKey struct {
		story                [2]string
		kind, input, history string
	}
	type firstCase struct{ id, label string }
	seen := make(map[inputKey]firstCase, len(cases))
	stories := make(map[Soup][2]string)
	for _, sample := range cases {
		story, ok := stories[sample.Soup]
		if !ok {
			story = holdoutStory(sample.Soup)
			stories[sample.Soup] = story
		}
		// 保留历史原文与顺序，无历史的空切片和 nil 视为相同输入。
		key := inputKey{story: story, kind: sample.Kind, input: strings.TrimSpace(sample.Input)}
		if len(sample.History) != 0 {
			data, err := json.Marshal(sample.History)
			if err != nil {
				return fmt.Errorf("评测样例历史无效: %s", sample.ID)
			}
			key.history = string(data)
		}
		if previous, found := seen[key]; found {
			if previous.label != sample.Expected {
				return fmt.Errorf("评测样例标签冲突: %s 与 %s", previous.id, sample.ID)
			}
			return fmt.Errorf("评测样例内容重复: %s 与 %s", previous.id, sample.ID)
		}
		seen[key] = firstCase{id: sample.ID, label: sample.Expected}
	}
	return nil
}
