package room

import (
	"strconv"
	"sync"
	"time"
)

const codeWindow = 5 * time.Minute
const maxCodeEntries = 4096
const maxUserAttempts = 30
const maxIPAttempts = 120

type codeEntry struct {
	count   int
	expires time.Time
}

// codeLimiter 限制试码次数；已在房间中的成员可在限额耗尽后重连。
type codeLimiter struct {
	mu      sync.Mutex
	entries map[string]codeEntry
	now     func() time.Time
}

func newCodeLimiter() *codeLimiter {
	return &codeLimiter{entries: make(map[string]codeEntry), now: time.Now}
}

func (l *codeLimiter) allow(ip string, user int64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	keys := [2]string{"code:ip:" + ip, "code:user:" + strconv.FormatInt(user, 10)}
	limits := [2]int{maxIPAttempts, maxUserAttempts}
	for i, key := range keys {
		entry := l.entries[key]
		if now.Before(entry.expires) && entry.count >= limits[i] {
			return false
		}
	}

	if len(l.entries)+len(keys) > maxCodeEntries {
		for key, entry := range l.entries {
			if !now.Before(entry.expires) {
				delete(l.entries, key)
			}
		}
	}
	newKeys := 0
	for _, key := range keys {
		if _, exists := l.entries[key]; !exists {
			newKeys++
		}
	}
	if len(l.entries)+newKeys > maxCodeEntries {
		return false
	}
	for _, key := range keys {
		entry := l.entries[key]
		if !now.Before(entry.expires) {
			entry = codeEntry{expires: now.Add(codeWindow)}
		}
		entry.count++
		l.entries[key] = entry
	}
	return true
}
