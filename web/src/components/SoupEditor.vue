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
  <section class="editor-page-h4">
    <p class="page-crumb"><button class="text-button" @click="emit('cancel')">题目社区</button><span>/</span>{{ editId ? '编辑题目' : '发布汤题' }}</p>
    <p v-if="loading" class="editor-state" role="status">正在读取题目…</p>
    <form v-else-if="ready" class="editor-grid" @submit.prevent="submit">
      <div class="editor-main">
        <div class="editor-head"><h2>{{ editId ? '编辑题目' : '新题目' }}</h2><button type="button" class="detail-text-button" @click="emit('cancel')">取消</button></div>
        <label class="editor-field">标题<input v-model="form.title" maxlength="80" placeholder="给这道汤取一个名字" required /></label>
        <label class="editor-field">汤面<textarea v-model="form.surface" rows="5" maxlength="2000" placeholder="玩家最先看到的故事。留下足够线索，也保留谜题。" required></textarea></label>
        <label class="editor-field">汤底<textarea v-model="form.answer" rows="7" maxlength="5000" placeholder="写下完整真相。玩家需要主动查看，正常浏览题目时不会展示。" required></textarea></label>
      </div>
      <aside class="editor-aside">
        <h3>题目设置</h3><span class="aside-mark" aria-hidden="true"></span>
        <label>类型<select v-model="form.genre"><option>本格</option><option>变格</option></select></label>
        <label>难度<select v-model.number="form.difficulty"><option v-for="n in 5" :key="n" :value="n">{{ n }} / 5</option></select></label>
        <label>恐怖程度<select v-model.number="form.horror"><option v-for="n in 6" :key="n" :value="n - 1">{{ n - 1 }} / 5</option></select></label>
        <p>发布后，其他人可以查看汤面、评论、收藏，也可以用这道题开始单人或多人对局。</p>
      </aside>
      <div class="editor-foot"><button class="primary-button" :disabled="busy">{{ busy ? '正在保存…' : editId ? '保存修改 ↗' : '发布汤题 ↗' }}</button><span>标题、汤面与汤底均为必填</span></div>
    </form>
    <p v-if="error" role="alert">{{ error }}</p>
    <button v-if="editId && !ready && !loading" class="small-button" @click="load">重新读取</button>
  </section>
</template>
