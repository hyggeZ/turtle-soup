package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestHoldoutCoverageAndReplay(t *testing.T) {
	cases, err := LoadHoldoutCases()
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 24 {
		t.Fatalf("独立验证集应有 24 条样例，实际 %d 条", len(cases))
	}
	labels := make(map[string]int)
	stories := make(map[[2]string]int)
	replay := &evalReplay{results: make(map[string]Decision), history: make(map[string]int)}
	for _, sample := range cases {
		labels[sample.Kind+":"+sample.Expected]++
		stories[holdoutStory(sample.Soup)]++
		replay.results[sample.Input] = Decision{Result: sample.Expected}
	}
	for _, key := range []string{"question:是", "question:否", "question:无关", "restore:正确", "restore:不完整", "restore:错误"} {
		if labels[key] != 4 {
			t.Errorf("标签 %s 样例不足或不均衡：%d", key, labels[key])
		}
	}
	if len(stories) != 4 {
		t.Fatalf("应有四则独立故事，实际 %d 则", len(stories))
	}
	for _, count := range stories {
		if count != 6 {
			t.Fatalf("每则故事应有 6 条样例，实际 %d 条", count)
		}
	}
	report := Evaluate(context.Background(), replay, cases)
	if report.Total != 24 || report.Correct != 24 || report.Errors != 0 {
		t.Fatalf("离线回放未覆盖完整题集：total=%d correct=%d errors=%d", report.Total, report.Correct, report.Errors)
	}
	// 代号只出现在超出最近 12 轮的历史里，读入和评测层都必须保留它。
	refs := map[string]string{"holdout_clock_question_yes": "海燕", "holdout_kite_restore_correct": "松针"}
	for _, sample := range cases {
		ref, ok := refs[sample.ID]
		if !ok {
			continue
		}
		count := len(sample.History)
		if count <= 12 || replay.history[sample.Input] != count || !strings.Contains(sample.Input, ref) {
			t.Fatalf("长历史夹具丢失：%s 输入轮数=%d，传入轮数=%d", sample.ID, count, replay.history[sample.Input])
		}
		if !strings.Contains(sample.History[0].Question, ref) || strings.Contains(sample.Soup.Answer, ref) {
			t.Fatalf("长历史夹具没有独立早期代号：%s", sample.ID)
		}
		for _, exchange := range sample.History[count-12:] {
			if strings.Contains(exchange.Question, ref) || strings.Contains(exchange.Answer, ref) {
				t.Fatalf("早期代号出现在最近 12 轮：%s", sample.ID)
			}
		}
		delete(refs, sample.ID)
	}
	if len(refs) != 0 {
		t.Fatalf("缺少长历史样例：%v", refs)
	}
}

func TestHoldoutRejectsOverlapAndMissingLabel(t *testing.T) {
	var groups []boundaryGroup
	if err := json.Unmarshal(holdoutData, &groups); err != nil || len(groups) < 2 {
		t.Fatalf("无法加载验证题夹具：%v", err)
	}
	story := groups[0].Soup
	story.Title = "换一个标题"
	story.Surface = " " + story.Surface + "\t"
	if _, err := parseHoldoutCases(holdoutData, []EvalCase{{Soup: story}}); err == nil {
		t.Error("开发集和验证集不能包含同一个故事")
	}
	if _, err := parseHoldoutCases(holdoutData, []EvalCase{{ID: groups[0].Cases[0].ID}}); err == nil {
		t.Error("开发集和验证集不能共用样例 ID")
	}

	duplicate := groups[1]
	duplicate.Cases[0].ID = groups[0].Cases[0].ID
	data, err := json.Marshal([]boundaryGroup{groups[0], duplicate})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseHoldoutCases(data, nil); err == nil {
		t.Error("同一个验证题集不能共用样例 ID")
	}

	groups[0].Cases = groups[0].Cases[:5]
	data, err = json.Marshal(groups[:1])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseHoldoutCases(data, nil); err == nil {
		t.Error("验证故事缺少一个判定类别时应拒绝加载")
	}
}
