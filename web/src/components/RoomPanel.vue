<script setup>
// 房间操作走 HTTP；WebSocket 只提示重新读取按身份过滤的房间快照。
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { request } from '../api'
import RoomEvents from './RoomEvents.vue'
import RoomSettings from './RoomSettings.vue'

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
const now = ref(Date.now())
const clockOffset = ref(0)

let socket
let reconnectTimer
let refreshTimer
let clockTimer
let active = false
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
  try {
    const path = `/api/rooms/${id}${after ? `?after=${after}` : ''}`
    const next = await request(path)
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
    error.value = ''
    if (next.status === 'closed') closeSocket()
    else if (!socket) connect()
  } catch (cause) {
    if (!active || props.id !== id || version !== loadVersion) return
    lastLoadedAt = 0
    error.value = cause.message
    if ([401, 403, 404].includes(cause.status)) {
      room.value = null
      eventCursor = 0
      closeSocket()
    }
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
  if (window.confirm('退出玩家席位后，本局只能旁观。确定退出吗？')) act('retire')
}

function closeRoom() {
  if (window.confirm('关闭后不能继续加入或开始游戏，房间记录会保留。确定关闭吗？')) act('close')
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
  old?.close()
}

function connect() {
  if (!active || !props.id) return
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  const connection = new WebSocket(`${protocol}//${window.location.host}/api/rooms/${props.id}/ws`)
  socket = connection
  connection.onopen = () => { connected.value = true; everConnected.value = true; load() }
  connection.onmessage = load
  connection.onclose = () => {
    if (!active || socket !== connection) return
    connected.value = false
    reconnectTimer = setTimeout(connect, 2000)
  }
}

function resetRoom() {
  closeSocket()
  room.value = null
  eventCursor = 0
  lastLoadedAt = 0
  clockOffset.value = 0
  everConnected.value = false
  error.value = ''
  notice.value = ''
  editing.value = false
  expiredChecked = ''
  restoreAction()
  load()
}

onMounted(() => {
  active = true
  resetRoom()
  // 实时连接断开时每 10 秒补查；连接正常时每 60 秒校准一次。
  refreshTimer = setInterval(() => {
    if (room.value?.status === 'closed') return
    if (!connected.value || performance.now() - lastLoadedAt >= 60000) load()
  }, 10000)
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
  clearInterval(refreshTimer)
  clearInterval(clockTimer)
})
watch(() => [props.id, props.user?.id], resetRoom)
watch(() => room.value?.status === 'active' && room.value?.myRole === 'host', value => emit('hosting', !!value), { immediate: true })
</script>

