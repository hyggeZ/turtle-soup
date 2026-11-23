<script setup>
// 发布与编辑共用表单，编辑时保存打开草稿所用的版本。
import { nextTick, onUnmounted, reactive, ref, watch } from 'vue'
import { request, send } from '../../api'
import ConfirmDialog from '../shared/ConfirmDialog.vue'
import { usePublishCooldown } from './usePublishCooldown'

const props = defineProps({ editId: { type: Number, default: 0 }, userId: { type: Number, required: true } })
const emit = defineEmits(['created', 'saved', 'cancel'])
const { seconds, blocked, remember } = usePublishCooldown('soup', () => props.userId)
const form = reactive({ title: '', surface: '', answer: '', genre: '本格', difficulty: 3, horror: 0 })
const titleInput = ref(null)
const error = ref('')
const busy = ref(false)
const loading = ref(false)
const ready = ref(true)
const version = ref(0)
const conflict = ref(false)
const latest = ref(null)
const latestError = ref('')
const loadingLatest = ref(false)
const confirmReplace = ref(false)
let loadKey = 0

async function readSoup(id) {
  const soup = await request(`/api/soups/${id}`, { cache: 'no-store' })
  if (typeof soup.answer !== 'string') throw new Error('无法读取汤底，请确认登录的是题目作者账号')
  if (!Number.isSafeInteger(soup.version) || soup.version < 1) throw new Error('无法读取题目版本，请重新读取')
  return soup
}

function applySoup(soup) {
  Object.assign(form, {
    title: soup.title, surface: soup.surface, answer: soup.answer,
    genre: soup.genre, difficulty: soup.difficulty, horror: soup.horror,
  })
  version.value = soup.version
}

async function load() {
  const key = ++loadKey
  const id = props.editId
  Object.assign(form, { title: '', surface: '', answer: '', genre: '本格', difficulty: 3, horror: 0 })
  version.value = 0
  busy.value = false
  conflict.value = false
  latest.value = null
  latestError.value = ''
  loadingLatest.value = false
  confirmReplace.value = false
  error.value = ''
  ready.value = !id
  loading.value = !!id
  if (!id) return

  try {
    const soup = await readSoup(id)
    if (key !== loadKey) return
    applySoup(soup)
    ready.value = true
  } catch (cause) {
    if (key === loadKey) error.value = cause.message
  } finally {
    if (key === loadKey) loading.value = false
  }
}

async function submit() {
  const id = props.editId
  const userId = props.userId
  if (busy.value || loading.value || !ready.value || conflict.value || (!id && blocked(userId))) return
  const key = loadKey
  busy.value = true
  error.value = ''
  try {
    const path = id ? `/api/soups/${id}` : '/api/soups'
    const body = id ? { ...form, version: version.value } : form
    const result = await send(path, id ? 'PATCH' : 'POST', body)
    if (key === loadKey) emit(id ? 'saved' : 'created', result.id)
  } catch (cause) {
    if (!id && remember(userId, cause)) return
    if (key !== loadKey) return
    if (id && cause.status === 409 && cause.code === 'SOUP_VERSION_CONFLICT') {
      conflict.value = true
      return
    }
    error.value = cause.message
  } finally {
    if (key === loadKey) busy.value = false
  }
}

// 读取新版只更新预览，作者确认替换时才更新草稿和编辑版本。
async function viewLatest() {
  if (!conflict.value || loadingLatest.value) return
  const key = loadKey
  loadingLatest.value = true
  latestError.value = ''
  latest.value = null
  try {
    const soup = await readSoup(props.editId)
    if (key === loadKey) latest.value = soup
  } catch (cause) {
    if (key === loadKey) latestError.value = cause.message
  } finally {
    if (key === loadKey) loadingLatest.value = false
  }
}

function replaceDraft() {
  if (!conflict.value || !latest.value) return
  applySoup(latest.value)
  latest.value = null
  conflict.value = false
  nextTick(() => titleInput.value?.focus())
}

watch(() => props.editId, load, { immediate: true })
onUnmounted(() => { loadKey++ })
</script>

