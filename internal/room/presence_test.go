package room

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"turtle-soup/internal/account"
)

func TestLiveHubPresenceEdges(t *testing.T) {
	hub := newLiveHub()
	observer, _, _ := hub.join(1, 1, liveSpectator)
	hub.activate(1, observer, nil)
	first, _, _ := hub.join(1, 2, liveHost)
	second, _, _ := hub.join(1, 2, liveHost)
	if hub.onlineUsers(1)[2] != 0 {
		t.Fatal("预留名额被计为在线")
	}
	if ok, joined := hub.activate(1, first, nil); !ok || !joined {
		t.Fatal("首个连接没有产生上线边缘")
	}
	if ok, joined := hub.activate(1, second, nil); !ok || joined {
		t.Fatal("第二个标签页重复产生上线边缘")
	}
	if ok, joined := hub.activate(1, second, nil); !ok || joined {
		t.Fatal("重复激活修改了在线状态")
	}
	snapshot := hub.onlineUsers(1)
	snapshot[2] = 0
	if hub.onlineUsers(1)[2] != 2 || hub.onlineUsers(2)[2] != 0 {
		t.Fatal("在线计数可被快照修改或串到了其他房间")
	}
	hub.notifyExcept(1, first)
	if len(first) != 0 || len(observer) != 1 || len(second) != 1 {
		t.Fatal("通知没有排除刚建立的连接")
	}
	if hub.leave(1, first) || !hub.changeHost(1, 1) || hub.onlineUsers(1)[2] != 0 {
		t.Fatal("主持交接驱逐时没有清掉前任的末个连接")
	}
	if hub.leave(1, second) || hub.changeHost(1, 1) {
		t.Fatal("重复驱逐或释放产生额外离线边缘")
	}
}

