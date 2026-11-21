package agent

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestContextHistoryKeepsRecentAndOlderEvidence(t *testing.T) {
	older := []Exchange{
		{Question: "‘海燕’指备用件吗？", Answer: "是"},
		{Question: "‘海燕号’已经入港了吗？", Answer: "否"},
		{Question: "木夹倒扣以后还在窗边吗？", Answer: "是"},
		{Question: "木夹倒扣以后还在窗边吗？", Answer: "是"},
		{Question: "‘海燕’指原来的断件吗？", Answer: "否"},
	}
	recent := make([]Exchange, historyTurns)
	for i := range recent {
		recent[i] = Exchange{Question: "钟楼靠近广场吗？", Answer: "是"}
	}
	history := append(slices.Clone(older), recent...)
	data, excerpt, err := contextHistory(history, "按前文，“海燕”和「木夹倒扣」相关吗？")
	if err != nil || !excerpt || len(data) > historyBytes {
		t.Fatalf("早期证据未加入：excerpt=%v bytes=%d err=%v", excerpt, len(data), err)
	}
	var got []Exchange
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	want := append([]Exchange{older[0], older[2], older[3], older[4]}, recent...)
	if !slices.Equal(got, want) {
		t.Fatal("应保留近期历史、重复次数、不同判定及原序")
	}
}

func TestContextHistoryFallsBackWithoutPartialEvidence(t *testing.T) {
	item := Exchange{Question: "‘海燕’指备用件吗？", Answer: "是"}
	history := make([]Exchange, historyTurns+contextEvidenceTurns+1)
	for i := range history {
		history[i] = item
	}
	assertContextFallback(t, history, "海燕指备用件吗？")
	assertContextFallback(t, history, "“海燕”指出现次数吗？")
	assertContextFallback(t, history[:14], "“海燕”指备用件吗？")
	history = history[1:]
	for i := range history[:contextEvidenceTurns] {
		history[i].Question += strings.Repeat("问", contextEvidenceBytes)
	}
	assertContextFallback(t, history, "“海燕”指备用件吗？")
}

func TestContextHistoryKeepsRecentAliasDefinition(t *testing.T) {
	older := []Exchange{{Question: "‘海燕’是朝北的门吗？", Answer: "是"}}
	recent := make([]Exchange, historyTurns)
	for i := range recent {
		recent[i] = Exchange{Question: "这是入口吗？", Answer: "无关"}
	}
	recent[len(recent)-1] = Exchange{Question: "改名后，‘海燕’表示东门吗？", Answer: "是"}
	history := append(older, recent...)
	assertContextFallback(t, history, "按最新说法，“海燕”是北门吗？")
}

func assertContextFallback(t *testing.T, history []Exchange, input string) {
	t.Helper()
	want, err := encodeHistory(history)
	if err != nil {
		t.Fatal(err)
	}
	got, excerpt, err := contextHistory(history, input)
	if err != nil || excerpt || string(got) != string(want) {
		t.Fatalf("回退请求改变：excerpt=%v err=%v", excerpt, err)
	}
}
