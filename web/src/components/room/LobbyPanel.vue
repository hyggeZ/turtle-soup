<script setup>
// 大厅分别展示招募中的公开房间、公开回看和我已加入的房间。
import { computed, onUnmounted, reactive, ref, watch } from 'vue'
import { request, send } from '../../api'
import SoupPicker from '../shared/SoupPicker.vue'

const props = defineProps({
  user: { type: Object, default: null },
  initialSoupId: { type: Number, default: 0 },
  initialCode: { type: String, default: '' },
  initialDraft: { type: Object, default: null },
})
const emit = defineEmits(['room', 'login', 'back'])
const rooms = ref([])
const replays = ref([])
const mine = ref([])
const history = ref([])
const code = ref(props.initialCode)
const error = ref('')
const replayError = ref('')
const historyError = ref('')
const loading = ref(false)
const loadingReplays = ref(false)
const loadingHistory = ref(false)
const loadingMore = ref(false)
const loadingMoreReplays = ref(false)
const loadingMoreHistory = ref(false)
const hasMoreRooms = ref(false)
const hasMoreReplays = ref(false)
const hasMoreHistory = ref(false)
const moreError = ref('')
const replayMoreError = ref('')
const replayLoaded = ref(false)
const busy = ref(false)
const showCreate = ref(!!props.initialSoupId || !!props.initialDraft)
const tab = ref('public')
const listedRooms = computed(() => tab.value === 'replay' ? replays.value : tab.value === 'mine' ? mine.value : tab.value === 'history' ? history.value : rooms.value)
const listLoading = computed(() => tab.value === 'replay' ? loadingReplays.value : tab.value === 'history' ? loadingHistory.value : loading.value)
const listError = computed(() => tab.value === 'replay' ? replayError.value : tab.value === 'history' ? historyError.value : error.value)
const draft = props.initialDraft || {}
const form = reactive({
  soupId: draft.soupId || props.initialSoupId,
  mode: draft.mode || 'casual',
  maxPlayers: draft.maxPlayers ?? 6,
  public: draft.public ?? true,
  hostSelf: draft.hostSelf ?? true,
})
const selectedSoup = ref(null)
let loadVersion = 0
let replayVersion = 0
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

async function loadReplays() {
  const version = ++replayVersion
  loadingReplays.value = true
  loadingMoreReplays.value = false
  hasMoreReplays.value = false
  replayMoreError.value = ''
  replayError.value = ''
  replayLoaded.value = false
  replays.value = []
  try {
    const next = await request('/api/rooms/replays')
    if (version !== replayVersion) return
    replays.value = next
    hasMoreReplays.value = next.length === 50
    replayLoaded.value = true
  } catch (cause) {
    if (version === replayVersion) replayError.value = cause.message
  } finally {
    if (version === replayVersion) loadingReplays.value = false
  }
}

