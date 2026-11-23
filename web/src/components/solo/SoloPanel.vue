<script setup>
// 单人模式只显示自己的对局；模型判定和汤底权限由 Go 服务决定。
import { computed, onUnmounted, ref, watch } from 'vue'
import { request, send } from '../../api'
import roomImage from '../../assets/room-city.webp'
import SoloHistory from './SoloHistory.vue'
import SoupPicker from '../shared/SoupPicker.vue'
import ConfirmDialog from '../shared/ConfirmDialog.vue'

const props = defineProps({ user: { type: Object, default: null }, initialSoupId: { type: Number, default: 0 } })
const emit = defineEmits(['login', 'back'])
const soupId = ref(props.initialSoupId)
const selectedSoup = ref(null)
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
const confirmOpen = ref(false)
const activeCount = computed(() => games.value.filter(item => item.status === 'active').length)
let loadVersion = 0
let listVersion = 0
let actionVersion = 0
let soupVersion = 0

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
  if (!props.user) return emit('login', soupId.value)
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
  confirmOpen.value = true
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
  confirmOpen.value = false
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
watch(() => props.initialSoupId, id => {
  if (id > 0) soupId.value = id
})
watch(soupId, async id => {
  const version = ++soupVersion
  selectedSoup.value = null
  if (!id) return
  try {
    const soup = await request(`/api/soups/${id}`)
    if (version === soupVersion) selectedSoup.value = soup
  } catch { /* 选题列表会显示具体错误。 */ }
}, { immediate: true })
onUnmounted(() => { actionVersion++; loadVersion++; listVersion++; soupVersion++ })
</script>

