package room

import (
	"context"
	"sync"
)

const maxRoomConnections = 3
const maxUserConnections = 12

// 单实例准入预算：每房预留三条、全进程预留 64 条主持连接；压测后再调整。
const maxLiveRoom = 128
const maxLiveProcess = 1024
const maxLiveRoomNonHost = 125
const maxLiveProcessNonHost = 960
const maxLiveRoomSpectators = 64
const maxLiveProcessSpectators = 768

type liveKind uint8

const (
	liveHost liveKind = iota
	livePlayer
	liveSpectator
)

type liveLimit uint8

const (
	liveAllowed liveLimit = iota
	liveUserLimit
	liveRoomLimit
	liveProcessLimit
)

type liveViewer struct {
	user   int64
	kind   liveKind
	active bool
	evict  chan struct{}
	cancel context.CancelFunc
}

// liveHub 只推送“房间有变化”的提示；私有内容仍由带权限检查的 HTTP 快照提供。
type liveHub struct {
	mu                sync.Mutex
	viewers           map[int64]map[chan struct{}]liveViewer
	users             map[int64]map[int64]int
	online            map[int64]map[int64]int
	total             map[int64]int
	roomNonHosts      map[int64]int
	roomSpectators    map[int64]int
	processTotal      int
	processNonHosts   int
	processSpectators int
}

func newLiveHub() *liveHub {
	return &liveHub{
		viewers:        make(map[int64]map[chan struct{}]liveViewer),
		users:          make(map[int64]map[int64]int),
		online:         make(map[int64]map[int64]int),
		total:          make(map[int64]int),
		roomNonHosts:   make(map[int64]int),
		roomSpectators: make(map[int64]int),
	}
}

// 所有容量检查和名额预占在同一把锁下完成，升级失败或断线后再释放。
func (h *liveHub) join(room, user int64, kind liveKind) (chan struct{}, chan struct{}, liveLimit) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.users[room][user] >= maxRoomConnections || h.total[user] >= maxUserConnections {
		return nil, nil, liveUserLimit
	}
	if h.processTotal >= maxLiveProcess {
		return nil, nil, liveProcessLimit
	}
	if len(h.viewers[room]) >= maxLiveRoom {
		return nil, nil, liveRoomLimit
	}
	if kind != liveHost && h.processNonHosts >= maxLiveProcessNonHost {
		return nil, nil, liveProcessLimit
	}
	if kind != liveHost && h.roomNonHosts[room] >= maxLiveRoomNonHost {
		return nil, nil, liveRoomLimit
	}
	if kind == liveSpectator && h.processSpectators >= maxLiveProcessSpectators {
		return nil, nil, liveProcessLimit
	}
	if kind == liveSpectator && h.roomSpectators[room] >= maxLiveRoomSpectators {
		return nil, nil, liveRoomLimit
	}
	if h.viewers[room] == nil {
		h.viewers[room] = make(map[chan struct{}]liveViewer)
		h.users[room] = make(map[int64]int)
	}
	updates := make(chan struct{}, 1)
	evict := make(chan struct{})
	h.viewers[room][updates] = liveViewer{user: user, kind: kind, evict: evict}
	h.users[room][user]++
	h.total[user]++
	h.processTotal++
	if kind != liveHost {
		h.roomNonHosts[room]++
		h.processNonHosts++
	}
	if kind == liveSpectator {
		h.roomSpectators[room]++
		h.processSpectators++
	}
	return updates, evict, liveAllowed
}

// 握手完成后才算在线；预留名额不能让主持人提前开局。
func (h *liveHub) activate(room int64, updates chan struct{}, cancel context.CancelFunc) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	viewer, exists := h.viewers[room][updates]
	if !exists {
		return false
	}
	if viewer.active {
		return true
	}
	viewer.active = true
	viewer.cancel = cancel
	h.viewers[room][updates] = viewer
	if h.online[room] == nil {
		h.online[room] = make(map[int64]int)
	}
	h.online[room][viewer.user]++
	return true
}

func (h *liveHub) leave(room int64, updates chan struct{}) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.leaveLocked(room, updates)
}

// 调用方必须持有 h.mu；重复释放不改变容量计数。
func (h *liveHub) leaveLocked(room int64, updates chan struct{}) bool {
	viewer, exists := h.viewers[room][updates]
	if !exists {
		return false
	}
	delete(h.viewers[room], updates)
	user := viewer.user
	h.users[room][user]--
	h.total[user]--
	h.processTotal--
	if viewer.kind != liveHost {
		h.roomNonHosts[room]--
		h.processNonHosts--
	}
	if viewer.kind == liveSpectator {
		h.roomSpectators[room]--
		h.processSpectators--
	}
	if h.total[user] == 0 {
		delete(h.total, user)
	}
	if h.users[room][user] == 0 {
		delete(h.users[room], user)
	}
	last := false
	if viewer.active {
		h.online[room][user]--
		last = h.online[room][user] == 0
		if last {
			delete(h.online[room], user)
		}
	}
	if len(h.viewers[room]) == 0 {
		delete(h.viewers, room)
		delete(h.users, room)
		delete(h.online, room)
		delete(h.roomNonHosts, room)
		delete(h.roomSpectators, room)
	}
	return last
}

// 换主持后释放前任连接占用的保留名额，现任已连接设备直接改计为主持。
func (h *liveHub) changeHost(room, newHost int64) {
	h.mu.Lock()
	var cancels []context.CancelFunc
	for updates, viewer := range h.viewers[room] {
		if viewer.kind == liveHost && viewer.user != newHost {
			h.leaveLocked(room, updates)
			close(viewer.evict)
			if viewer.cancel != nil {
				cancels = append(cancels, viewer.cancel)
			}
			continue
		}
		if viewer.user != newHost || viewer.kind == liveHost {
			continue
		}
		h.roomNonHosts[room]--
		h.processNonHosts--
		if viewer.kind == liveSpectator {
			h.roomSpectators[room]--
			h.processSpectators--
		}
		viewer.kind = liveHost
		h.viewers[room][updates] = viewer
	}
	h.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (h *liveHub) notify(room int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for updates := range h.viewers[room] {
		select {
		case updates <- struct{}{}:
		default:
		}
	}
}

func (h *liveHub) hasUser(room, user int64) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.online[room][user] > 0
}
