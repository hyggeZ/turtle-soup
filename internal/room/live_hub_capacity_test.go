package room

import (
	"context"
	"sync"
	"testing"
)

func reserveLive(t *testing.T, hub *liveHub, room, user int64, kind liveKind) chan struct{} {
	t.Helper()
	updates, _, limit := hub.join(room, user, kind)
	if limit != liveAllowed {
		t.Fatalf("房间 %d 用户 %d 预留失败：%d", room, user, limit)
	}
	return updates
}

func TestLiveHubRoomAndHostReserve(t *testing.T) {
	hub := newLiveHub()
	const room int64 = 1
	for user := int64(1); user <= maxLiveRoomSpectators; user++ {
		reserveLive(t, hub, room, user, liveSpectator)
	}
	if _, _, limit := hub.join(room, 1000, liveSpectator); limit != liveRoomLimit {
		t.Fatalf("第 65 个旁观连接应按房间限制拒绝：%d", limit)
	}
	for user := int64(100); user < 100+maxLiveRoomNonHost-maxLiveRoomSpectators; user++ {
		reserveLive(t, hub, room, user, livePlayer)
	}
	if _, _, limit := hub.join(room, 2000, livePlayer); limit != liveRoomLimit {
		t.Fatalf("非主持占满后仍允许入场：%d", limit)
	}
	for i := 0; i < maxRoomConnections; i++ {
		reserveLive(t, hub, room, 3000, liveHost)
	}
	if hub.processTotal != maxLiveRoom {
		t.Fatalf("房间容量与三条主持保留名额不符：%d", hub.processTotal)
	}
	if _, _, limit := hub.join(room, 3001, liveHost); limit != liveRoomLimit {
		t.Fatalf("超过房间总容量仍允许主持入场：%d", limit)
	}
}

func TestLiveHubProcessAndSpectatorLimits(t *testing.T) {
	hub := newLiveHub()
	for i := 0; i < maxLiveProcessSpectators; i++ {
		room := int64(i/maxLiveRoomSpectators + 1)
		reserveLive(t, hub, room, int64(i+1), liveSpectator)
	}
	if _, _, limit := hub.join(100, 10000, liveSpectator); limit != liveProcessLimit {
		t.Fatalf("超过进程旁观上限仍可进入：%d", limit)
	}
	for i := 0; i < maxLiveProcessNonHost-maxLiveProcessSpectators; i++ {
		room := int64(20 + i/100)
		reserveLive(t, hub, room, int64(20000+i), livePlayer)
	}
	if _, _, limit := hub.join(200, 30000, livePlayer); limit != liveProcessLimit {
		t.Fatalf("超过进程非主持上限仍可进入：%d", limit)
	}
	for i := 0; i < maxLiveProcess-maxLiveProcessNonHost; i++ {
		reserveLive(t, hub, int64(300+i), int64(40000+i), liveHost)
	}
	if _, _, limit := hub.join(500, 50000, liveHost); limit != liveProcessLimit {
		t.Fatalf("超过进程总容量仍可进入：%d", limit)
	}
}

func TestLiveHubConcurrentAdmissionAndRelease(t *testing.T) {
	hub := newLiveHub()
	const requests = maxLiveRoomNonHost + 16
	type result struct {
		updates chan struct{}
		limit   liveLimit
	}
	results := make(chan result, requests)
	var workers sync.WaitGroup
	for i := 0; i < requests; i++ {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			updates, _, limit := hub.join(1, int64(i+1), livePlayer)
			results <- result{updates: updates, limit: limit}
		}(i)
	}
	workers.Wait()
	close(results)
	accepted := 0
	for item := range results {
		if item.limit == liveAllowed {
			accepted++
			hub.leave(1, item.updates)
		} else if item.limit != liveRoomLimit {
			t.Fatalf("并发拒绝原因错误：%d", item.limit)
		}
	}
	if accepted != maxLiveRoomNonHost || hub.processTotal != 0 {
		t.Fatalf("并发准入/释放计数错误：成功 %d，剩余 %d", accepted, hub.processTotal)
	}
}

func TestLiveHubPendingAndHostChange(t *testing.T) {
	hub := newLiveHub()
	old := make([]chan struct{}, 0, maxRoomConnections)
	evictions := make([]chan struct{}, 0, maxRoomConnections)
	for i := 0; i < maxRoomConnections; i++ {
		updates, evict, limit := hub.join(1, 900, liveHost)
		if limit != liveAllowed {
			t.Fatal("前任主持预留失败")
		}
		old = append(old, updates)
		evictions = append(evictions, evict)
	}
	if hub.hasUser(1, 900) {
		t.Fatal("尚未完成握手的主持被计为在线")
	}
	oldContexts := make([]context.Context, 0, len(old))
	for _, updates := range old {
		ctx, cancel := context.WithCancel(context.Background())
		hub.activate(1, updates, cancel)
		oldContexts = append(oldContexts, ctx)
	}
	if !hub.hasUser(1, 900) {
		t.Fatal("握手后的主持未被计为在线")
	}
	newHost := reserveLive(t, hub, 1, 901, livePlayer)
	hub.activate(1, newHost, nil)
	hub.changeHost(1, 901)
	if hub.hasUser(1, 900) || !hub.hasUser(1, 901) {
		t.Fatal("换主持后在线身份未更新")
	}
	for i, evict := range evictions {
		select {
		case <-evict:
		default:
			t.Fatalf("前任主持第 %d 条连接未被驱逐", i+1)
		}
		select {
		case <-oldContexts[i].Done():
		default:
			t.Fatalf("前任主持第 %d 条连接的 I/O 未取消", i+1)
		}
		if hub.leave(1, old[i]) {
			t.Fatal("被驱逐连接重复释放名额")
		}
	}
	if hub.processTotal != 1 || hub.processNonHosts != 0 {
		t.Fatalf("换主持后保留计数错误：总数 %d，非主持 %d", hub.processTotal, hub.processNonHosts)
	}
	for i := 0; i < maxRoomConnections-1; i++ {
		reserveLive(t, hub, 1, 901, liveHost)
	}
	if hub.processTotal != maxRoomConnections {
		t.Fatalf("新主持未拿到完整三条名额：%d", hub.processTotal)
	}
}

func TestEvictedPendingConnectionCannotActivate(t *testing.T) {
	hub := newLiveHub()
	updates, evict, limit := hub.join(7, 10, liveHost)
	if limit != liveAllowed {
		t.Fatal("主持预留失败")
	}
	hub.changeHost(7, 11)
	select {
	case <-evict:
	default:
		t.Fatal("旧主持待升级连接未被驱逐")
	}
	if hub.activate(7, updates, nil) || liveTotal(hub) != 0 || hub.hasUser(7, 10) {
		t.Fatal("已驱逐的待升级连接被错误激活或占用名额")
	}
}
