package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHoldoutRejectsRepeatedContentWithDifferentID(t *testing.T) {
	for _, sample := range []struct {
		name  string
		label string
		want  string
	}{{"duplicate", "是", "内容重复"}, {"conflict", "否", "标签冲突"}} {
		t.Run(sample.name, func(t *testing.T) {
			var groups []boundaryGroup
			if err := json.Unmarshal(holdoutData, &groups); err != nil {
				t.Fatal(err)
			}
			first := groups[0].Cases[0]
			if first.Kind != "question" || first.Expected != "是" {
				t.Fatal("首条合成样例需是正向问答")
			}
			copy := first
			copy.ID += "_copy"
			copy.Input = "\t" + copy.Input + "\n"
			copy.Expected = sample.label
			groups[0].Cases = append(groups[0].Cases, copy)
			data, err := json.Marshal(groups)
			if err != nil {
				t.Fatal(err)
			}
			_, err = parseHoldoutCases(data, nil)
			if err == nil || !strings.Contains(err.Error(), sample.want) || !strings.Contains(err.Error(), first.ID+" 与 "+copy.ID) {
				t.Fatalf("应拒绝新增样例并说明两个 ID，实际 %v", err)
			}
		})
	}
}

func TestEvalContentKeepsDifferentContextsSeparate(t *testing.T) {
	first := EvalCase{
		ID: "first", Kind: "question", Input: "停电发生了吗？", Expected: "是",
		Soup:    Soup{Title: "车站", Surface: "车站突然变暗", Answer: "值班员关灯"},
		History: []Exchange{{Question: "有人值班吗？", Answer: "是"}, {Question: "有故障吗？", Answer: "否"}},
	}
	otherHistory := first
	otherHistory.ID = "history"
	otherHistory.History = []Exchange{first.History[1], first.History[0]}
	otherStory := first
	otherStory.ID = "story"
	otherStory.Soup.Answer = "站里的备用灯发生故障"
	otherKind := first
	otherKind.ID, otherKind.Kind, otherKind.Expected = "kind", "restore", "不完整"
	if err := validateUniqueEvalContent([]EvalCase{first, otherHistory, otherStory, otherKind}); err != nil {
		t.Fatalf("不同故事、类型或历史顺序可以使用相同输入：%v", err)
	}

	first.History = nil
	same := first
	same.ID, same.Soup.Title, same.History = "same", "另一个标题", []Exchange{}
	if err := validateUniqueEvalContent([]EvalCase{first, same}); err == nil || !strings.Contains(err.Error(), "内容重复") {
		t.Fatalf("改标题或用空切片表示无历史仍应识别为重复：%v", err)
	}
}
