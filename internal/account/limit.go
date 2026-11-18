package account

import (
	"time"

	"turtle-soup/internal/ratelimit"
)

const maxLimitEntries = 4096

// 注册和两种登录入口共用进程内限流器。
type requestLimiter struct {
	window *ratelimit.Window
	now    func() time.Time
}

func newRequestLimiter() *requestLimiter {
	return &requestLimiter{window: ratelimit.New(maxLimitEntries), now: time.Now}
}

func (l *requestLimiter) login(ip, name string) bool {
	return l.window.Allow(l.now,
		ratelimit.Rule{Key: "login:ip:" + ip, Max: 30, Window: 5 * time.Minute},
		ratelimit.Rule{Key: "login:account:" + name, Max: 30, Window: 5 * time.Minute},
	)
}

func (l *requestLimiter) register(ip string) bool {
	return l.window.Allow(l.now,
		ratelimit.Rule{Key: "register:ip:" + ip, Max: 10, Window: time.Hour},
	)
}