async function loadMoreReplays() {
  if (loadingReplays.value || loadingMoreReplays.value || !hasMoreReplays.value || !replays.value.length) return
  const version = replayVersion
  const before = replays.value.at(-1).id
  loadingMoreReplays.value = true
  replayMoreError.value = ''
  try {
    const next = await request(`/api/rooms/replays?before=${before}`)
    if (version !== replayVersion) return
    const seen = new Set(replays.value.map(room => room.id))
    replays.value = [...replays.value, ...next.filter(room => !seen.has(room.id))]
    hasMoreReplays.value = next.length === 50
  } catch (cause) {
    if (version === replayVersion) replayMoreError.value = cause.message
  } finally {
    if (version === replayVersion) loadingMoreReplays.value = false
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
  replayLoaded.value = false
  if (tab.value === 'replay') loadReplays()
}

function roomInfo(room, source) {
  const status = { waiting: '等待开始', active: '进行中', paused: '已暂停', finished: '已结束', closed: '已关闭' }[room.status] || room.status
  if (source === 'public' || source === 'replay') return [status, `难度 ${room.difficulty}/5`, `恐怖 ${room.horror}/5`].join(' · ')
  const role = { host: '主持', player: '玩家', spectator: '旁观' }[room.myRole] || '成员'
  return [status, role, room.public ? '公开' : '私密'].join(' · ')
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
  if (!props.user) return emit('login', '', { ...form })
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
watch(tab, value => {
  if (value === 'replay' && !replayLoaded.value && !loadingReplays.value) loadReplays()
})
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
onUnmounted(() => { loadVersion++; replayVersion++; historyVersion++; soupVersion++ })
</script>

<template>
  <section class="play-scene lobby-page">
    <div class="play-scene-content">
      <header class="play-page-heading">
        <button class="play-back" type="button" @click="showCreate ? showCreate = false : emit('back')">
          <span aria-hidden="true">←</span>{{ showCreate ? '返回大厅' : '返回首页' }}
        </button>
        <div class="play-page-title"><span class="play-title-loop" aria-hidden="true"></span><h1>{{ showCreate ? '创建房间' : '房间大厅' }}</h1></div>
      </header>

      <p v-if="error" class="play-alert" role="alert">{{ error }}</p>

      <template v-if="showCreate">
        <div class="play-create-grid">
          <section class="play-paper play-create-picker">
            <div class="play-card-heading"><div><span class="play-eyebrow">CHOOSE A CASE</span><h2>选择汤面</h2></div></div>
            <SoupPicker v-model="form.soupId" />
          </section>
          <form id="lobby-create-panel" class="play-paper play-create-rules" @submit.prevent="create">
            <div class="play-card-heading"><div><span class="play-eyebrow">ROOM RULES</span><h2>房间规则</h2></div></div>
            <div v-if="selectedSoup" class="play-selected-soup">
              <strong>{{ selectedSoup.title }}</strong>
              <p>{{ selectedSoup.surface }}</p>
              <div class="play-soup-tags"><span>{{ selectedSoup.genre }}</span><span>难度 {{ selectedSoup.difficulty }}/5</span><span>恐怖 {{ selectedSoup.horror }}/5</span></div>
            </div>
            <p v-else class="play-picker-placeholder">请先从左侧选择题目。</p>
            <fieldset class="play-mode-field">
              <legend>玩法</legend>
              <div class="play-mode-options">
                <button type="button" :class="{ selected: form.mode === 'casual' }" :aria-pressed="form.mode === 'casual'" @click="form.mode = 'casual'"><strong>共探</strong><small>自由提问，可中途加入</small></button>
                <button type="button" :class="{ selected: form.mode === 'competitive' }" :aria-pressed="form.mode === 'competitive'" @click="form.mode = 'competitive'"><strong>竞答</strong><small>轮流提问，答对即胜</small></button>
              </div>
            </fieldset>
            <label class="play-field">玩家人数上限
              <input v-model.number="form.maxPlayers" type="number" min="2" max="20" required />
            </label>
            <div class="play-checks">
              <label><input v-model="form.hostSelf" type="checkbox" />我来主持</label>
              <label><input v-model="form.public" type="checkbox" />在大厅公开</label>
            </div>
            <p v-if="!form.hostSelf" class="play-help">你将以玩家身份入场，等待其他人申请主持。</p>
            <button class="play-ticket play-ticket-coral" :disabled="busy || !form.soupId">{{ busy ? '创建中…' : '确认创建 →' }}</button>
          </form>
        </div>
      </template>

      <template v-else>
        <div class="play-lobby-grid">
          <section class="play-lobby-board" :class="{ 'play-lobby-board-empty': !listedRooms.length && !listLoading }">
            <div class="play-card-heading"><div><span class="play-eyebrow">{{ tab === 'replay' ? 'ROOM ARCHIVE' : 'OPEN ROOMS' }}</span><h2>{{ tab === 'public' ? '正在招募的房间' : tab === 'replay' ? '公开回看' : tab === 'mine' ? '我的房间' : '房间记录' }}</h2></div><span class="play-count">{{ listedRooms.length }} 间</span></div>
            <div class="play-lobby-tabs" role="group" aria-label="房间列表">
              <button :class="{ selected: tab === 'public' }" :aria-pressed="tab === 'public'" @click="tab = 'public'">公开房间</button>
              <button :class="{ selected: tab === 'replay' }" :aria-pressed="tab === 'replay'" @click="tab = 'replay'">公开回看</button>
              <button v-if="user" :class="{ selected: tab === 'mine' }" :aria-pressed="tab === 'mine'" @click="tab = 'mine'">我的房间</button>
              <button v-if="user" :class="{ selected: tab === 'history' }" :aria-pressed="tab === 'history'" @click="tab = 'history'">房间记录</button>
              <button class="play-link play-list-refresh" :disabled="loading || loadingReplays || loadingHistory" @click="refresh">刷新 ↻</button>
            </div>
            <div class="play-room-list">
              <p v-if="listLoading" role="status" class="play-empty">正在加载房间…</p>
              <p v-else-if="!listedRooms.length && !listError" class="play-empty">{{ tab === 'public' ? '暂无公开房间。' : tab === 'replay' ? '暂无可回看的公开房间。' : tab === 'mine' ? '暂无进行中的房间。' : '暂无房间记录。' }}</p>
              <article v-for="(item, index) in listedRooms" :key="item.id" class="play-room-row">
                <span class="play-row-number">{{ String(index + 1).padStart(2, '0') }}</span>
                <div class="play-room-row-body">
                  <h3>{{ item.title }}</h3>
                  <div class="play-row-meta"><span class="play-soup-tags"><span>{{ item.mode === 'casual' ? '共探' : '竞答' }}</span><span>{{ item.genre || (tab === 'replay' || item.public ? '公开' : '私密') }}</span></span><small>{{ roomInfo(item, tab) }}<template v-if="tab === 'replay'"> · {{ item.answerPublished ? '汤底已公开' : '汤底尚未公开' }}</template><template v-else> · {{ item.players }}/{{ item.maxPlayers }} 玩家</template></small></div>
                </div>
                <button class="play-ticket play-ticket-small" :class="index % 2 ? 'play-ticket-cyan' : 'play-ticket-coral'" :disabled="busy" @click="tab === 'public' || tab === 'replay' ? join(item.code) : emit('room', item.id)">{{ tab === 'history' || tab === 'replay' ? '回看' : item.status === 'active' && item.mode === 'competitive' ? '旁观' : '进入房间' }} →</button>
              </article>
            </div>
            <p v-if="tab === 'replay' && replayError" role="alert" class="play-alert">{{ replayError }}</p>
            <p v-if="tab === 'replay' && replayMoreError" role="alert" class="play-alert">{{ replayMoreError }}</p>
            <p v-if="tab === 'history' && historyError" role="alert" class="play-alert">{{ historyError }}</p>
            <p v-if="tab === 'public' && moreError" role="alert" class="play-alert">{{ moreError }}</p>
            <div class="play-list-foot">
              <button v-if="tab === 'public' && hasMoreRooms" class="play-link" :disabled="loadingMore" @click="loadMore">{{ loadingMore ? '加载中…' : '加载更多房间 ↓' }}</button>
              <button v-if="tab === 'replay' && hasMoreReplays" class="play-link" :disabled="loadingMoreReplays" @click="loadMoreReplays">{{ loadingMoreReplays ? '加载中…' : '加载更多回看 ↓' }}</button>
              <button v-if="tab === 'replay' && replayError && !replays.length" class="play-link" @click="loadReplays">重试</button>
              <button v-if="tab === 'history' && hasMoreHistory" class="play-link" :disabled="loadingMoreHistory" @click="loadMoreHistory">{{ loadingMoreHistory ? '加载中…' : '加载更多记录 ↓' }}</button>
              <button v-if="tab === 'history' && historyError && !history.length" class="play-link" @click="loadHistory">重试</button>
              <span v-if="tab === 'public'">竞答开局后不能加入；共探进行中仍可进入。</span>
              <span v-if="tab === 'replay'">汤底公开后会剧透，回看成员也将失去同题玩家资格。</span>
            </div>
          </section>
          <aside class="play-lobby-tools">
            <form class="play-paper play-join-card" @submit.prevent="join(code)">
              <span class="play-eyebrow">PRIVATE ROOM</span><h2>输入房间码</h2>
              <label class="play-field">房间码<input v-model="code" maxlength="32" placeholder="输入房间码" required /></label>
              <button class="play-ticket play-ticket-cyan" :disabled="busy || !code.trim()">加入房间 →</button>
            </form>
            <section class="play-create-card">
              <span class="play-create-mark" aria-hidden="true">＋</span><span class="play-eyebrow">NEW ROOM</span><h2>创建自己的房间</h2>
              <p>选择题目，决定玩法与人数。</p>
              <button class="play-ticket play-ticket-coral" type="button" @click="showCreate = true">创建房间 →</button>
            </section>
          </aside>
        </div>
      </template>
    </div>
  </section>
</template>
