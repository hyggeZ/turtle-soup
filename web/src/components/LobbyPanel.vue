<script setup>
// 大厅展示公开房间和我已加入的房间；房间码可以进入未公开的房间。
import { onUnmounted, reactive, ref, watch } from 'vue'
import { request, send } from '../api'
import SoupPicker from './SoupPicker.vue'

const props = defineProps({ user: { type: Object, default: null }, initialSoupId: { type: Number, default: 0 }, initialCode: { type: String, default: '' } })
const emit = defineEmits(['room', 'login'])
const rooms = ref([])
const mine = ref([])
const history = ref([])
const code = ref(props.initialCode)
const error = ref('')
const historyError = ref('')
const loading = ref(false)
const loadingHistory = ref(false)
const loadingMore = ref(false)
const loadingMoreHistory = ref(false)
const hasMoreRooms = ref(false)
const hasMoreHistory = ref(false)
const moreError = ref('')
const busy = ref(false)
const showCreate = ref(!!props.initialSoupId)
const tab = ref('public')
const form = reactive({ soupId: props.initialSoupId, mode: 'casual', maxPlayers: 6, public: true, hostSelf: true })
const selectedSoup = ref(null)
let loadVersion = 0
let historyVersion = 0
let soupVersion = 0

async function load() {
  const version = ++loadVersion
  const userId = props.user?.id
  loading.value = true
  loadingMore.value = false
  hasMoreRooms.value = false
  moreError.value = ''
  rooms.value = []
  mine.value = []
  try {
    const publicRooms = await request('/api/rooms')
    const ownRooms = userId ? await request('/api/rooms/mine') : []
    if (version !== loadVersion || props.user?.id !== userId) return
    rooms.value = publicRooms
    hasMoreRooms.value = publicRooms.length === 50
    // 旧接口仍返回已关闭房间；页面统一把它放到独立的历史区。
    mine.value = ownRooms.filter(room => room.status !== 'closed')
    error.value = ''
  } catch (cause) {
    if (version === loadVersion) error.value = cause.message
  } finally {
    if (version === loadVersion) loading.value = false
  }
}

async function loadHistory() {
  const version = ++historyVersion
  const userId = props.user?.id
  history.value = []
  hasMoreHistory.value = false
  historyError.value = ''
  loadingMoreHistory.value = false
  loadingHistory.value = !!userId
  if (!userId) return
  try {
    const next = await request('/api/rooms/mine/history')
    if (version !== historyVersion || props.user?.id !== userId) return
    history.value = next
    hasMoreHistory.value = next.length === 50
  } catch (cause) {
    if (version === historyVersion) historyError.value = cause.message
  } finally {
    if (version === historyVersion) loadingHistory.value = false
  }
}

async function loadMoreHistory() {
  if (loadingHistory.value || loadingMoreHistory.value || !hasMoreHistory.value || !history.value.length) return
  const version = historyVersion
  const last = history.value.at(-1)
  loadingMoreHistory.value = true
  historyError.value = ''
  try {
    const next = await request(`/api/rooms/mine/history?before=${last.id}`)
    if (version !== historyVersion) return
    const seen = new Set(history.value.map(room => room.id))
    history.value = [...history.value, ...next.filter(room => !seen.has(room.id))]
    hasMoreHistory.value = next.length === 50
  } catch (cause) {
    if (version === historyVersion) historyError.value = cause.message
  } finally {
    if (version === historyVersion) loadingMoreHistory.value = false
  }
}

function refresh() {
  load()
  loadHistory()
}

function roomInfo(room, source) {
  const mode = room.mode === 'casual' ? '共探' : '竞答'
  const status = { waiting: '等待开始', active: '进行中', paused: '已暂停', finished: '已结束', closed: '已关闭' }[room.status] || room.status
  if (source === 'public') return [mode, status, room.genre, `难度 ${room.difficulty}/5`, `恐怖 ${room.horror}/5`].join(' · ')
  const role = { host: '主持', player: '玩家', spectator: '旁观' }[room.myRole] || '成员'
  return [mode, status, role, room.public ? '公开' : '私密'].join(' · ')
}

// 公开房间按末条 ID 加载更早的页面，刷新会重新从第一页开始。
async function loadMore() {
  if (loading.value || loadingMore.value || !hasMoreRooms.value || !rooms.value.length) return
  const version = loadVersion
  const before = rooms.value.at(-1).id
  loadingMore.value = true
  moreError.value = ''
  try {
    const next = await request(`/api/rooms?before=${before}`)
    if (version !== loadVersion) return
    rooms.value = [...rooms.value, ...next]
    hasMoreRooms.value = next.length === 50
  } catch (cause) {
    if (version === loadVersion) moreError.value = cause.message
  } finally {
    if (version === loadVersion) loadingMore.value = false
  }
}

