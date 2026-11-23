package main

import (
	"slices"

	"turtle-soup/internal/agent"
)

const maxBootstrapClusters = 6

type pairedAccuracyInterval struct {
	ObservedDelta float64 `json:"observedDelta"`
	Lower         float64 `json:"lower"`
	Upper         float64 `json:"upper"`
	Confidence    float64 `json:"confidence"`
	Clusters      int     `json:"clusters"`
	Resamples     int     `json:"resamples"`
}

// 同一故事内的样例一起抽取，且新旧判定始终使用同一批样例。输入已按题集校验。
func bootstrapAccuracy(cases []agent.EvalCase, base, next []agent.EvalItem) *pairedAccuracyInterval {
	type cluster struct{ total, delta int }
	groups := make([]cluster, 0, maxBootstrapClusters)
	indices := make(map[[2]string]int, maxBootstrapClusters)
	observed := 0
	for i, sample := range cases {
		story := [2]string{sample.Soup.Surface, sample.Soup.Answer}
		group, exists := indices[story]
		if !exists {
			if len(groups) == maxBootstrapClusters {
				return nil
			}
			group = len(groups)
			indices[story] = group
			groups = append(groups, cluster{})
		}
		delta := correctIndicator(next[i]) - correctIndicator(base[i])
		groups[group].delta += delta
		groups[group].total++
		observed += delta
	}
	n := len(groups)
	if n < 2 {
		return nil
	}
	resamples := 1
	for range n {
		resamples *= n
	}
	deltas := make([]float64, resamples)
	for i := range deltas {
		// 将编号看成 n 进制的 n 个数字，枚举整簇有放回抽样。四个故事得到 4^4 组。
		seq, count, delta := i, 0, 0
		for range n {
			picked := groups[seq%n]
			seq /= n
			count += picked.total
			delta += picked.delta
		}
		deltas[i] = float64(delta) / float64(count)
	}
	slices.Sort(deltas)
	// 最近秩定义为向上取整（分位数 × 样本数），再转为从零开始的下标。
	lower := (25*resamples+999)/1000 - 1
	upper := (975*resamples+999)/1000 - 1
	return &pairedAccuracyInterval{
		ObservedDelta: float64(observed) / float64(len(cases)),
		Lower:         deltas[lower], Upper: deltas[upper], Confidence: 0.95,
		Clusters: n, Resamples: resamples,
	}
}

func correctIndicator(item agent.EvalItem) int {
	if item.Error == "" && item.Actual == item.Expected {
		return 1
	}
	return 0
}
