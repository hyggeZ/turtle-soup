<script setup>
// 房间操作走 HTTP；WebSocket 只提示重新读取按身份过滤的房间快照。
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { request } from '../../api'
import { needsRefresh, offlinePollDelay, reconnectDelay, snapshotRetryDelay } from './retryTiming'
import RoomEvents from './RoomEvents.vue'
import RoomMembers from './RoomMembers.vue'
import RoomSettings from './RoomSettings.vue'
import ConfirmDialog from '../shared/ConfirmDialog.vue'

const props = defineProps({ id: Number, user: { type: Object, default: null } })
const emit = defineEmits(['back', 'hosting'])
const room = ref(null)
const error = ref('')
const notice = ref('')
const question = ref('')
const restore = ref('')
const busy = ref(false)
const pendingAction = ref(null)
const connected = ref(false)
const everConnected = ref(false)
const editing = ref(false)
const confirmOpen = ref(false)
const confirmAction = ref('')
const now = ref(Date.now())
const clockOffset = ref(0)

let socket
let reconnectTimer
let refreshTimer
let clockTimer
let stableTimer
let active = false
let reconnectFailures = 0
let loadFailures = 0
let loadVersion = 0
let eventCursor = 0
let lastLoadedAt = 0
let expiredChecked = ''

const names = computed(() => Object.fromEntries((room.value?.members || []).map(item => [item.id, item.username])))
const playerCount = computed(() => (room.value?.members || []).filter(item => item.role === 'player').length)
const locked = computed(() => busy.value || !!pendingAction.value)
const me = computed(() => room.value?.members.find(item => item.id === props.user?.id))
const remaining = computed(() => {
  if (!room.value?.turnDeadline) return 0
  return Math.max(0, Math.ceil((new Date(room.value.turnDeadline).getTime() - now.value - clockOffset.value) / 1000))
})
const myTurn = computed(() => room.value?.mode === 'casual' || (room.value?.turnUserId === props.user?.id && remaining.value > 0))
const replied = computed(() => new Set((room.value?.events || []).filter(item => item.parentId).map(item => item.parentId)))
const pendingRestore = computed(() => room.value?.events.some(item => item.kind === 'restore' && item.userId === props.user?.id && !replied.value.has(item.id)))
const pendingAnswer = computed(() => room.value?.events.some(item => item.kind === 'answer_request' && item.userId === props.user?.id && !replied.value.has(item.id)))

// 新回答可能让旧问题首次公开，补发事件需要按 ID 插回原位置。
function mergeEvents(previous, incoming) {
  const byId = new Map(previous.map(event => [event.id, event]))
  for (const event of incoming) byId.set(event.id, event)
  return [...byId.values()].sort((a, b) => a.id - b.id)
}

async function load() {
  const id = props.id
  const version = ++loadVersion
  const after = room.value?.id === id ? eventCursor : 0
  const sentAt = Date.now()
  const controller = new AbortController()
  const timeout = setTimeout(() => controller.abort(), 15000)
  try {
    const path = `/api/rooms/${id}${after ? `?after=${after}` : ''}`
    const next = await request(path, { signal: controller.signal })
    if (!active || props.id !== id || version !== loadVersion) return
    if (!Array.isArray(next.events) || !Number.isSafeInteger(next.eventCursor) || next.eventCursor < after) {
      throw new Error('房间事件数据无效，请重试')
    }
    if (after && next.myRole !== room.value.myRole) {
      // 身份变化会改变可见历史，重新读取全量记录以清除旧权限内容。
      room.value = null
      eventCursor = 0
      load()
      return
    }
    const serverTime = Date.parse(next.serverTime)
    if (Number.isFinite(serverTime)) clockOffset.value = serverTime - (sentAt + Date.now()) / 2
    if (next.turnDeadline !== room.value?.turnDeadline) expiredChecked = ''
    const events = after ? mergeEvents(room.value.events, next.events) : next.events
    room.value = { ...next, events }
    eventCursor = next.eventCursor
    lastLoadedAt = performance.now()
    loadFailures = 0
    error.value = ''
    if (next.status === 'closed') closeSocket()
    else if (!socket && !reconnectTimer) connect()
    scheduleRefresh()
  } catch (cause) {
    if (!active || props.id !== id || version !== loadVersion) return
    lastLoadedAt = 0
    loadFailures += 1
    error.value = cause.name === 'AbortError' ? '房间更新超时，请稍后重试' : cause.message
    if ([401, 403, 404].includes(cause.status)) {
      room.value = null
      eventCursor = 0
      closeSocket()
    }
    scheduleRefresh()
  } finally {
    clearTimeout(timeout)
  }
}

