<script setup>
// 单人模式只显示自己的对局；模型判定和汤底权限由 Go 服务决定。
import { onMounted, ref, watch } from 'vue'
import { request, send } from '../api'
import SoloHistory from './SoloHistory.vue'
import SoupPicker from './SoupPicker.vue'

const props = defineProps({ user: { type: Object, default: null }, initialSoupId: { type: Number, default: 0 } })
const emit = defineEmits(['login'])
const soupId = ref(props.initialSoupId)
const game = ref(null)
const question = ref('')
const restore = ref('')
const error = ref('')
const latest = ref(null)
const retry = ref(null)
const busy = ref(false)
const loading = ref(false)
let loadVersion = 0

function gameKey() { return `turtle_solo_game_${props.user.id}` }
function actionKey() { return `turtle_solo_action_${props.user.id}` }

function clearRetry() {
  retry.value = null
  sessionStorage.removeItem(actionKey())
}

function completed(entry, events) {
  return events.some(event => event.id > entry.afterEventId && event.kind === entry.type && event.body === entry.body)
}

async function loadGame(id) {
  const version = ++loadVersion
  loading.value = true
  try {
    const next = await request(`/api/ai/solo/${id}`)
    if (version !== loadVersion) return
    game.value = next
    localStorage.setItem(gameKey(), String(id))
    if (retry.value && (next.status === 'finished' || completed(retry.value, next.events))) clearRetry()
    error.value = ''
  } catch (cause) {
    if (version === loadVersion) {
      error.value = cause.message || '读取对局失败，请重试'
      if ([401, 403, 404].includes(cause.status)) {
        game.value = null
        localStorage.removeItem(gameKey())
        clearRetry()
      }
    }
  } finally {
    if (version === loadVersion) loading.value = false
  }
}

async function start() {
  if (!props.user) return emit('login')
  if (!soupId.value || busy.value || loading.value) return
  busy.value = true
  error.value = ''
  try {
    const created = await send('/api/ai/solo', 'POST', { soupId: soupId.value })
    localStorage.setItem(gameKey(), String(created.id))
    await loadGame(created.id)
  } catch (cause) {
    error.value = cause.message === 'AI 主持尚未配置'
      ? 'AI 主持尚未配置，暂时无法开始单人试玩。'
      : cause.message || '创建对局失败，请稍后重试'
  } finally {
    busy.value = false
  }
}

function saveRetry(entry) {
  retry.value = entry
  sessionStorage.setItem(actionKey(), JSON.stringify(entry))
}

async function execute(entry) {
  if (busy.value || !game.value) return
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
    latest.value = result
    if (entry.type === 'question') question.value = ''
    if (entry.type === 'restore') restore.value = ''
    clearRetry()
    await loadGame(entry.gameId)
  } catch (cause) {
    const rejected = cause.status >= 400 && cause.status < 500 &&
      cause.status !== 408 && cause.message !== '上一条操作仍在处理'
    // 明确拒绝的操作没有生效，解除锁定；处理中的操作和网络超时沿用原 ID。
    if (rejected) clearRetry()
    const message = rejected
      ? cause.message || '操作未被接受，请检查后重试。'
      : cause.name === 'AbortError' || cause.status === 408
        ? '等待 AI 主持超时，可以重试上次操作。'
        : cause.status
          ? cause.message || '操作结果未确认，可以重试上次操作。'
          : '网络连接中断，可以重试上次操作。'
    await loadGame(entry.gameId)
    // 请求可能已写入成功，只是响应在途中丢失；以刷新后的事件为准。
    const confirmed = game.value?.id === entry.gameId && completed(entry, game.value.events)
    if (!confirmed) error.value = message
  } finally {
    clearTimeout(timeout)
    busy.value = false
  }
}

function act(type, body = '') {
  if (!game.value || game.value.status !== 'active' || retry.value) return
  const text = body.trim()
  if (type !== 'reveal' && !text) return
  const last = game.value.events.at(-1)
  const entry = {
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
  if (busy.value) return
  loadVersion++
  loading.value = false
  game.value = null
  soupId.value = 0
  latest.value = null
  error.value = ''
  clearRetry()
  localStorage.removeItem(gameKey())
}

async function restoreSavedGame() {
  if (!props.user || props.initialSoupId || game.value || loading.value) return
  const id = Number(localStorage.getItem(gameKey()))
  if (!Number.isSafeInteger(id) || id < 1) return
  try {
    const saved = JSON.parse(sessionStorage.getItem(actionKey()) || 'null')
    if (saved?.gameId === id && saved.operationId && saved.type) retry.value = saved
  } catch { sessionStorage.removeItem(actionKey()) }
  await loadGame(id)
}

onMounted(restoreSavedGame)
watch(() => props.user?.id, restoreSavedGame)
</script>

<template>
  <section class="panel solo-page">
    <div class="section-head">
      <h1>单人模式</h1>
      <button v-if="game" class="text-button" :disabled="loading || busy" @click="loadGame(game.id)">刷新对局</button>
    </div>
    <p v-if="error" role="alert">{{ error }}</p>
    <div v-if="!user" class="solo-entry">
      <p>登录后可以和 AI 主持人玩海龟汤。</p>
      <button class="primary-button" @click="emit('login')">登录后试玩</button>
    </div>

    <div v-else-if="!game" class="solo-entry">
      <p class="meta">选择一道尚未看过汤底的题目开始推理。</p>
      <SoupPicker v-model="soupId" />
      <button class="primary-button" :disabled="busy || loading || !soupId" @click="start">{{ busy ? '正在创建…' : loading ? '正在恢复对局…' : '开始单人试玩' }}</button>
    </div>

    <template v-else>
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
