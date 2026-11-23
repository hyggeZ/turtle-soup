package main

import (
	"slices"
	"testing"

	"turtle-soup/internal/agent"
)

func TestScopeTracksExactSelection(t *testing.T) {
	cases, err := agent.LoadEvalCases()
	if err != nil {
		t.Fatal(err)
	}
	hashes := map[string]bool{}
	for _, count := range []int{1, 2, len(cases)} {
		selected := cases[:count]
		scope, err := describeScope("dev", len(cases), selected, nil)
		if err != nil || scope.Selected != count || scope.Complete != (count == len(cases)) || hashes[scope.SelectedHash] {
			t.Fatalf("第 %d 条范围错误：%+v %v", count, scope, err)
		}
		hashes[scope.SelectedHash] = true
		repeat, err := describeScope("dev", len(cases), selected, nil)
		if err != nil || scope != repeat {
			t.Fatalf("同一批样例的指纹发生变化：%+v %v", repeat, err)
		}
	}
	changed := slices.Clone(cases)
	changed[0].Soup.Answer += "补充线索"
	scope, err := describeScope("dev", len(cases), changed, nil)
	if err != nil || hashes[scope.SelectedHash] {
		t.Fatalf("样例内容变化应产生新指纹：%+v %v", scope, err)
	}
	changed = slices.Clone(cases)
	changed[0], changed[1] = changed[1], changed[0]
	scope, err = describeScope("dev", len(cases), changed, nil)
	if err != nil || hashes[scope.SelectedHash] {
		t.Fatalf("样例顺序变化应产生新指纹：%+v %v", scope, err)
	}
}
