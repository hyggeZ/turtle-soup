package room

import (
	"strconv"
	"time"

	"turtle-soup/internal/ratelimit"
)

const codeWindow = 5 * time.Minute
const maxCodeEntries = 4096
const maxUserAttempts = 30
const maxIPAttempts = 120

// codeLimiter 同时限制 IP 和当前账号的房间码查询。
type codeLimiter struct {
	window *ratelimit.Window
	now    func() time.Time
}

func newCodeLimiter() *codeLimiter {
	return &codeLimiter{window: ratelimit.New(maxCodeEntries), now: time.Now}
}

func (l *codeLimiter) allow(ip string, user int64) bool {
	return l.window.Allow(l.now,
		ratelimit.Rule{Key: "code:ip:" + ip, Max: maxIPAttempts, Window: codeWindow},
		ratelimit.Rule{Key: "code:user:" + strconv.FormatInt(user, 10), Max: maxUserAttempts, Window: codeWindow},
	)
}
