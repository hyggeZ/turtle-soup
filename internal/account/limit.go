package account

import (
	"sync"
	"time"
)

const maxLimitEntries = 4096

type limitRule struct {
	key    string
	max    int
	window time.Duration
}

type limitEntry struct {
	count   int
	expires time.Time
}

// requestLimiter 是单进程短时限流器，记录数有上限。
type requestLimiter struct {
	mu      sync.Mutex
	entries map[string]limitEntry
	now     func() time.Time
}

func newRequestLimiter() *requestLimiter {
	return &requestLimiter{
		entries: make(map[string]limitEntry),
		now:     time.Now,
	}
}

// 多个规则在同一把锁内检查和扣除，避免并发请求穿过额度。
func (l *requestLimiter) allow(rules ...limitRule) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	for _, rule := range rules {
		entry, exists := l.entries[rule.key]
		if exists && now.Before(entry.expires) && entry.count >= rule.max {
			return false
		}
	}
	if len(l.entries)+len(rules) > maxLimitEntries {
		for key, entry := range l.entries {
			if !now.Before(entry.expires) {
				delete(l.entries, key)
			}
		}
	}
	newKeys := 0
	for _, rule := range rules {
		if _, exists := l.entries[rule.key]; !exists {
			newKeys++
		}
	}
	if len(l.entries)+newKeys > maxLimitEntries {
		return false
	}
	for _, rule := range rules {
		entry := l.entries[rule.key]
		if !now.Before(entry.expires) {
			entry = limitEntry{expires: now.Add(rule.window)}
		}
		entry.count++
		l.entries[rule.key] = entry
	}
	return true
}

func (l *requestLimiter) login(ip, name string) bool {
	return l.allow(
		limitRule{key: "login:ip:" + ip, max: 30, window: 5 * time.Minute},
		limitRule{key: "login:account:" + name, max: 30, window: 5 * time.Minute},
	)
}

func (l *requestLimiter) register(ip string) bool {
	return l.allow(limitRule{key: "register:ip:" + ip, max: 10, window: time.Hour})
}
