package telemetry

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
)

const (
	restoreBlockBytes = 64 << 10
	restoreLineBytes  = 1 << 20
)

// 优先恢复当前日志，记录不足时才打开上一代，最后还原文件追加顺序。
func (s *Store) loadRecent() {
	s.loadRecentFile("requests.jsonl", true)
	if len(s.records) < maxRecords {
		s.loadRecentFile("requests.previous.jsonl", false)
	}
	slices.Reverse(s.records)
}

// 每次从文件尾部读取一块。跨块的行暂存为倒序字节，读到行首再翻转。
func (s *Store) loadRecentFile(name string, current bool) {
	file, err := os.Open(filepath.Join(s.dir, name))
	if err != nil {
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return
	}

	var buf [restoreBlockBytes]byte
	var tail []byte
	large := false
	for end := info.Size(); end > 0 && len(s.records) < maxRecords; {
		start := max(end-restoreBlockBytes, 0)
		block := buf[:end-start]
		if _, err := file.ReadAt(block, start); err != nil {
			return
		}
		// 保留原末行，并让此后追加的日志从新的一行开始。
		if current && end == info.Size() && block[len(block)-1] != '\n' {
			if _, err := s.file.Write([]byte{'\n'}); err != nil {
				s.writeError = err.Error()
			}
		}
		end = start
		for len(block) > 0 && len(s.records) < maxRecords {
			lf := bytes.LastIndexByte(block, '\n')
			part := block[lf+1:]
			complete := lf >= 0 || start == 0
			large = large || len(part)+len(tail) >= restoreLineBytes
			if !large {
				if len(tail) > 0 || !complete {
					before := len(tail)
					tail = append(tail, part...)
					slices.Reverse(tail[before:])
					part = tail
				}
				if complete && len(part) > 0 {
					slices.Reverse(tail)
					s.restoreRecord(part)
				}
			}
			if complete {
				tail = tail[:0]
				large = false
			}
			block = block[:max(lf, 0)]
		}
	}
}

// 坏行与缺少请求标识的记录不占最近日志的名额。
func (s *Store) restoreRecord(line []byte) {
	var record Record
	if json.Unmarshal(line, &record) == nil && record.TraceID != "" {
		s.records = append(s.records, record)
	}
}