// 建立真实 WebSocket，用旁观者观察多标签页的两条上线和离线通知。
func TestLivePresenceWithPostgres(t *testing.T) {
	f := newLiveSessionTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	// 心跳夹具只造了连接所需字段，实际快照还读取建房时保存的题目信息。
	if _, err := f.db.Exec(ctx, `UPDATE rooms r SET soup_title=s.title,soup_surface=s.surface,soup_answer=s.answer,
		soup_genre=s.genre,soup_difficulty=s.difficulty,soup_horror=s.horror
		FROM soups s WHERE r.id=$1 AND r.soup_id=s.id`, f.room); err != nil {
		t.Fatal(err)
	}
	addMember := func(name, role string) *http.Cookie {
		t.Helper()
		var user int64
		if err := f.db.QueryRow(ctx, `INSERT INTO users(username,password_hash) VALUES($1,'test') RETURNING id`, name).Scan(&user); err != nil {
			t.Fatal(err)
		}
		if role != "" {
			if _, err := f.db.Exec(ctx, `INSERT INTO room_members(room_id,user_id,role) VALUES($1,$2,$3)`, f.room, user, role); err != nil {
				t.Fatal(err)
			}
		}
		return admissionCookie(t, f, user)
	}
	observer := addMember("presence_spectator", "spectator")
	player := addMember("presence_player", "player")
	outsider := addMember("presence_outsider", "")
	roomPath := fmt.Sprintf("/api/rooms/%d", f.room)
	socketPath := "ws" + strings.TrimPrefix(f.server.URL, "http") + roomPath + "/ws"
	dial := func(cookie *http.Cookie) *websocket.Conn {
		t.Helper()
		header := http.Header{"Origin": {f.server.URL}, "Cookie": {cookie.String()}}
		conn, _, err := websocket.Dial(ctx, socketPath, &websocket.DialOptions{HTTPHeader: header})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.CloseNow() })
		f.waitResumes(1)
		return conn
	}
	watch := dial(observer)
	notices := make(chan string, 16)
	go func() {
		defer close(notices)
		for {
			_, msg, err := watch.Read(ctx)
			if err != nil {
				return
			}
			select {
			case notices <- string(msg):
			case <-ctx.Done():
				return
			}
		}
	}()
	// 用同一连接池和 Hub 检查成员授权后的真实快照。
	handler := NewHandler(f.app, account.NewHandler(f.store, false))
	handler.liveHub = f.hub
	router := gin.New()
	router.GET("/api/rooms/:id", handler.get)
	read := func(cookie *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, roomPath+"?history=2&tasks=1", nil).WithContext(ctx)
		req.AddCookie(cookie)
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		return res
	}
	assertSnapshot := func(status string, playerOnline, hostOnline, observerOnline bool) {
		t.Helper()
		res := read(observer)
		var snapshot struct {
			Status  string `json:"status"`
			Members []struct {
				Username string `json:"username"`
				Online   *bool  `json:"online"`
			} `json:"members"`
		}
		if err := json.Unmarshal(res.Body.Bytes(), &snapshot); res.Code != http.StatusOK || err != nil || snapshot.Status != status {
			t.Fatalf("快照状态错误：HTTP %d，状态 %s，预期 %s，解析 %v", res.Code, snapshot.Status, status, err)
		}
		want := map[string]bool{"host": hostOnline, "presence_player": playerOnline, "presence_spectator": observerOnline}
		for _, member := range snapshot.Members {
			if expected, exists := want[member.Username]; !exists || member.Online == nil || *member.Online != expected {
				t.Fatalf("成员 %s 的在线字段错误", member.Username)
			}
			delete(want, member.Username)
		}
		if len(want) != 0 {
			t.Fatalf("快照漏了成员 %v", want)
		}
	}
	count := 0
	assertChanged := func() {
		t.Helper()
		select {
		case msg := <-notices:
			if msg != `{"type":"changed"}` {
				t.Fatalf("实时通知内容异常：%q", msg)
			}
			count++
		case <-time.After(3 * time.Second):
			t.Fatal("在线状态变化后没有收到通知")
		}
	}
	assertQuiet := func() {
		t.Helper()
		select {
		case msg := <-notices:
			t.Fatalf("在线状态未变化却收到通知：%q", msg)
		case <-time.After(75 * time.Millisecond):
		}
	}

	assertSnapshot("active", false, false, true)
	if res := read(outsider); res.Code != http.StatusNotFound || strings.Contains(res.Body.String(), `"online"`) {
		t.Fatalf("非成员收到了成员状态：HTTP %d", res.Code)
	}
	if res := admissionRequest(t, f, player); res.StatusCode != http.StatusUpgradeRequired {
		t.Fatalf("未升级请求返回 HTTP %d", res.StatusCode)
	}
	assertQuiet()
	start := time.Now()
	first := dial(player)
	assertChanged()
	up := time.Since(start)
	assertSnapshot("active", true, false, true)
	second, third := dial(player), dial(player)
	first.CloseNow()
	second.CloseNow()
	assertQuiet()
	assertSnapshot("active", true, false, true)
	start = time.Now()
	third.CloseNow()
	assertChanged()
	down := time.Since(start)
	assertSnapshot("active", false, false, true)
	if count != 2 {
		t.Fatalf("三个标签页开关共六次，旁观者应只收到两次状态通知，实际 %d", count)
	}
	t.Logf("三个标签页旁观者收到 %d 条通知，首次上线 %s，末个离线 %s", count, up.Round(time.Millisecond), down.Round(time.Millisecond))

	host := admissionCookie(t, f, f.host)
	hostConn := dial(host)
	assertChanged()
	hostConn.CloseNow()
	assertChanged()
	assertSnapshot("paused", false, false, true)
	dial(host)
	assertChanged()
	assertSnapshot("active", false, true, true)
	assertQuiet()
	if _, err := f.db.Exec(ctx, `UPDATE rooms SET status='finished' WHERE id=$1`, f.room); err != nil {
		t.Fatal(err)
	}
	assertSnapshot("finished", false, false, false)
}
