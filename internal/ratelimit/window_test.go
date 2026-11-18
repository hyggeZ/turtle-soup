package ratelimit

import (
	"testing"
	"time"
)

func TestWindowRestoresOneSlotAtATime(t *testing.T) {
	now := time.Unix(0, 0)
	clock := func() time.Time { return now }
	limit := New(1)
	rule := Rule{Key: "account", Max: 3, Window: time.Second}

	for i := 0; i < 3; i++ {
		if !limit.Allow(clock, rule) {
			t.Fatal("额度提前用完")
		}
		now = now.Add(200 * time.Millisecond)
	}
	if limit.Allow(clock, rule) {
		t.Fatal("窗口内通过次数超过三次")
	}

	now = time.Unix(0, 0).Add(time.Second)
	if !limit.Allow(clock, rule) || limit.Allow(clock, rule) {
		t.Fatal("首条记录到期时应只恢复一个名额")
	}
	now = now.Add(200 * time.Millisecond)
	if !limit.Allow(clock, rule) || limit.Allow(clock, rule) {
		t.Fatal("第二个名额应按自己的时间恢复")
	}
}

func TestWindowDenialDoesNotConsumeOtherRule(t *testing.T) {
	clock := func() time.Time { return time.Unix(0, 0) }
	limit := New(2)
	account := Rule{Key: "account", Max: 1, Window: time.Second}
	ip := Rule{Key: "ip", Max: 1, Window: time.Second}

	if !limit.Allow(clock, account) || limit.Allow(clock, ip, account) || !limit.Allow(clock, ip) {
		t.Fatal("有一条规则未通过时，其他规则的额度不可被扣除")
	}
}

func TestWindowRecountsKeysAfterSweep(t *testing.T) {
	now := time.Unix(0, 0)
	clock := func() time.Time { return now }
	limit := New(2)
	short := Rule{Key: "short", Max: 1, Window: time.Second}
	long := Rule{Key: "long", Max: 1, Window: time.Minute}
	fresh := Rule{Key: "fresh", Max: 1, Window: time.Minute}

	if !limit.Allow(clock, short, long) {
		t.Fatal("初始容量应容纳两个键")
	}
	now = now.Add(time.Second)
	if limit.Allow(clock, short, fresh) || !limit.Allow(clock, fresh) {
		t.Fatal("回收后仍须为本次请求中的所有键预留空间")
	}
}