<template>
  <main class="game-ui game-editor">
    <div class="game-width">
      <button class="game-back" @click="emit('cancel')"><span aria-hidden="true">←</span> {{ editId ? '返回题目' : '返回首页' }}</button>
      <h1 class="game-title">{{ editId ? '编辑题目' : '发布题目' }}</h1>
      <p v-if="loading" class="game-feed-state" role="status">正在读取题目…</p>
      <form v-else-if="ready" @submit.prevent="submit">
        <div class="game-editor-grid">
          <div class="game-paper-panel game-editor-main">
            <i class="game-corner-tape" aria-hidden="true"></i>
            <label class="game-field">标题<input ref="titleInput" v-model.trim="form.title" maxlength="80" placeholder="给这道汤取一个名字" required /></label>
            <label class="game-field">汤面<textarea v-model.trim="form.surface" rows="5" maxlength="2000" placeholder="玩家首先看到的故事；留下线索，也保留谜题。" required></textarea></label>
            <label class="game-field">汤底<textarea v-model.trim="form.answer" rows="6" maxlength="5000" placeholder="写下完整真相。玩家需要主动查看，普通浏览不会显示。" required></textarea></label>
          </div>
          <aside class="game-paper-panel game-editor-settings">
            <h2>题目设置</h2>
            <div class="game-setting"><p>类型</p><div class="game-segmented"><button v-for="genre in ['本格', '变格']" :key="genre" type="button" :class="{ selected: form.genre === genre }" :aria-pressed="form.genre === genre" @click="form.genre = genre">{{ genre }}</button></div></div>
            <div class="game-setting"><p>难度</p><div class="game-segmented"><button v-for="n in 5" :key="n" type="button" :class="{ selected: form.difficulty === n }" :aria-pressed="form.difficulty === n" @click="form.difficulty = n">{{ n }}</button></div></div>
            <div class="game-setting"><p>恐怖程度</p><div class="game-segmented game-segmented--coral"><button v-for="n in 6" :key="n" type="button" :class="{ selected: form.horror === n - 1 }" :aria-pressed="form.horror === n - 1" @click="form.horror = n - 1">{{ n - 1 }}</button></div></div>
            <p class="game-setting-help">标题、汤面与汤底均为必填。</p>
          </aside>
        </div>
        <div v-if="conflict" class="editor-conflict">
          <p class="game-feed-error" role="alert">题目已被更新，当前草稿已保留。</p>
          <button type="button" class="game-stamp game-stamp--paper game-stamp--small" :disabled="loadingLatest" @click="viewLatest">
            {{ loadingLatest ? '正在读取最新版本…' : '查看最新版本' }}
          </button>
          <p v-if="latestError" class="game-feed-error" role="alert">{{ latestError }}</p>
          <section v-if="latest" class="game-paper-panel editor-latest" aria-labelledby="latest-soup-heading">
            <h2 id="latest-soup-heading">最新版本</h2>
            <dl>
              <dt>标题</dt><dd>{{ latest.title }}</dd>
              <dt>汤面</dt><dd>{{ latest.surface }}</dd>
              <dt>汤底</dt><dd>{{ latest.answer }}</dd>
              <dt>题目设置</dt><dd>{{ latest.genre }} · 难度 {{ latest.difficulty }} · 恐怖程度 {{ latest.horror }}</dd>
            </dl>
            <button type="button" class="game-stamp game-stamp--paper game-stamp--small" @click="confirmReplace = true">用最新版本替换草稿</button>
          </section>
        </div>
        <div class="game-editor-submit">
          <p v-if="!editId && seconds" class="game-detail-hint">请在 {{ seconds }} 秒后重试</p>
          <button class="game-stamp" :disabled="busy || conflict || (!editId && seconds > 0)">{{ busy ? '正在保存…' : editId ? '保存修改' : '发布题目' }} <span aria-hidden="true">↗</span></button>
        </div>
      </form>
      <div v-if="error" class="game-feed-error" role="alert"><p>{{ error }}</p><button v-if="editId && !ready && !loading" class="game-stamp game-stamp--paper game-stamp--small" @click="load">重新读取</button></div>
    </div>
    <ConfirmDialog v-model:open="confirmReplace" title="用最新版本替换草稿？" message="当前未保存的修改会被丢弃。" confirm-text="确认替换" cancel-text="保留草稿" danger @confirm="replaceDraft" />
  </main>
</template>

<style scoped>
.editor-conflict { margin-top: 22px; }
.editor-conflict .game-feed-error { margin: 0 0 10px; }
.editor-conflict button + .game-feed-error { margin: 10px 0 0; }
.editor-latest { margin-top: 15px; padding: 20px; }
.editor-latest h2 { margin: 0; font-size: 20px; }
.editor-latest dl { margin: 10px 0 15px; }
.editor-latest dt { margin-top: 9px; font-weight: 750; }
.editor-latest dd { margin: 3px 0 0; white-space: pre-wrap; overflow-wrap: anywhere; }
</style>
