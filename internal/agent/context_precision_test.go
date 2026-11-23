package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"testing"
)

type contextPrecisionCase struct {
	ID          string `json:"id"`
	History     string `json:"history"`
	Input       string `json:"input"`
	EarlyRounds []int  `json:"earlyRounds"`
}

type contextPrecisionFixture struct {
	Histories map[string][]Exchange  `json:"histories"`
	Cases     []contextPrecisionCase `json:"cases"`
}

// 使用第 165 条的两套合成长局。标注轮次从 1 开始，重复问答仍是不同轮次。
func loadContextPrecisionFixture(t testing.TB) contextPrecisionFixture {
	t.Helper()
	data, err := os.ReadFile("testdata/context_precision_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture contextPrecisionFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Histories) != 2 || len(fixture.Cases) != 14 {
		t.Fatal("证据评测应包含两组长局及 14 个样例")
	}
	seen := make(map[string]bool, len(fixture.Cases))
	for _, sample := range fixture.Cases {
		history := fixture.Histories[sample.History]
		if sample.ID == "" || sample.Input == "" || seen[sample.ID] || len(history) != 50 || sample.EarlyRounds == nil {
			t.Fatalf("证据评测样例无效：%+v", sample)
		}
		seen[sample.ID] = true
		last := max(0, len(history)-contextHistoryTurns)
		for _, round := range sample.EarlyRounds {
			if round <= last || round > len(history)-historyTurns {
				t.Fatalf("%s 的旧证据轮次无效：%v", sample.ID, sample.EarlyRounds)
			}
			last = round
		}
	}
	return fixture
}

type contextPrecisionTotals struct {
	picked          int
	relevant        int
	marked          int
	withoutEvidence int
	falseRetrieval  int
	jsonBytes       int
}

func (m *contextPrecisionTotals) add(picked, marked []int, size int) {
	m.picked += len(picked)
	m.marked += len(marked)
	m.jsonBytes += size
	for _, round := range picked {
		if slices.Contains(marked, round) {
			m.relevant++
		}
	}
	if len(marked) == 0 {
		m.withoutEvidence++
		if len(picked) > 0 {
			m.falseRetrieval++
		}
	}
}

func evidenceRatio(numerator, denominator int) string {
	if denominator == 0 {
		return "未定义"
	}
	return fmt.Sprintf("%.2f%%", 100*float64(numerator)/float64(denominator))
}

func (m contextPrecisionTotals) log(t *testing.T, name string) {
	t.Helper()
	t.Logf("%s 选中=%d 命中=%d 标注=%d 精确率=%s 召回率=%s 无早期证据误取=%d/%d 历史JSON字节=%d",
		name, m.picked, m.relevant, m.marked, evidenceRatio(m.relevant, m.picked), evidenceRatio(m.relevant, m.marked),
		m.falseRetrieval, m.withoutEvidence, m.jsonBytes)
}

// 将 JSON 中的旧问答按原顺序定位回历史。游标只向前移动，相同问答也只能各用一次。
func evidenceRounds(t *testing.T, history, old []Exchange) []int {
	t.Helper()
	next := max(0, len(history)-contextHistoryTurns)
	end := len(history) - historyTurns
	rounds := make([]int, 0, len(old))
	for _, item := range old {
		for next < end && history[next] != item {
			next++
		}
		if next >= end {
			t.Fatal("选入的旧证据不是同局原文，或重复次数、顺序不正确")
		}
		rounds = append(rounds, next+1)
		next++
	}
	return rounds
}

func TestContextPrecision(t *testing.T) {
	fixture := loadContextPrecisionFixture(t)
	var baseline, current contextPrecisionTotals
	for _, sample := range fixture.Cases {
		t.Run(sample.ID, func(t *testing.T) {
			history := fixture.Histories[sample.History]
			base, err := encodeHistory(history)
			if err != nil {
				t.Fatal(err)
			}
			data, excerpt, err := contextHistory(history, sample.Input)
			if err != nil || len(data) > historyBytes {
				t.Fatalf("检索失败或超过历史预算：%v，字节=%d", err, len(data))
			}
			var recent, combined []Exchange
			if err := json.Unmarshal(base, &recent); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(data, &combined); err != nil {
				t.Fatal(err)
			}
			count := len(combined) - len(recent)
			if len(recent) != historyTurns || !slices.Equal(recent, history[len(history)-historyTurns:]) ||
				count < 0 || count > contextEvidenceTurns || excerpt != (count > 0) || !slices.Equal(combined[count:], recent) {
				t.Fatalf("新旧策略未保留相同的近期问答：近期=%d，旧证据=%d，摘录=%v", len(recent), count, excerpt)
			}
			picked := evidenceRounds(t, history, combined[:count])
			baseline.add(nil, sample.EarlyRounds, len(base))
			current.add(picked, sample.EarlyRounds, len(data))
			t.Logf("标注轮次=%v 选中轮次=%v 历史JSON字节=%d→%d", sample.EarlyRounds, picked, len(base), len(data))
			if sample.ID != "early_alias_without_quotes_yes" && !slices.Equal(picked, sample.EarlyRounds) {
				t.Error("本样例选入的旧证据与人工标注不一致")
			}
		})
	}
	baseline.log(t, "最近24轮")
	current.log(t, "现行检索")
	// 当前有一个无引号代号未命中。允许后续补齐，但其余样例不得丢证据或引入无关原文。
	if current.marked != 10 || current.withoutEvidence != 7 || current.relevant < 9 ||
		current.picked != current.relevant || current.falseRetrieval != 0 {
		t.Fatalf("旧证据检索回归：%+v", current)
	}
}

func BenchmarkContextPrecision(b *testing.B) {
	fixture := loadContextPrecisionFixture(b)
	b.Run("Recent24", func(b *testing.B) {
		b.ReportAllocs()
		i := 0
		for b.Loop() {
			sample := fixture.Cases[i%len(fixture.Cases)]
			if _, err := encodeHistory(fixture.Histories[sample.History]); err != nil {
				b.Fatal(err)
			}
			i++
		}
	})
	b.Run("RetrieveEarlier", func(b *testing.B) {
		b.ReportAllocs()
		i := 0
		for b.Loop() {
			sample := fixture.Cases[i%len(fixture.Cases)]
			if _, _, err := contextHistory(fixture.Histories[sample.History], sample.Input); err != nil {
				b.Fatal(err)
			}
			i++
		}
	})
}
