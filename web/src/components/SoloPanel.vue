<script setup>
// 单人模式只显示自己的对局；模型判定和汤底权限由 Go 服务决定。
import { computed, onUnmounted, ref, watch } from 'vue'
import { request, send } from '../api'
import SoloHistory from './SoloHistory.vue'
import SoupPicker from './SoupPicker.vue'

const props = defineProps({ user: { type: Object, default: null }, initialSoupId: { type: Number, default: 0 } })
const emit = defineEmits(['login'])
const soupId = ref(props.initialSoupId)
const game = ref(null)
const games = ref([])
const question = ref('')
const restore = ref('')
const error = ref('')
const latest = ref(null)
const retry = ref(null)
const busy = ref(false)
const loading = ref(false)
const gamesLoading = ref(false)
const gamesError = ref('')
const showGames = ref(false)
const activeCount = computed(() => games.value.filter(item => item.status === 'active').length)
let loadVersion = 0
let listVersion = 0
let actionVersion = 0

function gameKey(userId) { return `turtle_solo_game_${userId}` }
function actionKey(userId, gameId) { return `turtle_solo_action_${userId}_${gameId}` }
function oldActionKey(userId) { return `turtle_solo_action_${userId}` }

// 每局分别保存待确认操作，切换对局时不会覆盖另一局的操作 ID。
function readRetry(userId, gameId) {
  const key = actionKey(userId, gameId)
  const legacy = oldActionKey(userId)
  const raw = sessionStorage.getItem(key) || sessionStorage.getItem(legacy)
  if (!raw) return null
  try {
    const entry = JSON.parse(raw)
    if (entry?.gameId !== gameId || !entry.operationId || !entry.type) return null
    sessionStorage.setItem(key, JSON.stringify(entry))
    sessionStorage.removeItem(legacy)
    return entry
  } catch {
    sessionStorage.removeItem(key)
    sessionStorage.removeItem(legacy)
    return null
  }
}

function clearRetry(gameId, userId = props.user?.id) {
  if (retry.value?.gameId === gameId) retry.value = null
  if (userId) sessionStorage.removeItem(actionKey(userId, gameId))
}

async function loadGames() {
  const userId = props.user?.id
  if (!userId) return
  const version = ++listVersion
  gamesLoading.value = true
  gamesError.value = ''
  try {
    const next = await request('/api/ai/solo/mine')
    if (version === listVersion && props.user?.id === userId) games.value = next
  } catch (cause) {
    if (version === listVersion) gamesError.value = cause.message || '读取对局列表失败'
  } finally {
    if (version === listVersion) gamesLoading.value = false
  }
}

function completed(entry, events) {
  return events.some(event => event.id > entry.afterEventId && event.kind === entry.type && event.body === entry.body)
}

async function loadGame(id) {
  const userId = props.user?.id
  if (!userId) return
  const version = ++loadVersion
  loading.value = true
  try {
    const next = await request(`/api/ai/solo/${id}`)
    if (version !== loadVersion || props.user?.id !== userId) return
    game.value = next
    localStorage.setItem(gameKey(userId), String(id))
    if (retry.value?.gameId === id &&
      (next.status === 'finished' || completed(retry.value, next.events))) clearRetry(id, userId)
    error.value = ''
  } catch (cause) {
    if (version === loadVersion && props.user?.id === userId) {
      error.value = cause.message || '读取对局失败，请重试'
      if ([401, 403, 404].includes(cause.status)) {
        game.value = null
        if (localStorage.getItem(gameKey(userId)) === String(id)) localStorage.removeItem(gameKey(userId))
        clearRetry(id, userId)
      }
    }
  } finally {
    if (version === loadVersion) loading.value = false
  }
}

async function start() {
  if (!props.user) return emit('login')
  if (!soupId.value || busy.value || loading.value || gamesLoading.value || activeCount.value >= 5) return
  const userId = props.user.id
  const version = ++actionVersion
  busy.value = true
  error.value = ''
  try {
    const created = await send('/api/ai/solo', 'POST', { soupId: soupId.value })
    if (version !== actionVersion || props.user?.id !== userId) return
    showGames.value = false
    localStorage.setItem(gameKey(userId), String(created.id))
    retry.value = readRetry(userId, created.id)
    await loadGame(created.id)
    if (version !== actionVersion || props.user?.id !== userId) return
    await loadGames()
  } catch (cause) {
    if (version === actionVersion && props.user?.id === userId) {
      error.value = cause.message === 'AI 主持尚未配置'
        ? 'AI 主持尚未配置，暂时无法开始单人试玩。'
        : cause.message || '创建对局失败，请稍后重试'
    }
  } finally {
    if (version === actionVersion) busy.value = false
  }
}