async function join(roomCode) {
  if (!props.user) return emit('login', roomCode)
  if (busy.value) return
  busy.value = true
  try {
    const room = await send('/api/rooms/join', 'POST', { code: roomCode.trim().toUpperCase() })
    emit('room', room.id)
  } catch (cause) {
    error.value = cause.message
  } finally {
    busy.value = false
  }
}

async function create() {
  if (!props.user) return emit('login')
  if (busy.value || !form.soupId) return
  busy.value = true
  try {
    const room = await send('/api/rooms', 'POST', { ...form })
    emit('room', room.id)
  } catch (cause) {
    error.value = cause.message
  } finally {
    busy.value = false
  }
}

watch(() => props.user?.id, refresh, { immediate: true })
watch(() => props.initialSoupId, id => {
  if (!id) return
  form.soupId = id
  showCreate.value = true
})
// 预览只读取公开汤面，玩家不会在建房时看到汤底。
watch(() => form.soupId, async id => {
  const version = ++soupVersion
  selectedSoup.value = null
  if (!id) return
  try {
    const soup = await request(`/api/soups/${id}`)
    if (version === soupVersion) selectedSoup.value = soup
  } catch { /* 选题组件会显示具体错误。 */ }
}, { immediate: true })
onUnmounted(() => { loadVersion++; historyVersion++; soupVersion++ })
</script>

