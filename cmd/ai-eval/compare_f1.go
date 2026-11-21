package main

import "turtle-soup/internal/agent"

type classF1Delta struct {
	Label string   `json:"label"`
	F1    *float64 `json:"f1"`
}

type f1Delta struct {
	Classes []classF1Delta `json:"classes"`
	MacroF1 *float64       `json:"macroF1"`
}

// 逐类减去基线。两份指标均按同一份固定标签顺序从逐例判定重算。
func compareF1(base, next agent.ClassF1Summary) f1Delta {
	delta := f1Delta{Classes: make([]classF1Delta, 0, len(next.Classes)), MacroF1: subtractMetric(next.MacroF1, base.MacroF1)}
	for i, item := range next.Classes {
		delta.Classes = append(delta.Classes, classF1Delta{Label: item.Label, F1: subtractMetric(item.F1, base.Classes[i].F1)})
	}
	return delta
}

func subtractMetric(next, base *float64) *float64 {
	if next == nil || base == nil {
		return nil
	}
	delta := *next - *base
	return &delta
}
