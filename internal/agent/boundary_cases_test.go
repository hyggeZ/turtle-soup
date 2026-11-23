package agent

import (
	"reflect"
	"testing"
)

func TestLoadBoundaryCasesKeepsDevelopmentOrder(t *testing.T) {
	cases, groups, err := LoadBoundaryCases()
	if err != nil {
		t.Fatal(err)
	}
	const groupSize = 9
	wantGroups := []string{"boundary_weather", "boundary_museum", "boundary_runner", "boundary_backup"}
	if len(cases) != len(wantGroups)*groupSize || len(groups) != len(cases) {
		t.Fatalf("边界样例数=%d，映射数=%d，期望各有 36 项", len(cases), len(groups))
	}
	for i, sample := range cases {
		if group := groups[sample.ID]; group != wantGroups[i/groupSize] {
			t.Fatalf("第 %d 条边界题 %s 的分组为 %q，期望 %q", i+1, sample.ID, group, wantGroups[i/groupSize])
		}
	}
	dev, err := LoadEvalCases()
	if err != nil {
		t.Fatal(err)
	}
	if len(dev) < len(cases) || !reflect.DeepEqual(cases, dev[len(dev)-len(cases):]) {
		t.Fatal("独立加载改变了开发集尾部的边界题内容或顺序")
	}
}

func TestLoadBoundaryCasesRejectsDevelopmentConflicts(t *testing.T) {
	cases, groups, err := LoadBoundaryCases()
	if err != nil {
		t.Fatal(err)
	}
	id := cases[0].ID
	if _, _, err := loadBoundaryCases(map[string]bool{id: true}, map[string]bool{}); err == nil {
		t.Fatal("加载边界题时接受了相同的开发集样例 ID")
	}
	if _, _, err := loadBoundaryCases(map[string]bool{}, map[string]bool{groups[id]: true}); err == nil {
		t.Fatal("加载边界题时接受了相同的开发集故事 ID")
	}
}
