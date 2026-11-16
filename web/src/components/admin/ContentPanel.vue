<script setup>
// 后台题目列表不读取汤底；管理员只处理低于自己权限级别的作者作品。
import { onMounted, onUnmounted, ref } from 'vue'
import { request, send } from '../../api'
import ConfirmDialog from '../shared/ConfirmDialog.vue'

const props = defineProps({ role: { type: String, required: true } })
const emit = defineEmits(['back'])

const items = ref([])
const query = ref('')
const status = ref('all')
const loading = ref(false)
const loadingMore = ref(false)
const hasMore = ref(false)
const busyId = ref(0)
const error = ref('')
const moreError = ref('')
const message = ref('')
const confirmOpen = ref(false)
const pendingItem = ref(null)
let activeQuery = ''
let activeStatus = 'all'
let loadVersion = 0

function path(before = 0) {
  const params = new URLSearchParams({ q: activeQuery, status: activeStatus })
  if (before) params.set('before', String(before))
  return `/api/admin/soups?${params}`
}

async function load() {
  const version = ++loadVersion
  activeQuery = query.value.trim()
  activeStatus = status.value
  items.value = []
  hasMore.value = false
  error.value = ''
  moreError.value = ''
  message.value = ''
  loading.value = true
  try {
    const next = await request(path())
    if (version !== loadVersion) return
    items.value = next
    hasMore.value = next.length === 50
  } catch (cause) {
    if (version === loadVersion) error.value = cause.message
  } finally {
    if (version === loadVersion) loading.value = false
  }
}

async function loadMore() {
  if (loading.value || loadingMore.value || !hasMore.value || !items.value.length) return
  const version = loadVersion
  loadingMore.value = true
  moreError.value = ''
  try {
    const next = await request(path(items.value.at(-1).id))
    if (version !== loadVersion) return
    items.value = [...items.value, ...next]
    hasMore.value = next.length === 50
  } catch (cause) {
    if (version === loadVersion) moreError.value = cause.message
  } finally {
    if (version === loadVersion) loadingMore.value = false
  }
}

function askToHide(item) {
  if (busyId.value || item.status === 'deleted' || !canManage(item)) return
  if (item.status === 'hidden') {
    setHidden(item)
    return
  }
  pendingItem.value = item
  confirmOpen.value = true
}

function confirmHide() {
  const item = pendingItem.value
  pendingItem.value = null
  if (item) setHidden(item)
}

async function setHidden(item) {
  if (busyId.value || item.status === 'deleted') return
  const hidden = item.status !== 'hidden'
  busyId.value = item.id
  error.value = ''
  message.value = ''
  try {
    const result = await send(`/api/admin/soups/${item.id}`, 'PATCH', { hidden })
    await load()
    message.value = hidden
      ? `题目已隐藏，关闭 ${result.closedRooms} 间未开局房间。`
      : '题目已恢复公开。'
  } catch (cause) {
    error.value = cause.message
  } finally {
    busyId.value = 0
  }
}

function label(value) {
  return { visible: '公开', hidden: '已隐藏', deleted: '作者已删除' }[value] || value
}
function canManage(item) {
  return item.authorRole === 'user' || (props.role === 'super_admin' && item.authorRole === 'admin')
}
function roleLabel(value) { return value === 'admin' ? '管理员' : '用户' }

onMounted(load)
onUnmounted(() => { loadVersion++ })
</script>

