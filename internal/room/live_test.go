package room

import "testing"

func TestLiveHubConnectionLimits(t *testing.T) {
	hub := newLiveHub()
	connections := make([]chan struct{}, 0, maxRoomConnections)
	for i := 0; i < maxRoomConnections; i++ {
		updates, _, limit := hub.join(1, 7, livePlayer)
		if limit != liveAllowed {
			t.Fatalf("第 %d 个房间连接被提前拒绝", i+1)
		}
		connections = append(connections, updates)
	}
	if _, _, limit := hub.join(1, 7, livePlayer); limit != liveUserLimit {
		t.Fatal("同一用户在房间内突破了连接上限")
	}
	if hub.hasUser(1, 7) {
		t.Fatal("预留连接被错误计为在线")
	}
	for _, updates := range connections {
		hub.activate(1, updates, nil)
	}
	if !hub.hasUser(1, 7) {
		t.Fatal("用户已连接却未被识别为在线")
	}
	for i, updates := range connections {
		last := hub.leave(1, updates)
		if last != (i == len(connections)-1) {
			t.Fatalf("第 %d 个连接断开时，最后连接判定错误", i+1)
		}
	}
	if hub.hasUser(1, 7) {
		t.Fatal("所有连接断开后仍被识别为在线")
	}
}

func TestLiveHubGlobalUserLimit(t *testing.T) {
	hub := newLiveHub()
	connections := make([]chan struct{}, 0, maxUserConnections)
	for i := 0; i < maxUserConnections; i++ {
		room := int64(i/maxRoomConnections + 1)
		updates, _, limit := hub.join(room, 7, livePlayer)
		if limit != liveAllowed {
			t.Fatalf("用户第 %d 个总连接被提前拒绝", i+1)
		}
		connections = append(connections, updates)
	}
	if _, _, limit := hub.join(99, 7, livePlayer); limit != liveUserLimit {
		t.Fatal("同一用户跨房间突破了总连接上限")
	}
	if _, _, limit := hub.join(99, 8, livePlayer); limit != liveAllowed {
		t.Fatal("其他用户的连接被错误阻止")
	}
	hub.leave(1, connections[0])
	if _, _, limit := hub.join(99, 7, livePlayer); limit != liveAllowed {
		t.Fatal("断开后未释放连接名额")
	}
}
