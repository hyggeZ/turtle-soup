package room

import (
	"fmt"
	"testing"
	"time"
)

func joinBackpressureViewer(t testing.TB, hub *liveHub, roomID, userID int64, kind liveKind) chan struct{} {
	t.Helper()
	updates, _, limit := hub.join(roomID, userID, kind)
	if limit != liveAllowed {
		t.Fatalf("连接准入失败：%v", limit)
	}
	if ok, _ := hub.activate(roomID, updates, nil); !ok {
		t.Fatal("开启实时连接失败")
	}
	return updates
}

func TestLiveHubBackpressure(t *testing.T) {
	const roomID = 1
	const notifications = 10_000
	hub := newLiveHub()
	slow := joinBackpressureViewer(t, hub, roomID, 1, livePlayer)
	fast := []chan struct{}{
		joinBackpressureViewer(t, hub, roomID, 2, livePlayer),
		joinBackpressureViewer(t, hub, roomID, 3, livePlayer),
	}
	if cap(slow) != 1 {
		t.Fatalf("实时通知容量为 %d，期望 1", cap(slow))
	}

	// 慢连接整轮停读，其他连接在每次广播返回后直接读取信号。
	done := make(chan error, 1)
	go func() {
		for i := 1; i <= notifications; i++ {
			hub.notify(roomID)
			if len(slow) != 1 {
				done <- fmt.Errorf("第 %d 次通知后慢连接待收信号数为 %d", i, len(slow))
				return
			}
			for j, updates := range fast {
				select {
				case _, ok := <-updates:
					if !ok {
						done <- fmt.Errorf("连接 %d 的通知通道提前关闭", j+1)
						return
					}
				default:
					done <- fmt.Errorf("第 %d 次广播返回后连接 %d 没有收到通知", i, j+1)
					return
				}
			}
		}
		done <- nil
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		// 超时只检测广播卡死，正常收取通过同步通知顺序来断言。
		t.Fatal("慢连接停读期间广播未能完成")
	}

	select {
	case _, ok := <-slow:
		if !ok || len(slow) != 0 {
			t.Fatal("恢复读取时应只取到一个待处理信号")
		}
	default:
		t.Fatal("恢复读取时缺少待处理信号")
	}
	hub.notify(roomID)
	for _, updates := range append(fast, slow) {
		if len(updates) != 1 {
			t.Fatal("慢连接恢复后应和其他连接一样收到新一轮信号")
		}
	}
	t.Logf("广播 %d 次，两条快连接各收 %d 次；慢连接保留 1 个信号，合并 %d 次", notifications, notifications, notifications-1)
}

func BenchmarkLiveHubNotifyBackpressure(b *testing.B) {
	const roomID = 1
	for _, count := range []int{16, maxLiveRoom} {
		for _, draining := range []int{count, count / 2} {
			b.Run(fmt.Sprintf("connections_%d/draining_%d", count, draining), func(b *testing.B) {
				hub := newLiveHub()
				viewers := make([]chan struct{}, 0, count)
				for i := 0; i < count; i++ {
					userID, kind := int64(i+1), livePlayer
					if i < maxRoomConnections {
						// 同一个主持人占用三个预留连接，允许覆盖每房容量上限。
						userID, kind = 1, liveHost
					}
					viewers = append(viewers, joinBackpressureViewer(b, hub, roomID, userID, kind))
				}
				fast, slow := viewers[:draining], viewers[draining:]
				hub.notify(roomID)
				for _, updates := range fast {
					<-updates
				}
				b.ReportAllocs()
				for b.Loop() {
					hub.notify(roomID)
					for _, updates := range fast {
						select {
						case <-updates:
						default:
							b.Fatal("广播返回后缺少通知")
						}
					}
				}
				for _, updates := range slow {
					if cap(updates) != 1 || len(updates) != 1 {
						b.Fatal("慢连接的通知积压应保持为 1")
					}
				}
				b.ReportMetric(float64(len(fast)), "delivered/op")
				b.ReportMetric(float64(len(slow)), "merged/op")
			})
		}
	}
}