function actionKey(roomId, userId) {
  return `turtle_room_action_${userId}_${roomId}`
}

function saveAction(action) {
  pendingAction.value = action
  sessionStorage.setItem(actionKey(action.roomId, action.userId), JSON.stringify(action))
}

function clearAction(action) {
  sessionStorage.removeItem(actionKey(action.roomId, action.userId))
  if (pendingAction.value?.operationId === action.operationId) pendingAction.value = null
}

function restoreAction() {
  pendingAction.value = null
  if (!props.user) return
  const key = actionKey(props.id, props.user.id)
  try {
    const saved = JSON.parse(sessionStorage.getItem(key) || 'null')
    if (saved?.roomId === props.id && saved.userId === props.user.id && saved.operationId && saved.type) {
      pendingAction.value = saved
    }
  } catch {
    sessionStorage.removeItem(key)
  }
}

async function runAction(action) {
  if (busy.value) return false
  busy.value = true
  const controller = new AbortController()
  const timeout = setTimeout(() => controller.abort(), 15000)
  try {
    const result = await request(`/api/rooms/${action.roomId}/actions`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ type: action.type, operationId: action.operationId, ...action.values }),
      signal: controller.signal,
    })
    clearAction(action)
    if (active && props.id === action.roomId) {
      notice.value = result.message || '操作已完成'
      error.value = ''
      if (action.type === 'question' && question.value.trim() === action.values.body) question.value = ''
      if (action.type === 'restore' && restore.value.trim() === action.values.body) restore.value = ''
      await load()
    }
    return true
  } catch (cause) {
    // 已收到明确的业务拒绝时可重新编辑；网络超时则沿用原操作 ID。
    if (cause.status >= 400 && cause.status < 500 && cause.status !== 408 && cause.status !== 429) clearAction(action)
    if (active && props.id === action.roomId) {
      await load()
      error.value = cause.name === 'AbortError' ? '请求超时，请重试上次操作。' : cause.message || '操作失败'
      notice.value = ''
    }
    return false
  } finally {
    clearTimeout(timeout)
    busy.value = false
  }
}

function act(type, values = {}) {
  if (busy.value || pendingAction.value || !props.user) return Promise.resolve(false)
  const action = { roomId: props.id, userId: props.user.id, type, values, operationId: crypto.randomUUID() }
  saveAction(action)
  return runAction(action)
}

function retryAction() {
  if (pendingAction.value) runAction(pendingAction.value)
}

async function ask() {
  if (!question.value.trim()) return
  await act('question', { body: question.value.trim() })
}

async function submitRestore() {
  if (!restore.value.trim()) return
  await act('restore', { body: restore.value.trim() })
}

function retire() {
  confirmAction.value = 'retire'
  confirmOpen.value = true
}

function closeRoom() {
  confirmAction.value = 'close'
  confirmOpen.value = true
}

function confirmChoice() {
  if (confirmAction.value) act(confirmAction.value)
  confirmAction.value = ''
}

async function copyCode() {
  try {
    await navigator.clipboard.writeText(room.value.code)
    notice.value = '房间码已复制'
  } catch {
    error.value = '复制失败，请手动选中房间码'
  }
}

function closeSocket() {
  const old = socket
  socket = null
  connected.value = false
  clearTimeout(reconnectTimer)
  reconnectTimer = null
  clearTimeout(stableTimer)
  clearTimeout(refreshTimer)
  reconnectFailures = 0
  old?.close()
}

function scheduleRefresh() {
  clearTimeout(refreshTimer)
  if (!active || room.value?.status === 'closed') return
  const wait = !connected.value ? offlinePollDelay() : loadFailures ? snapshotRetryDelay(loadFailures) : 60000
  refreshTimer = setTimeout(async () => {
    refreshTimer = null
    if (!active || room.value?.status === 'closed') return
    if (needsRefresh(connected.value, loadFailures, lastLoadedAt, performance.now())) await load()
    else scheduleRefresh()
  }, wait)
}

