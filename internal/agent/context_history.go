package agent

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	contextHistoryTurns  = 64
	contextEvidenceTurns = 6
	contextEvidenceBytes = 1024
)

var contextQuotes = regexp.MustCompile(`“([^”]{2,20})”|「([^」]{2,20})」|‘([^’]{2,20})’|"([^"]{2,20})"`)

func quotedAliases(input string) map[string]bool {
	aliases := make(map[string]bool)
	for _, match := range contextQuotes.FindAllStringSubmatch(input, -1) {
		for _, label := range match[1:] {
			if label != "" {
				aliases[label] = true
			}
		}
	}
	return aliases
}

// 补入同局较早的原始问答。所有匹配记录一起进入剩余预算，或保留原请求。
func contextHistory(history []Exchange, input string) (json.RawMessage, bool, error) {
	recent, err := encodeHistory(history)
	if err != nil || len(history) <= historyTurns || len(recent) <= 2 {
		return recent, false, err
	}
	aliases := quotedAliases(input)
	if len(aliases) == 0 {
		return recent, false, nil
	}
	if len(history) > contextHistoryTurns {
		history = history[len(history)-contextHistoryTurns:]
	}
	boundary := len(history) - historyTurns
	// 最近问答已使用的短代号优先，避免已作废的定义和新定义同时进入请求。
	for _, item := range history[boundary:] {
		for label := range quotedAliases(item.Question) {
			if utf8.RuneCountInString(label) < 4 {
				delete(aliases, label)
			}
		}
	}
	if len(aliases) == 0 {
		return recent, false, nil
	}
	evidence := make([]Exchange, 0, contextEvidenceTurns)
	for _, item := range history[:boundary] {
		if !refersToAlias(item.Question, aliases) {
			continue
		}
		if len(evidence) == contextEvidenceTurns {
			return recent, false, nil
		}
		evidence = append(evidence, item)
	}
	if len(evidence) == 0 {
		return recent, false, nil
	}
	older, err := json.Marshal(evidence)
	if err != nil || len(older) > contextEvidenceBytes || len(older)+len(recent)-1 > historyBytes {
		return recent, false, err
	}
	combined := make(json.RawMessage, 0, len(older)+len(recent)-1)
	combined = append(combined, older[:len(older)-1]...)
	combined = append(combined, ',')
	combined = append(combined, recent[1:]...)
	return combined, true, nil
}

func refersToAlias(question string, aliases map[string]bool) bool {
	for label := range quotedAliases(question) {
		if aliases[label] {
			return true
		}
	}
	// 完整短语可在早期问句中自然出现，短代号仍要求引号内精确对应。
	for label := range aliases {
		if utf8.RuneCountInString(label) >= 4 && strings.Contains(question, label) {
			return true
		}
	}
	return false
}