<template>
  <section class="lobby-page">
    <p v-if="error" role="alert">{{ error }}</p>
    <template v-if="showCreate">
      <button class="text-button lobby-back" type="button" @click="showCreate = false">← 返回大厅</button>
      <div class="lobby-create-layout">
        <section id="lobby-create-panel" class="lobby-create-section">
          <div class="lobby-create-head">
            <h2>房间设置</h2><span>选题与规则</span>
          </div>
          <form class="lobby-create-form" @submit.prevent="create">
            <SoupPicker v-model="form.soupId" />
            <div class="form-row lobby-rule-row">
              <label>玩法
                <select v-model="form.mode">
                  <option value="casual">共探</option>
                  <option value="competitive">竞答</option>
                </select>
              </label>
              <label>玩家人数上限
                <input v-model.number="form.maxPlayers" type="number" min="2" max="20" required />
              </label>
            </div>
            <div class="lobby-checks">
              <label class="check-line"><input v-model="form.hostSelf" type="checkbox" />我来主持</label>
              <label class="check-line"><input v-model="form.public" type="checkbox" />在大厅公开</label>
            </div>
            <p v-if="!form.hostSelf" class="meta">你将以玩家身份入场，等待其他人申请主持。</p>
            <div class="lobby-create-actions">
              <button class="primary-button" :disabled="busy || !form.soupId">{{ busy ? '创建中…' : '确认创建' }}</button>
              <button type="button" class="text-button" @click="showCreate = false">取消</button>
            </div>
          </form>
        </section>
        <aside class="lobby-soup-preview" aria-label="选中题目预览">
          <span class="play-kicker">SELECTED STORY / 题目预览</span>
          <template v-if="selectedSoup">
            <h3>{{ selectedSoup.title }}</h3>
            <p>{{ selectedSoup.surface }}</p>
            <div class="lobby-preview-tags">
              <span>{{ selectedSoup.genre }}</span>
              <span>难度 {{ selectedSoup.difficulty }}/5</span>
              <span>恐怖 {{ selectedSoup.horror }}/5</span>
            </div>
          </template>
          <p v-else class="lobby-preview-empty">选择题目后，这里会显示汤面。</p>
          <small>汤底仅对本局主持人可见</small>
        </aside>
      </div>
    </template>

    <template v-else>
      <div class="lobby-intro">
        <div>
          <span class="play-kicker">ROOM LOBBY</span>
          <h2>选择房间</h2>
          <p>从公开房间加入，或用房间码与朋友会合。</p>
        </div>
        <form class="lobby-join" @submit.prevent="join(code)">
          <label>房间码加入<input v-model="code" maxlength="32" placeholder="输入房间码" required /></label>
          <button class="small-button" :disabled="busy || !code.trim()">加入房间</button>
        </form>
        <button class="primary-button lobby-create-toggle" type="button" aria-controls="lobby-create-panel" :aria-expanded="showCreate" @click="showCreate = true">＋ 创建房间</button>
      </div>
      <div class="lobby-tabs" role="group" aria-label="房间列表">
        <button id="lobby-tab-public" :class="{ active: tab === 'public' }" :aria-pressed="tab === 'public'" @click="tab = 'public'">公开房间</button>
        <button v-if="user" id="lobby-tab-mine" :class="{ active: tab === 'mine' }" :aria-pressed="tab === 'mine'" @click="tab = 'mine'">我的房间</button>
        <button v-if="user" id="lobby-tab-history" :class="{ active: tab === 'history' }" :aria-pressed="tab === 'history'" @click="tab = 'history'">房间记录</button>
        <button class="text-button lobby-refresh" :disabled="loading || loadingHistory" @click="refresh">刷新 ↻</button>
      </div>
      <div class="lobby-room-layout">
        <section v-if="tab === 'public'" id="lobby-panel-public" class="lobby-list-panel" aria-labelledby="lobby-tab-public">
          <p v-if="loading" role="status" class="muted">正在加载房间…</p>
          <p v-else-if="!rooms.length && !error" class="empty-state">暂无公开房间。你可以创建一间。</p>
          <article v-for="(room, index) in rooms" :key="room.id" class="room-row">
            <span class="room-index">{{ String(index + 1).padStart(2, '0') }}</span>
            <div><strong>{{ room.title }}</strong><p class="meta">{{ roomInfo(room, 'public') }}</p></div>
            <span class="room-count"><b>{{ room.players }}/{{ room.maxPlayers }}</b><small>玩家</small></span>
            <button class="small-button" :disabled="busy" @click="join(room.code)">{{ room.status === 'active' && room.mode === 'competitive' ? '旁观' : '进入' }} →</button>
          </article>
          <p v-if="moreError" role="alert">{{ moreError }}</p>
          <button v-if="hasMoreRooms" class="text-button lobby-more" :disabled="loadingMore" @click="loadMore">{{ loadingMore ? '正在加载…' : moreError ? '重试加载更多' : '加载更多房间 ↓' }}</button>
        </section>
        <section v-if="user && tab === 'mine'" id="lobby-panel-mine" class="lobby-list-panel" aria-labelledby="lobby-tab-mine">
          <p v-if="loading" role="status" class="muted">正在加载房间…</p>
          <p v-else-if="!mine.length && !error" class="empty-state">暂无进行中的房间。</p>
          <article v-for="(room, index) in mine" :key="room.id" class="room-row">
            <span class="room-index">{{ String(index + 1).padStart(2, '0') }}</span>
            <div><strong>{{ room.title }}</strong><p class="meta">{{ roomInfo(room, 'mine') }}</p></div>
            <span class="room-count"><b>{{ room.players }}/{{ room.maxPlayers }}</b><small>玩家</small></span>
            <button class="small-button" @click="emit('room', room.id)">进入 →</button>
          </article>
        </section>
        <section v-if="user && tab === 'history'" id="lobby-panel-history" class="lobby-list-panel" aria-labelledby="lobby-tab-history">
          <p v-if="loadingHistory" role="status" class="muted">正在加载房间记录…</p>
          <p v-else-if="!history.length && !historyError" class="empty-state">暂无房间记录。</p>
          <article v-for="(room, index) in history" :key="room.id" class="room-row">
            <span class="room-index">{{ String(index + 1).padStart(2, '0') }}</span>
            <div><strong>{{ room.title }}</strong><p class="meta">{{ roomInfo(room, 'history') }}</p></div>
            <span class="room-count"><b>{{ room.players }}/{{ room.maxPlayers }}</b><small>玩家</small></span>
            <button class="small-button" @click="emit('room', room.id)">回看 →</button>
          </article>
          <p v-if="historyError" role="alert">{{ historyError }}</p>
          <button v-if="!history.length && historyError" class="small-button" @click="loadHistory">重试</button>
          <button v-if="hasMoreHistory" class="text-button lobby-more" :disabled="loadingMoreHistory" @click="loadMoreHistory">{{ loadingMoreHistory ? '正在加载…' : historyError ? '重试加载更多' : '加载更多记录 ↓' }}</button>
        </section>
        <aside class="lobby-guide">
          <span class="play-kicker">房间模式</span>
          <h3>共探<br />或竞答</h3>
          <p>共探房间可以在进行中加入。竞答房间开局后只能旁观。</p>
          <span class="lobby-guide-line"></span>
        </aside>
      </div>
    </template>
  </section>
</template>
