<script setup>
// 发布和编辑共用表单；编辑时只从作者可见的题目详情读取汤底。
import { reactive, ref, watch } from 'vue'
import { request, send } from '../api'

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
  }
  catch (cause) { error.value = cause.message }
  finally { busy.value = false }
}

watch(() => props.editId, load, { immediate: true })
</script>
<template>
  <section class="panel"><div class="section-head"><h2>{{ editId ? '编辑海龟汤' : '发布海龟汤' }}</h2><button class="text-button" @click="emit('cancel')">取消</button></div>
    <p v-if="loading" class="muted">正在读取题目…</p>
    <form v-else-if="ready" @submit.prevent="submit">
      <label>标题<input v-model="form.title" maxlength="80" required /></label>
      <label>汤面<textarea v-model="form.surface" rows="5" maxlength="2000" required /></label>
      <label>汤底<textarea v-model="form.answer" rows="7" maxlength="5000" required /></label>
      <div class="form-row"><label>类型<select v-model="form.genre"><option>本格</option><option>变格</option></select></label>
        <label>难度<select v-model.number="form.difficulty"><option v-for="n in 5" :key="n" :value="n">{{ n }}</option></select></label>
        <label>恐怖程度<select v-model.number="form.horror"><option v-for="n in 6" :key="n" :value="n - 1">{{ n - 1 }}</option></select></label></div>
      <button :disabled="busy">{{ busy ? '正在保存…' : editId ? '保存修改' : '发布' }}</button>
    </form><p v-if="error" role="alert">{{ error }}</p>
    <button v-if="editId && !ready && !loading" class="small-button" @click="load">重新读取</button>
  </section>
</template>