function scheduleReconnect() {
  reconnectFailures += 1
  const wait = reconnectDelay(reconnectFailures)
  clearTimeout(reconnectTimer)
  reconnectTimer = setTimeout(() => {
    reconnectTimer = null
    connect()
  }, wait)
  scheduleRefresh()
}

function connect() {
  if (!active || !props.id || socket || reconnectTimer || room.value?.status === 'closed') return
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  let connection
  try {
    connection = new WebSocket(`${protocol}//${window.location.host}/api/rooms/${props.id}/ws`)
  } catch {
    scheduleReconnect()
    return
  }
  socket = connection
  connection.onopen = () => {
    if (!active || socket !== connection) return
    connected.value = true
    everConnected.value = true
    // 短暂建立后又断开仍算连续失败；稳定 30 秒才重置退避。
    stableTimer = setTimeout(() => { reconnectFailures = 0 }, 30000)
    scheduleRefresh()
    load()
  }
  connection.onmessage = () => {
    // 快照服务连续失败时，通知只需等下一次退避补查即可。
    if (loadFailures === 0) load()
  }
  connection.onclose = () => {
    if (!active || socket !== connection) return
    socket = null
    connected.value = false
    clearTimeout(stableTimer)
    scheduleReconnect()
  }
}

function resetRoom() {
  closeSocket()
  room.value = null
  eventCursor = 0
  lastLoadedAt = 0
  loadFailures = 0
  clockOffset.value = 0
  everConnected.value = false
  error.value = ''
  notice.value = ''
  editing.value = false
  confirmOpen.value = false
  expiredChecked = ''
  restoreAction()
  load()
}

onMounted(() => {
  active = true
  resetRoom()
  clockTimer = setInterval(() => {
    now.value = Date.now()
    if (room.value?.status !== 'active' || room.value?.mode !== 'competitive' || !room.value.turnDeadline) return
    if (remaining.value > 0 || expiredChecked === room.value.turnDeadline) return
    expiredChecked = room.value.turnDeadline
    load()
  }, 1000)
})
onUnmounted(() => {
  active = false
  emit('hosting', false)
  closeSocket()
  clearInterval(clockTimer)
})
watch(() => [props.id, props.user?.id], resetRoom)
watch(() => ['active', 'paused'].includes(room.value?.status) && room.value?.myRole === 'host', value => emit('hosting', !!value), { immediate: true })
</script>