<template>
  <section class="panel room-page">
    <div class="section-head">
      <h1>房间</h1>
      <button class="text-button" @click="emit('back')">返回大厅</button>
    </div>
    <p v-if="error" role="alert">{{ error }}</p>
    <p v-if="notice" class="room-notice" role="status">{{ notice }}</p>
    <div v-if="pendingAction && !busy" class="room-retry">
      <span>上次操作结果尚未确认，重试会沿用同一操作 ID。</span>
      <button class="small-button" @click="retryAction">重试上次操作</button>
    </div>
    <p v-if="room && room.status !== 'closed' && !connected" class="room-connection">{{ everConnected ? '实时连接中断，正在重连；页面仍会定期更新。' : '正在建立实时连接，页面仍会定期更新。' }}</p>

    <template v-if="room">
      <div class="room-summary">
        <h2>{{ room.title }}</h2>
        <div class="room-code">
          <span>房间码</span>
          <code>{{ room.code }}</code>
          <button class="text-button" @click="copyCode">复制房间码</button>
        </div>
        <p class="meta">
          {{ room.mode === 'casual' ? '共探' : '竞答' }} ·
          {{ room.status === 'waiting' ? '等待开始' : room.status === 'active' ? '进行中' : room.status === 'paused' ? '主持人离线，已暂停' : room.status === 'closed' ? '已关闭' : '已结束' }}
        </p>
        <p class="meta">{{ room.genre }} · 难度 {{ room.difficulty }}/5 · 恐怖 {{ room.horror }}/5 · 玩家 {{ playerCount }}/{{ room.maxPlayers }}</p>
        <p class="surface">{{ room.surface }}</p>
      </div>

      <section class="room-members">
        <h3>成员</h3>
        <div class="members">
          <span v-for="member in room.members" :key="member.id" class="member-tag">
            {{ member.username }} · {{ member.role === 'host' ? '主持' : member.role === 'player' ? '玩家' : '旁观' }}{{ member.correct ? ' · 已答对' : '' }}
          </span>
        </div>
      </section>

      <section v-if="room.status === 'waiting'" class="room-waiting">
        <h3>等待开局</h3>
        <p v-if="!room.hostId" class="meta">还没有主持人，房主确认申请后即可开局。</p>
        <p v-else-if="!playerCount" class="meta">至少一名玩家加入后可以开局。</p>
        <p v-else class="meta">主持人和玩家已就位，可以开始。</p>
        <div class="room-controls">
          <button v-if="room.myRole !== 'host' && room.ownerId !== user?.id && !me?.hostRequest" class="small-button" :disabled="locked" @click="act('host_request')">申请主持</button>
          <span v-if="me?.hostRequest" class="meta">已申请主持，等待房主确认</span>
          <template v-if="room.ownerId === user?.id">
            <button v-for="member in room.members.filter(item => item.hostRequest && item.id !== room.ownerId)" :key="member.id" class="small-button" :disabled="locked" @click="act('host_approve', { userId: member.id })">同意 {{ member.username }} 主持</button>
            <button class="small-button" :disabled="locked" @click="editing = !editing">{{ editing ? '收起设置' : '修改设置' }}</button>
            <button class="small-button" :disabled="locked" @click="closeRoom">关闭房间</button>
          </template>
          <button v-if="(room.ownerId === user?.id || room.myRole === 'host') && room.hostId && playerCount" class="primary-button" :disabled="locked" @click="act('start')">开始游戏</button>
        </div>
        <RoomSettings v-if="editing && room.ownerId === user?.id" :room="room" @saved="editing = false; load()" @close="editing = false" />
      </section>

      <section v-if="room.status === 'closed'" class="room-result">
        <h3>房间已关闭</h3>
        <p>房间在开局前已关闭，成员和房间记录仍可查看。</p>
      </section>

      <section v-if="room.status === 'paused'" class="room-paused">
        <h3>对局已暂停</h3>
        <p>主持人重连后继续，提问和判定暂时不可用。</p>
      </section>

      <section v-if="room.status === 'active'" class="room-play">
        <div v-if="room.mode === 'competitive'" class="room-turn">
          <strong>当前提问：{{ names[room.turnUserId] || '无' }}</strong>
          <span>{{ remaining > 0 ? `剩余 ${remaining} 秒` : '本回合已超时' }}</span>
        </div>
        <template v-if="room.myRole === 'player'">
          <p v-if="room.mode === 'competitive' && !myTurn" class="meta">等待你的提问回合。</p>
          <form class="room-action-form" @submit.prevent="ask">
            <label>向主持人提问<input v-model="question" maxlength="500" :disabled="locked || !myTurn" placeholder="输入一个可以回答是、否或无关的问题" /></label>
            <button :disabled="locked || !myTurn || !question.trim()">发送问题</button>
          </form>
          <form class="room-action-form" @submit.prevent="submitRestore">
            <label>还原真相<textarea v-model="restore" rows="3" maxlength="2000" :disabled="locked || pendingRestore" placeholder="把完整推理单独提交给主持人" /></label>
            <button :disabled="locked || pendingRestore || !restore.trim()">提交还原</button>
          </form>
          <p v-if="pendingRestore" class="meta">已提交还原，等待主持人判定。</p>
          <div class="room-controls">
            <button class="small-button" :disabled="locked || pendingAnswer" @click="act('answer_request')">{{ pendingAnswer ? '等待查看申请结果' : '申请查看汤底' }}</button>
            <button class="small-button" :disabled="locked" @click="retire">退出玩家席位</button>
          </div>
        </template>
        <p v-else-if="room.myRole === 'spectator'" class="meta">你正在旁观，可以查看公开的问答与判定。</p>
        <p v-else class="meta">请回答玩家问题，并判定还原与查看申请。</p>
      </section>

      <section v-if="room.status === 'finished'" class="room-result">
        <h3>对局结束</h3>
        <p v-if="room.mode === 'competitive'">{{ room.winnerId ? `${names[room.winnerId] || '玩家'} 获胜` : '本局没有获胜者' }}</p>
        <p v-else>本局已有 {{ room.members.filter(item => item.correct).length }} 人还原正确。</p>
        <button v-if="room.myRole === 'host' && !room.answerPublished" class="small-button" :disabled="locked" @click="act('publish_answer')">公开汤底</button>
      </section>

      <div v-if="room.answer" class="answer"><h3>汤底</h3><p>{{ room.answer }}</p></div>
      <RoomEvents :room="room" :busy="locked" @action="act" />
    </template>
  </section>
</template>
