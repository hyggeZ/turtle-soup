package agent

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestHistoryBudgetPreservesShortRounds(t *testing.T) {
	history := make([]Exchange, 14)
	for i := range history {
		history[i] = Exchange{Question: fmt.Sprintf("第 %d 条", i), Answer: "是"}
	}
	history[1] = history[0] // 同一问题出现几次也可能是线索。
	for _, limit := range []int{0, 8, 14} {
		want, err := json.Marshal(history[:limit])
		if err != nil {
			t.Fatal(err)
		}
		got, err := encodeHistory(history[:limit])
		if err != nil || string(got) != string(want) {
			t.Fatalf("第 %d 轮以内的问答变化：%s %v", limit, got, err)
		}
	}
}

func TestHistoryBudgetCapsTurnsAndEncodedBytes(t *testing.T) {
	history := make([]Exchange, historyTurns+6)
	for i := range history {
		history[i] = Exchange{Question: fmt.Sprintf("第 %d 条", i), Answer: "是"}
	}
	if got := decodeBudgetHistory(t, history); !slices.Equal(got, history[6:]) {
		t.Fatal("轮数上限应保留按时间排序的最后 24 轮")
	}
	for i := range history {
		history[i].Question += strings.Repeat("汉字🙂\n\"<&", 34)
	}
	got := decodeBudgetHistory(t, history)
	start := len(history) - len(got)
	if start <= 6 || len(got) == 0 || !slices.Equal(got, history[start:]) {
		t.Fatal("字节上限应按完整问答从前面裁剪")
	}
	longer, err := json.Marshal(history[start-1:])
	if err != nil || len(longer) <= historyBytes {
		t.Fatal("还能加入的最近一轮被错误裁剪")
	}
	oversize := []Exchange{{Question: strings.Repeat("海", historyBytes), Answer: "是"}}
	if got := decodeBudgetHistory(t, oversize); len(got) != 0 {
		t.Fatal("超出预算的单条问答应整条排除")
	}
	escaped := make([]Exchange, 8)
	for i := range escaped {
		escaped[i] = Exchange{Question: strings.Repeat("<", 120), Answer: "是"}
	}
	if got := decodeBudgetHistory(t, escaped); len(got) == 0 || len(got) == len(escaped) {
		t.Fatal("预算应以转义后的 JSON 为准")
	}
}

func decodeBudgetHistory(t *testing.T, history []Exchange) []Exchange {
	t.Helper()
	data, err := encodeHistory(history)
	if err != nil || len(data) > historyBytes {
		t.Fatalf("历史编码未满足预算：长度=%d 错误=%v", len(data), err)
	}
	var got []Exchange
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	return got
}