<template>
  <section class="play-scene solo-page">
    <div class="play-scene-content">
      <header class="play-page-heading">
        <button class="play-back" type="button" @click="emit('back')"><span aria-hidden="true">←</span>返回首页</button>
        <div class="play-page-title"><span class="play-title-loop" aria-hidden="true"></span><h1>单人模式</h1></div>
        <button v-if="game" class="play-link play-head-action" :disabled="loading || busy" @click="loadGame(game.id)">刷新对局 ↻</button>
      </header>
      <p v-if="error" class="play-alert" role="alert">{{ error }}</p>
      <div class="play-solo-ribbon"><span class="play-ai-seal">AI</span><span><strong>AI 主持</strong><small>只回答 是 / 否 / 无关</small></span><b>{{ game ? game.status === 'finished' ? '对局已结束' : '推理进行中' : '选择汤面' }}</b></div>

      <div v-if="!user" class="play-paper play-solo-login">
        <div class="play-card-heading"><div><span class="play-eyebrow">SOLO GAME</span><h2>单人推理</h2></div></div>
        <p>登录后选择题目，由 AI 主持回答问题。</p>
        <button class="play-ticket play-ticket-cyan" @click="emit('login', soupId)">登录后试玩 →</button>
      </div>

      <template v-else-if="!game">
        <div class="play-solo-start-grid">
          <section class="play-paper play-solo-picker">
            <div class="play-card-heading"><div><span class="play-eyebrow">CHOOSE A CASE</span><h2>选择汤面</h2></div></div>
            <SoupPicker v-model="soupId" />
            <p v-if="activeCount >= 5" class="play-alert">已有 5 局正在进行，请先完成一局。</p>
          </section>
          <div class="play-solo-start-side">
            <article v-if="selectedSoup" class="play-paper play-case-card">
              <div class="play-case-head"><span class="play-case-tab">◇ 汤面</span><span class="play-case-ref">CASE FILE</span></div>
              <div class="play-case-content">
                <figure class="play-case-image">
                  <img :src="roomImage" alt="雨夜街景插画" width="1672" height="941" loading="lazy" decoding="async" />
                  <figcaption>故事场景</figcaption>
                </figure>
                <div class="play-case-copy"><h2>{{ selectedSoup.title }}</h2><div class="play-soup-tags"><span>{{ selectedSoup.genre }}</span><span>难度 {{ selectedSoup.difficulty }}/5</span><span>恐怖 {{ selectedSoup.horror }}/5</span></div><p class="surface">{{ selectedSoup.surface }}</p></div>
              </div>
              <div class="play-case-foot">推理之前，请先阅读汤面</div>
            </article>
            <div v-else class="play-paper play-no-selection">选择题目后，这里会显示汤面。</div>
            <div class="play-start-bar"><strong>{{ selectedSoup?.title || '尚未选择题目' }}</strong><button class="play-ticket play-ticket-cyan" :disabled="busy || loading || gamesLoading || activeCount >= 5 || !soupId" @click="start">{{ busy ? '正在创建…' : loading ? '正在恢复…' : '开始推理 →' }}</button></div>
          </div>
        </div>
      </template>

      <template v-else>
        <div class="play-solo-game-grid">
          <div class="play-solo-main">
            <article class="play-paper play-case-card">
              <div class="play-case-head"><span class="play-case-tab">◇ 汤面</span><span class="play-case-ref">CASE FILE</span></div>
              <div class="play-case-content">
                <figure class="play-case-image">
                  <img :src="roomImage" alt="雨夜街景插画" width="1672" height="941" loading="lazy" decoding="async" />
                  <figcaption>故事场景</figcaption>
                </figure>
                <div class="play-case-copy"><h2>{{ game.title }}</h2><p class="surface">{{ game.surface }}</p></div>
              </div>
              <div class="play-case-foot">推理之前，请先阅读汤面</div>
            </article>
            <div v-if="game.status === 'finished' && game.answer" class="play-paper play-answer-card"><span class="play-eyebrow">TRUTH / 汤底</span><h3>故事的真相</h3><p class="surface">{{ game.answer }}</p></div>
            <div v-if="game.status === 'finished'" class="play-paper play-solo-finished"><span class="play-finish-symbol">✓</span><h3>本局结束</h3><button class="play-ticket play-ticket-cyan" @click="chooseAgain">再来一局 →</button></div>
          </div>
          <div class="play-solo-side">
            <SoloHistory :events="game.events" />
            <section v-if="game.status === 'active'" class="play-paper play-solo-controls">
              <div class="play-action-heading"><span class="play-action-icon">?</span><div><span class="play-eyebrow">ASK AI HOST</span><h3>继续提问</h3></div></div>
              <div v-if="retry" class="play-retry"><span>上次操作结果未确认，可沿用原操作编号重试。</span><button class="play-link" :disabled="busy" @click="execute(retry)">重试</button></div>
              <p v-if="busy" class="play-working" role="status">AI 主持正在思考…</p>
              <form @submit.prevent="act('question', question)">
                <label class="play-input-paper"><span>输入一个可以回答是、否或无关的问题</span><textarea v-model="question" rows="2" maxlength="500" :disabled="busy || !!retry" placeholder="例如：门是死者自己锁上的吗？" /></label>
                <button class="play-ticket play-ticket-small play-ticket-cyan" :disabled="busy || !!retry || !question.trim()">提问 →</button>
              </form>
              <form @submit.prevent="act('restore', restore)">
                <label class="play-input-paper"><span>完整还原</span><textarea v-model="restore" rows="2" maxlength="2000" :disabled="busy || !!retry" placeholder="写下你推测的完整真相" /></label>
                <button class="play-ticket play-ticket-small play-ticket-coral" :disabled="busy || !!retry || !restore.trim()">提交还原 →</button>
              </form>
              <div class="play-solo-secondary"><button class="play-link" :disabled="busy || !!retry" @click="reveal">查看汤底并结束</button><button class="play-link" :disabled="busy || loading" @click="chooseAgain">选择其他题目</button></div>
              <p v-if="latest?.model" class="play-model">本次判定：{{ latest.result }} · {{ latest.model }} · {{ Number(latest.latencyMs || 0).toFixed(0) }} ms</p>
            </section>
            <button class="play-link play-game-list-toggle" :aria-expanded="showGames" @click="showGames = !showGames">{{ showGames ? '收起对局列表 ↑' : '查看其他对局 ↓' }}</button>
          </div>
        </div>
      </template>

      <section v-if="user && (!game || showGames)" class="play-paper play-my-games">
        <div class="play-card-heading"><div><span class="play-eyebrow">MY GAMES</span><h3>我的对局</h3></div><button class="play-link" :disabled="gamesLoading" @click="loadGames">刷新 ↻</button></div>
        <p v-if="gamesLoading" class="play-empty" role="status">正在加载对局…</p>
        <p v-if="gamesError" class="play-alert" role="alert">{{ gamesError }}</p>
        <p v-if="!gamesLoading && !gamesError && !games.length" class="play-empty">暂无对局。</p>
        <article v-for="item in games" :key="item.id" class="play-game-row">
          <span><strong>{{ item.title }}</strong><small>{{ item.status === 'active' ? '推理中' : '已结束' }} · {{ new Date(item.createdAt).toLocaleDateString('zh-CN') }}</small></span>
          <button class="play-ticket play-ticket-small play-ticket-plain" :disabled="busy || loading || game?.id === item.id" @click="openGame(item.id)">{{ game?.id === item.id ? '当前对局' : item.status === 'active' ? '继续' : '回看' }}</button>
        </article>
      </section>
    </div>

    <ConfirmDialog v-model:open="confirmOpen" title="查看汤底" message="查看汤底后，本局立即结束。" confirm-text="确认查看" @confirm="act('reveal')" />
  </section>
</template>
