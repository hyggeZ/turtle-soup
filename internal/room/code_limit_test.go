package room

import (
	"strconv"
	"testing"
	"time"
)

func TestCodeLimiterUserAndIP(t *testing.T) {
	now := time.Now()
	byUser := newCodeLimiter()
	byUser.now = func() time.Time { return now }
	for i := 0; i < maxUserAttempts; i++ {
		if !byUser.allow("ip:"+strconv.Itoa(i), 1) {
			t.Fatalf("用户第 %d 次试码被提前拒绝", i+1)
		}
	}
	if byUser.allow("another-ip", 1) {
		t.Fatal("更换 IP 后绕过了账号限额")
	}
	now = now.Add(codeWindow + time.Nanosecond)
	if !byUser.allow("another-ip", 1) {
		t.Fatal("窗口结束后仍被限制")
	}

	byIP := newCodeLimiter()
	byIP.now = func() time.Time { return now }
	for i := 0; i < maxIPAttempts; i++ {
		if !byIP.allow("shared-ip", int64(i+1)) {
			t.Fatalf("IP 第 %d 次试码被提前拒绝", i+1)
		}
	}
	if byIP.allow("shared-ip", 1000) {
		t.Fatal("更换账号后绕过了 IP 限额")
	}
}

func TestValidRoomCode(t *testing.T) {
	for _, code := range []string{"01234567", "0123456789ABCDEF0123456789ABCDEF"} {
		if !validRoomCode(code) {
			t.Fatalf("有效房间码被拒绝：%s", code)
		}
	}
	for _, code := range []string{"", "1234567", "0123456G", "0123456789ABCDEF0123456789ABCDEG"} {
		if validRoomCode(code) {
			t.Fatalf("无效房间码被接受：%s", code)
		}
	}
}