function saveRetry(entry) {
  retry.value = entry
  sessionStorage.setItem(actionKey(entry.userId, entry.gameId), JSON.stringify(entry))
}

async function execute(entry) {
  if (busy.value || loading.value || !props.user || !game.value || game.value.id !== entry.gameId) return
  const version = ++actionVersion
  const userId = props.user?.id
  busy.value = true
  error.value = ''
  const controller = new AbortController()
  const timeout = setTimeout(() => controller.abort(), 30000)
  try {
    const result = await request(`/api/ai/solo/${entry.gameId}/actions`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ type: entry.type, body: entry.body, operationId: entry.operationId }),
      signal: controller.signal,
    })
    if (version !== actionVersion || props.user?.id !== userId) return
    latest.value = result
    if (entry.type === 'question') question.value = ''
    if (entry.type === 'restore') restore.value = ''
    clearRetry(entry.gameId, userId)
    await loadGame(entry.gameId)
    if (version !== actionVersion || props.user?.id !== userId) return
    await loadGames()
  } catch (cause) {
    if (version !== actionVersion || props.user?.id !== userId) return
    const rejected = cause.status >= 400 && cause.status < 500 &&
      cause.status !== 408 && cause.message !== '上一条操作仍在处理'
    // 明确拒绝的操作没有生效，解除锁定；处理中的操作和网络超时沿用原 ID。
    if (rejected) clearRetry(entry.gameId, userId)
    const message = rejected
      ? cause.message || '操作未被接受，请检查后重试。'
      : cause.name === 'AbortError' || cause.status === 408
        ? '等待 AI 主持超时，可以重试上次操作。'
        : cause.status
          ? cause.message || '操作结果未确认，可以重试上次操作。'
          : '网络连接中断，可以重试上次操作。'
    await loadGame(entry.gameId)
    if (version !== actionVersion || props.user?.id !== userId) return
    await loadGames()
    if (version !== actionVersion || props.user?.id !== userId) return
    // 请求可能已写入成功，只是响应在途中丢失；以刷新后的事件为准。
    const confirmed = game.value?.id === entry.gameId && completed(entry, game.value.events)
    if (!confirmed) error.value = message
  } finally {
    clearTimeout(timeout)
    if (version === actionVersion) busy.value = false
  }
}

function act(type, body = '') {
  if (!props.user || !game.value || game.value.status !== 'active' || retry.value || loading.value) return
  const text = body.trim()
  if (type !== 'reveal' && !text) return
  const last = game.value.events.at(-1)
  const entry = {
    userId: props.user.id,
    gameId: game.value.id,
    type,
    body: text,
    operationId: crypto.randomUUID(),
    afterEventId: last?.id || 0,
  }
  latest.value = null
  saveRetry(entry)
  execute(entry)
}

function reveal() {
  if (window.confirm('查看汤底后，本局立即结束。确定查看吗？')) act('reveal')
}

function chooseAgain() {
  if (busy.value || loading.value) return
  actionVersion++
  loadVersion++
  loading.value = false
  game.value = null
  showGames.value = false
  soupId.value = 0
  latest.value = null
  error.value = ''
  retry.value = null
  if (props.user) localStorage.removeItem(gameKey(props.user.id))
}

async function openGame(id) {
  if (!props.user || busy.value || loading.value || game.value?.id === id) return
  actionVersion++
  // 切换对局后直接显示汤面和操作。
  showGames.value = false
  game.value = null
  latest.value = null
  question.value = ''
  restore.value = ''
  error.value = ''
  retry.value = readRetry(props.user.id, id)
  await loadGame(id)
}

async function restoreSavedGame() {
  if (!props.user || props.initialSoupId || game.value || loading.value) return
  const userId = props.user.id
  const id = Number(localStorage.getItem(gameKey(userId)))
  if (!Number.isSafeInteger(id) || id < 1) return
  retry.value = readRetry(userId, id)
  await loadGame(id)
}

watch(() => props.user?.id, userId => {
  actionVersion++
  loadVersion++
  listVersion++
  game.value = null
  games.value = []
  retry.value = null
  latest.value = null
  error.value = ''
  gamesError.value = ''
  showGames.value = false
  loading.value = false
  gamesLoading.value = false
  busy.value = false
  if (!userId) return
  loadGames()
  restoreSavedGame()
}, { immediate: true })
onUnmounted(() => { actionVersion++; loadVersion++; listVersion++ })
</script>

