<script setup>
// 后台题目列表不读取汤底；管理员只处理低于自己权限级别的作者作品。
import { onMounted, onUnmounted, ref } from 'vue'
import { request, send } from '../api'

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

async function setHidden(item) {
  if (busyId.value || item.status === 'deleted') return
  const hidden = item.status !== 'hidden'
  if (hidden && !window.confirm('隐藏题目会关闭所有尚未开局的房间。恢复题目公开后，这些房间也不会重新打开。确定隐藏吗？')) return
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

onMounted(load)
onUnmounted(() => { loadVersion++ })
</script>

<template>
  <section class="admin-panel admin-content-panel">
    <div class="admin-page-heading">
      <h1>题目管理</h1>
    </div>
    <section class="admin-section">
      <div class="admin-section-heading">
        <h2>题目列表</h2>
        <span>已显示 {{ items.length }} 道题</span>
      </div>
      <p class="muted">已开局对局不受隐藏影响；作者删除的题目不可恢复。</p>
      <form class="inline-form admin-search" @submit.prevent="load">
        <input v-model="query" aria-label="搜索题目或作者" maxlength="50" placeholder="搜索题目或作者" />
        <select v-model="status" aria-label="题目状态">
          <option value="all">全部状态</option>
          <option value="visible">公开</option>
          <option value="hidden">已隐藏</option>
          <option value="deleted">作者已删除</option>
        </select>
        <button class="admin-search-button">筛选</button>
      </form>
      <p v-if="error" role="alert">{{ error }}</p>
      <p v-if="message" role="status">{{ message }}</p>
      <p v-if="loading" role="status">正在加载题目…</p>
      <p v-else-if="!items.length && !error" class="muted">没有符合条件的题目。</p>
      <div v-if="items.length" class="admin-table-wrap">
        <table class="simple-table">
          <thead><tr><th>题目</th><th>作者</th><th>分类</th><th>状态</th><th>操作</th></tr></thead>
          <tbody>
            <tr v-for="item in items" :key="item.id">
              <td>
                <strong>{{ item.title }}</strong>
                <details><summary>查看汤面</summary><p class="admin-soup-surface">{{ item.surface }}</p></details>
              </td>
              <td>@{{ item.author }}<br /><span class="muted">{{ item.authorRole === 'admin' ? '管理员' : '用户' }}</span></td>
              <td>{{ item.genre }} · 难度 {{ item.difficulty }}/5 · 恐怖 {{ item.horror }}/5</td>
              <td><span class="admin-status" :class="{ disabled: item.status !== 'visible' }">{{ label(item.status) }}</span></td>
              <td>
                <button v-if="item.status !== 'deleted'" class="admin-table-button" :disabled="!!busyId" @click="setHidden(item)">
                  {{ busyId === item.id ? '正在处理…' : item.status === 'hidden' ? '恢复公开' : '隐藏' }}
                </button>
                <span v-else class="muted">不可恢复</span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <p v-if="moreError" role="alert">{{ moreError }}</p>
      <button v-if="hasMore" class="admin-secondary-button" :disabled="loadingMore" @click="loadMore">
        {{ loadingMore ? '正在加载…' : moreError ? '重试加载更多' : '加载更多题目' }}
      </button>
    </section>
  </section>
</template>
