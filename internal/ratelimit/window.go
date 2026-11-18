// Package ratelimit 提供单进程、按键计数的精确滑动窗口。
package ratelimit

import (
	"sync"
	"time"
)

// Rule 的键在同次检查中各不相同。同一个键始终使用固定的正数额度和窗口。
type Rule struct {
	Key    string
	Max    int
	Window time.Duration
}

type entry struct {
	times   []time.Duration
	oldest  int
	expires time.Duration
}

// Window 是一个有键数上限的滑动窗口。
type Window struct {
	mu      sync.Mutex
	entries map[string]entry
	maxKeys int
	origin  time.Time
	sweepAt time.Duration
}

func New(maxKeys int) *Window {
	return &Window{entries: make(map[string]entry), maxKeys: maxKeys}
}

// Allow 一次检查所有规则。只有全部通过时才记录这次请求。
func (w *Window) Allow(clock func() time.Time, rules ...Rule) bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	current := clock()
	if w.origin.IsZero() {
		w.origin = current
	}
	now := current.Sub(w.origin)

	added := 0
	for _, rule := range rules {
		item, ok := w.entries[rule.Key]
		if !ok {
			added++
			continue
		}
		if len(item.times) == rule.Max && item.times[item.oldest] > now-rule.Window {
			return false
		}
	}
	if len(w.entries)+added > w.maxKeys {
		if now < w.sweepAt {
			return false
		}
		w.sweep(now)
		// 刚被回收的键也可能在本次请求里，重新计算所需位置。
		added = 0
		for _, rule := range rules {
			if _, ok := w.entries[rule.Key]; !ok {
				added++
			}
		}
		if len(w.entries)+added > w.maxKeys {
			return false
		}
	}
	for _, rule := range rules {
		item := w.entries[rule.Key]
		item.record(now, rule.Max)
		item.expires = now + rule.Window
		w.rememberExpiry(item.expires)
		w.entries[rule.Key] = item
	}
	return true
}

func (w *Window) sweep(now time.Duration) {
	w.sweepAt = 0
	for key, item := range w.entries {
		if item.expires <= now {
			delete(w.entries, key)
			continue
		}
		w.rememberExpiry(item.expires)
	}
}

func (w *Window) rememberExpiry(expires time.Duration) {
	// 更新已有键只会延迟到期，因此过早的时间只会多触发一次检查。
	if w.sweepAt == 0 || expires < w.sweepAt {
		w.sweepAt = expires
	}
}

// 只保存最近 Max 次通过的时间。装满后用环覆盖最旧记录。
func (e *entry) record(now time.Duration, maxCount int) {
	size := len(e.times)
	if size < maxCount {
		if size == cap(e.times) {
			grown := make([]time.Duration, size, min(maxCount, max(1, size*2)))
			copy(grown, e.times)
			e.times = grown
		}
		e.times = append(e.times, now)
		return
	}
	e.times[e.oldest] = now
	e.oldest++
	if e.oldest == size {
		e.oldest = 0
	}
}