<template>
  <section class="panel solo-page">
    <div class="section-head">
      <h1>单人模式</h1>
      <div v-if="game" class="solo-head-actions">
        <button class="text-button" aria-controls="solo-games" :aria-expanded="showGames" @click="showGames = !showGames">
          {{ showGames ? '收起列表' : '查看其他对局' }}
        </button>
        <button class="text-button" :disabled="loading || busy" @click="loadGame(game.id)">刷新</button>
      </div>
    </div>
    <p v-if="error" role="alert">{{ error }}</p>
    <div v-if="!user" class="solo-entry">
      <button class="primary-button" @click="emit('login')">登录后试玩</button>
    </div>

    <div v-else-if="!game" class="solo-entry solo-start">
      <h2>开始新对局</h2>
      <SoupPicker v-model="soupId" />
      <p v-if="activeCount >= 5" class="muted">已有 5 局正在进行，请先完成一局。</p>
      <button class="primary-button" :disabled="busy || loading || gamesLoading || activeCount >= 5 || !soupId" @click="start">{{ busy ? '正在创建…' : loading ? '正在恢复对局…' : '开始推理' }}</button>
    </div>

    <section v-if="user" v-show="!game || showGames" id="solo-games" class="solo-entry solo-games">
      <div class="section-head">
        <h2>我的对局</h2>
        <button class="text-button" :disabled="gamesLoading" @click="loadGames">刷新</button>
      </div>
      <p v-if="gamesLoading" role="status" class="muted">正在加载对局…</p>
      <p v-if="gamesError" role="alert">{{ gamesError }}</p>
      <p v-if="!gamesLoading && !gamesError && !games.length" class="muted">暂无对局。</p>
      <article v-for="item in games" :key="item.id" class="room-row">
        <div>
          <strong>{{ item.title }}</strong>
          <p class="meta">{{ item.status === 'active' ? '推理中' : '已结束' }} · {{ new Date(item.createdAt).toLocaleDateString('zh-CN') }}</p>
        </div>
        <button class="small-button" :disabled="busy || loading || game?.id === item.id" @click="openGame(item.id)">
          {{ game?.id === item.id ? '当前对局' : item.status === 'active' ? '继续' : '回看' }}
        </button>
      </article>
    </section>

    <template v-if="game">
      <div class="room-summary">
        <h2>{{ game.title }}</h2>
        <p class="meta">{{ game.status === 'finished' ? '已结束' : '推理中' }}</p>
        <p class="surface">{{ game.surface }}</p>
      </div>

      <template v-if="game.status === 'active'">
        <div v-if="retry" class="solo-retry">
          <p>上次操作的结果还未确认。重试会沿用同一操作 ID。</p>
          <button class="small-button" :disabled="busy" @click="execute(retry)">{{ busy ? '正在等待 AI 主持…' : '重试上次操作' }}</button>
        </div>
        <div v-if="busy" class="solo-working" role="status">AI 主持正在思考，请稍候…</div>
        <form class="room-action-form" @submit.prevent="act('question', question)">
          <label>向 AI 主持提问
            <input v-model="question" maxlength="500" :disabled="busy || !!retry" placeholder="输入一个可以回答是、否或无关的问题" />
          </label>
          <button :disabled="busy || !!retry || !question.trim()">发送问题</button>
        </form>
        <form class="room-action-form" @submit.prevent="act('restore', restore)">
          <label>还原真相
            <textarea v-model="restore" rows="3" maxlength="2000" :disabled="busy || !!retry" placeholder="写下你推测的完整真相" />
          </label>
          <button :disabled="busy || !!retry || !restore.trim()">提交还原</button>
        </form>
        <button class="small-button" :disabled="busy || !!retry" @click="reveal">查看汤底并结束</button>
        <button class="small-button" :disabled="busy || loading" @click="chooseAgain">选择其他题目</button>
      </template>

      <div v-if="latest?.model" class="solo-model meta">
        本次判定：{{ latest.result }} · {{ latest.model }} · {{ Number(latest.latencyMs || 0).toFixed(0) }} ms
      </div>
      <div v-if="game.status === 'finished'" class="solo-finished">
        <h3>对局结束</h3>
        <div v-if="game.answer" class="answer"><h3>汤底</h3><p>{{ game.answer }}</p></div>
        <button class="small-button" @click="chooseAgain">换一道题</button>
      </div>
      <SoloHistory :events="game.events" />
    </template>
  </section>
</template>