<template>
  <main class="inner-screen admin-soups-page">
    <div class="page-heading">
      <button type="button" class="back-ticket" @click="emit('back')">← 返回后台</button>
      <div><p class="eyebrow">02 / STORIES</p><h1>题目管理</h1></div>
      <span class="heading-stamp">内容档案</span>
    </div>
    <section class="paper panel-soups">
      <div class="paper-heading"><div><span class="section-number">02</span><h2>题目列表</h2><small>已显示 {{ items.length }} 道题</small></div></div>
      <form class="toolbar" @submit.prevent="load">
        <label class="search-field"><span aria-hidden="true">⌕</span><input v-model="query" aria-label="搜索题目或作者" maxlength="50" placeholder="搜索题目或作者" /></label>
        <label class="select-wrap"><select v-model="status" aria-label="题目状态"><option value="all">全部状态</option><option value="visible">公开</option><option value="hidden">已隐藏</option><option value="deleted">作者已删除</option></select></label>
        <button type="submit" class="action-button ink">筛选 <span aria-hidden="true">→</span></button>
      </form>
      <p v-if="error" class="admin-error" role="alert">{{ error }}</p>
      <p v-if="message" class="admin-success" role="status">{{ message }}</p>
      <div class="table-wrap">
        <table class="dossier-table soup-table" aria-label="题目列表">
          <thead><tr><th>题目</th><th>作者</th><th>分类</th><th>状态</th><th>操作</th></tr></thead>
          <tbody>
            <tr v-for="item in items" :key="item.id">
              <td><strong>{{ item.title }}</strong><details><summary class="inline-link">查看汤面</summary><p class="soup-excerpt">{{ item.surface }}</p></details></td>
              <td>@{{ item.author }}<small>{{ roleLabel(item.authorRole) }}</small></td>
              <td><span class="pill">{{ item.genre }}</span><small>难度 {{ item.difficulty }}/5 · 恐怖 {{ item.horror }}/5</small></td>
              <td><span class="status" :class="{ hidden: item.status === 'hidden', disabled: item.status === 'deleted' }"><i aria-hidden="true"></i>{{ label(item.status) }}</span></td>
              <td><button v-if="item.status !== 'deleted' && canManage(item)" type="button" class="mini-button" :class="item.status === 'hidden' ? 'restore' : 'danger'" :disabled="!!busyId" @click="askToHide(item)">{{ busyId === item.id ? '正在处理…' : item.status === 'hidden' ? '恢复公开' : '隐藏' }}</button><span v-else class="locked">{{ item.status === 'deleted' ? '不可恢复' : '无权限操作' }}</span></td>
            </tr>
          </tbody>
        </table>
      </div>
      <div class="mobile-soups" aria-label="题目列表">
        <article v-for="item in items" :key="item.id" class="soup-card">
          <div class="soup-card-head"><strong>{{ item.title }}</strong><span class="status" :class="{ hidden: item.status === 'hidden', disabled: item.status === 'deleted' }"><i aria-hidden="true"></i>{{ label(item.status) }}</span></div>
          <p class="soup-card-meta">@{{ item.author }} · {{ roleLabel(item.authorRole) }}</p>
          <p class="soup-card-meta"><span class="pill">{{ item.genre }}</span> 难度 {{ item.difficulty }}/5 · 恐怖 {{ item.horror }}/5</p>
          <details><summary class="inline-link">查看汤面</summary><p class="soup-excerpt">{{ item.surface }}</p></details>
          <div class="soup-card-actions"><button v-if="item.status !== 'deleted' && canManage(item)" type="button" class="mini-button" :class="item.status === 'hidden' ? 'restore' : 'danger'" :disabled="!!busyId" @click="askToHide(item)">{{ busyId === item.id ? '正在处理…' : item.status === 'hidden' ? '恢复公开' : '隐藏' }}</button><span v-else class="locked">{{ item.status === 'deleted' ? '不可恢复' : '无权限操作' }}</span></div>
        </article>
      </div>
      <p v-if="loading" class="admin-state" role="status">正在加载题目…</p>
      <p v-else-if="!items.length && !error" class="admin-state">没有符合条件的题目。</p>
      <p v-if="moreError" class="admin-error" role="alert">{{ moreError }}</p>
      <div class="paper-foot"><span>已开局对局不受隐藏影响；作者删除的题目不可恢复。</span><button v-if="hasMore" type="button" class="mini-button" :disabled="loadingMore" @click="loadMore">{{ loadingMore ? '正在加载…' : moreError ? '重试加载更多' : '加载更多 →' }}</button></div>
    </section>
    <ConfirmDialog v-model:open="confirmOpen" title="隐藏题目" message="隐藏题目会关闭所有尚未开局的房间。恢复题目公开后，这些房间也不会重新打开。" confirm-text="确认隐藏" danger @confirm="confirmHide" />
  </main>
</template>