<template>
  <section class="play-scene room-page" :class="room ? `room-${room.status}` : ''">
    <div class="play-scene-content">
      <header class="play-page-heading">
        <button class="play-back" type="button" @click="emit('back')"><span aria-hidden="true">←</span>返回大厅</button>
        <div class="play-page-title"><span class="play-title-loop" aria-hidden="true"></span><h1>{{ room?.mode === 'competitive' ? '竞答房间' : '共探房间' }}</h1></div>
        <span v-if="room && room.status !== 'closed'" class="play-connection" :class="{ offline: !connected }">{{ connected ? '● 实时连接正常' : '● 正在重连' }}</span>
      </header>

      <p v-if="error" class="play-alert" role="alert">{{ error }}</p>
      <p v-if="notice" class="play-notice" role="status">{{ notice }}</p>
      <div v-if="pendingAction && !busy" class="play-retry">
        <span>上次操作结果尚未确认，可以沿用原操作编号重试。</span>
        <button class="play-ticket play-ticket-small play-ticket-cyan" @click="retryAction">重试上次操作</button>
      </div>
      <p v-if="room && room.status !== 'closed' && !connected && everConnected" class="play-connection-note">实时连接暂时中断，页面仍会定期更新。</p>

      <template v-if="room">
        <div class="play-room-ribbon">
          <span><small>房间码</small><strong>{{ room.code }}</strong><button class="play-link" @click="copyCode">复制</button></span>
          <span><small>玩法</small><strong>{{ room.mode === 'casual' ? '共探模式' : '竞答模式' }}</strong></span>
          <span><small>状态</small><strong>{{ room.status === 'waiting' ? '等待开始' : room.status === 'active' ? '进行中' : room.status === 'paused' ? '已暂停' : room.status === 'closed' ? '已关闭' : '已结束' }}</strong></span>
        </div>

        <div class="play-room-grid">
          <div class="play-room-left">
            <article class="play-paper play-case-card">
              <div class="play-case-head"><span class="play-case-tab">◇ 汤面</span><span class="play-case-ref">CASE FILE</span></div>
              <div class="play-case-content">
                <figure class="play-case-image"><img src="/assets/room-city.png" alt="雨夜街景插画" /><figcaption>故事场景</figcaption></figure>
                <div class="play-case-copy">
                  <h2>{{ room.title }}</h2>
                  <div class="play-soup-tags"><span>{{ room.genre }}</span><span>难度 {{ room.difficulty }}/5</span><span>恐怖 {{ room.horror }}/5</span></div>
                  <p class="surface">{{ room.surface }}</p>
                </div>
              </div>
              <div class="play-case-foot">推理之前，请先阅读汤面</div>
            </article>
            <RoomMembers v-if="room.status !== 'waiting'" :room="room" :player-count="playerCount" />
            <div v-if="room.answer" class="play-paper play-answer-card"><span class="play-eyebrow">TRUTH / 汤底</span><h3>故事的真相</h3><p class="surface">{{ room.answer }}</p></div>
          </div>

          <div class="play-room-right">
            <RoomMembers v-if="room.status === 'waiting'" :room="room" :player-count="playerCount" />
            <section v-if="room.status === 'waiting'" class="play-paper play-waiting-card">
              <div class="play-card-heading"><div><span class="play-eyebrow">READY TO PLAY</span><h3>等待开局</h3></div><span v-if="room.members.some(item => item.hostRequest)" class="play-count">主持申请 {{ room.members.filter(item => item.hostRequest).length }}</span></div>
              <p v-if="!room.hostId">还没有主持人，房主确认申请后即可开局。</p>
              <p v-else-if="!playerCount">至少一名玩家加入后可以开局。</p>
              <p v-else>主持人和玩家已就位，可以开始。</p>
              <div class="play-host-requests">
                <div v-for="member in room.members.filter(item => item.hostRequest && item.id !== room.ownerId)" :key="member.id" class="play-request-row">
                  <span class="play-avatar">{{ member.username.slice(0, 1) }}</span><span><b>{{ member.username }} 申请主持</b><small>房主确认后即可主持本局</small></span>
                  <button v-if="room.ownerId === user?.id" class="play-ticket play-ticket-small play-ticket-cyan" :disabled="locked" @click="act('host_approve', { userId: member.id })">同意</button>
                </div>
              </div>
              <div class="play-waiting-actions">
                <button v-if="room.myRole !== 'host' && room.ownerId !== user?.id && !me?.hostRequest" class="play-ticket play-ticket-small play-ticket-cyan" :disabled="locked" @click="act('host_request')">申请主持</button>
                <span v-if="me?.hostRequest" class="play-help">已申请主持，等待房主确认</span>
                <template v-if="room.ownerId === user?.id">
                  <button class="play-ticket play-ticket-small play-ticket-plain" :disabled="locked" @click="editing = !editing">{{ editing ? '收起设置' : '修改设置' }}</button>
                  <button class="play-ticket play-ticket-small play-ticket-plain" :disabled="locked" @click="closeRoom">关闭房间</button>
                </template>
                <button v-if="(room.ownerId === user?.id || room.myRole === 'host') && room.hostId && playerCount" class="play-ticket play-ticket-coral" :disabled="locked" @click="act('start')">开始游戏 →</button>
              </div>
              <RoomSettings v-if="editing && room.ownerId === user?.id" :room="room" @saved="editing = false; load()" @close="editing = false" />
            </section>
            <section v-if="room.status === 'paused'" class="play-paper play-state-card"><span class="play-eyebrow">PAUSED</span><h3>对局已暂停</h3><p>主持人重连后继续，提问和判定暂时不可用。</p></section>
            <section v-if="room.status === 'closed'" class="play-paper play-state-card"><span class="play-eyebrow">CLOSED</span><h3>房间已关闭</h3><p>房间记录仍可查看。</p></section>
            <section v-if="room.status === 'finished'" class="play-paper play-state-card">
              <span class="play-eyebrow">FINISHED</span><h3>对局结束</h3>
              <p v-if="room.mode === 'competitive'">{{ room.winnerId ? `${names[room.winnerId] || '玩家'} 获胜` : '本局没有获胜者' }}</p>
              <p v-else>本局已有 {{ room.members.filter(item => item.correct).length }} 人还原正确。</p>
              <button v-if="room.myRole === 'host' && !room.answerPublished" class="play-ticket play-ticket-small play-ticket-cyan" :disabled="locked" @click="act('publish_answer')">公开汤底</button>
            </section>
            <div v-if="room.status === 'active' && room.mode === 'competitive'" class="play-turn-banner">
              <strong>{{ remaining > 0 ? remaining : '—' }}<small>秒</small></strong>
              <span>当前提问：{{ names[room.turnUserId] || '无' }}</span>
              <small>{{ myTurn ? '轮到你提问' : '等待提问回合' }}</small>
            </div>
            <RoomEvents v-if="room.status !== 'waiting'" :room="room" :busy="locked" @action="act" />
          </div>
        </div>

        <template v-if="room.status === 'active'">
          <div v-if="room.myRole === 'player'" class="play-action-grid">
            <form class="play-paper play-action-card" @submit.prevent="ask">
              <div class="play-action-heading"><span class="play-action-icon">?</span><div><span class="play-eyebrow">QUESTION</span><h3>提出问题</h3></div></div>
              <label class="play-input-paper"><span>写下你想向主持人确认的事</span><textarea v-model="question" rows="2" maxlength="500" :disabled="locked || !myTurn" placeholder="例如：死者是被他人杀害的吗？" /></label>
              <div class="play-action-foot"><span>{{ room.mode === 'competitive' && !myTurn ? '等待你的提问回合' : '主持人只会回答 是 / 否 / 无关' }}</span><button class="play-ticket play-ticket-small play-ticket-cyan" :disabled="locked || !myTurn || !question.trim()">提问 →</button></div>
            </form>
            <form class="play-paper play-action-card" @submit.prevent="submitRestore">
              <div class="play-action-heading"><span class="play-action-icon play-action-icon-cyan">⌕</span><div><span class="play-eyebrow">RESTORE</span><h3>还原真相</h3></div></div>
              <label class="play-input-paper"><span>说出你推断的完整经过</span><textarea v-model="restore" rows="2" maxlength="2000" :disabled="locked || pendingRestore" placeholder="我认为事情的真相是……" /></label>
              <div class="play-action-foot"><span>{{ pendingRestore ? '等待主持人判定' : room.mode === 'casual' ? '共探模式下，你的还原内容不会公开' : '答对即结束本局' }}</span><button class="play-ticket play-ticket-small play-ticket-coral" :disabled="locked || pendingRestore || !restore.trim()">提交还原 →</button></div>
            </form>
          </div>
          <div v-if="room.myRole === 'player'" class="play-room-extra-actions">
            <button class="play-ticket play-ticket-small play-ticket-plain" :disabled="locked || pendingAnswer" @click="act('answer_request')">{{ pendingAnswer ? '等待查看申请结果' : '申请看答案' }}</button>
            <button class="play-link" :disabled="locked" @click="retire">退出玩家席位</button>
          </div>
          <p v-if="room.myRole === 'spectator'" class="play-viewer-note">你正在旁观，可以查看公开的问答与判定。</p>
        </template>
      </template>
      <p v-else-if="!error" class="play-loading" role="status">正在读取房间…</p>
    </div>

    <ConfirmDialog v-model:open="confirmOpen"
      :title="confirmAction === 'close' ? '关闭房间' : '退出玩家席位'"
      :message="confirmAction === 'close' ? '关闭后不能继续加入或开局，房间记录会保留。' : '退出后，本局只能旁观。'"
      :confirm-text="confirmAction === 'close' ? '关闭房间' : '确认退出'"
      :danger="confirmAction === 'close'"
      @confirm="confirmChoice" @cancel="confirmAction = ''" />
  </section>
</template>
