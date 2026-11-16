<script setup>
// 发布与编辑共用表单；编辑时从作者可见的详情读取原题。
import { reactive, ref, watch } from 'vue'
import { request, send } from '../../api'

const props = defineProps({ editId: { type: Number, default: 0 } })
const emit = defineEmits(['created', 'saved', 'cancel'])
const form = reactive({ title: '', surface: '', answer: '', genre: '本格', difficulty: 3, horror: 0 })
const error = ref('')
const busy = ref(false)
const loading = ref(false)
const ready = ref(true)
let loadKey = 0

function reset() {
  Object.assign(form, { title: '', surface: '', answer: '', genre: '本格', difficulty: 3, horror: 0 })
}

async function load() {
  const key = ++loadKey
  reset()
  error.value = ''
  ready.value = !props.editId
  if (!props.editId) { loading.value = false; return }

  loading.value = true
  try {
    const soup = await request(`/api/soups/${props.editId}`)
    if (key !== loadKey) return
    if (typeof soup.answer !== 'string') throw new Error('无法读取汤底，请确认登录的是题目作者账号')
    Object.assign(form, {
      title: soup.title, surface: soup.surface, answer: soup.answer,
      genre: soup.genre, difficulty: soup.difficulty, horror: soup.horror,
    })
    ready.value = true
  } catch (cause) {
    if (key === loadKey) error.value = cause.message
  } finally {
    if (key === loadKey) loading.value = false
  }
}

async function submit() {
  if (busy.value || loading.value || !ready.value) return
  busy.value = true
  error.value = ''
  try {
    const path = props.editId ? `/api/soups/${props.editId}` : '/api/soups'
    const result = await send(path, props.editId ? 'PATCH' : 'POST', form)
    emit(props.editId ? 'saved' : 'created', result.id)
  } catch (cause) {
    error.value = cause.message
  } finally {
    busy.value = false
  }
}

watch(() => props.editId, load, { immediate: true })
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
            <label class="game-field">标题<input v-model.trim="form.title" maxlength="80" placeholder="给这道汤取一个名字" required /></label>
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
        <div class="game-editor-submit"><button class="game-stamp" :disabled="busy">{{ busy ? '正在保存…' : editId ? '保存修改' : '发布题目' }} <span aria-hidden="true">↗</span></button></div>
      </form>
      <div v-if="error" class="game-feed-error" role="alert"><p>{{ error }}</p><button v-if="editId && !ready && !loading" class="game-stamp game-stamp--paper game-stamp--small" @click="load">重新读取</button></div>
    </div>
  </main>
</template>
