package agent

import (
	"encoding/json"
	"slices"
)

const (
	historyTurns = 24
	historyBytes = 4 << 10
)

// encodeHistory 限制发送给模型的历史 JSON 字节数，保留最近、完整且有序的问答。
// 本地按编码后字节计算，实际 Token 数由模型返回的 usage 提供。
func encodeHistory(history []Exchange) (json.RawMessage, error) {
	if len(history) > historyTurns {
		history = history[len(history)-historyTurns:]
	}
	// 原文已经超限时，可以直接跳过整段编码；转义可能让短原文也超限。
	textBytes := 0
	for _, item := range history {
		textBytes += len(item.Question) + len(item.Answer)
	}
	if textBytes <= historyBytes {
		data, err := json.Marshal(history)
		if err != nil || len(data) <= historyBytes {
			return data, err
		}
	}

	parts := make([]json.RawMessage, 0, len(history))
	size := 2 // JSON 数组的方括号。
	for i := len(history) - 1; i >= 0; i-- {
		item, err := json.Marshal(history[i])
		if err != nil {
			return nil, err
		}
		if len(parts) > 0 {
			size++ // 问答之间的逗号。
		}
		if size+len(item) > historyBytes {
			break
		}
		size += len(item)
		parts = append(parts, item)
	}
	slices.Reverse(parts)
	return json.Marshal(parts)
}
