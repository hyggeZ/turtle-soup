<script setup>
// 题目列表供社区与个人主页复用；游标分页只读取当前账号可见的内容。
import { onMounted, onUnmounted, ref, watch } from 'vue'
import { request } from '../../api'

const props = defineProps({ author: { type: Number, default: 0 }, own: { type: Boolean, default: false }, refresh: { type: Number, default: 0 } })
const emit = defineEmits(['open', 'profile', 'back'])
const items = ref([])
const error = ref('')
const moreError = ref('')
const loading = ref(false)
const loadingMore = ref(false)
const hasMore = ref(false)
let loadVersion = 0

function path(last) {
  const query = new URLSearchParams()
  if (props.author) query.set('author', String(props.author))
  if (last) {
    query.set('before', last.createdAt)
    query.set('beforeId', String(last.id))
  }
  return `/api/soups?${query}`
}

async function load() {
  const version = ++loadVersion
  loading.value = true
  loadingMore.value = false
  hasMore.value = false
  items.value = []
  error.value = ''
  moreError.value = ''
  try {
    const next = await request(path())
    if (version !== loadVersion) return
    items.value = next
    hasMore.value = next.length === 20
  } catch (cause) {
    if (version === loadVersion) error.value = cause.message
  } finally {
    if (version === loadVersion) loading.value = false
  }
}

async function loadMore() {
  if (loading.value || loadingMore.value || !hasMore.value || !items.value.length) return
  const version = loadVersion
  const last = items.value.at(-1)
  loadingMore.value = true
  moreError.value = ''
  try {
    const next = await request(path(last))
    if (version !== loadVersion) return
    const seen = new Set(items.value.map(item => item.id))
    items.value = [...items.value, ...next.filter(item => !seen.has(item.id))]
    hasMore.value = next.length === 20
  } catch (cause) {
    if (version === loadVersion) moreError.value = cause.message
  } finally {
    if (version === loadVersion) loadingMore.value = false
  }
}

watch(() => [props.author, props.refresh], load)
onMounted(load)
onUnmounted(() => { loadVersion++ })
</script>

<template>
  <section class="game-ui" :class="author ? 'game-profile-feed' : 'game-community'">
    <div class="game-width">
      <template v-if="!author">
        <button class="game-back" @click="emit('back')"><span aria-hidden="true">←</span> 返回首页</button>
        <h1 class="game-title">题目社区</h1>
        <p class="game-subtitle">挑一道题，先读汤面，再决定要不要揭开真相。</p>
      </template>
      <h2 v-else class="game-profile-list-title">发布的题目</h2>

      <div v-if="error" class="game-feed-state" role="alert"><p>{{ error }}</p><button class="game-stamp game-stamp--paper game-stamp--small" @click="load">重试</button></div>
      <p v-else-if="loading" class="game-feed-state" role="status">正在加载题目…</p>
      <div v-else-if="!items.length" class="game-feed-state">
        <strong>{{ own ? '你还没有发布题目' : '还没有题目' }}</strong>
      </div>

      <div v-else class="game-case-grid">
        <article v-for="item in items" :key="item.id" class="game-case-card game-paper-panel">
          <div class="game-case-no"><span>CASE / {{ String(item.id).padStart(3, '0') }}</span><time :datetime="item.createdAt">{{ new Date(item.createdAt).toLocaleDateString('zh-CN') }}</time></div>
          <button class="game-case-title" @click="emit('open', item.id)">{{ item.title }}</button>
          <p class="game-case-preview">{{ item.surface }}</p>
          <div class="game-tags"><span v-if="item.hidden" class="game-tag game-tag--coral">已隐藏</span><span class="game-tag game-tag--teal">{{ item.genre }}</span><span class="game-tag">难度 {{ item.difficulty }}/5</span><span class="game-tag">恐怖 {{ item.horror }}/5</span></div>
          <div class="game-case-foot">
            <div><button class="game-text-link" @click="emit('profile', item.authorId)">@{{ item.author }}</button><span> · ♡ {{ item.likes }} · ☆ {{ item.bookmarks }} · 评论 {{ item.comments }}</span></div>
            <button class="game-card-open" @click="emit('open', item.id)">阅读汤面 ↗</button>
          </div>
        </article>
      </div>
      <p v-if="moreError" class="game-feed-error" role="alert">{{ moreError }}</p>
      <div v-if="hasMore" class="game-feed-more"><button class="game-stamp game-stamp--paper game-stamp--small" :disabled="loadingMore" @click="loadMore">{{ loadingMore ? '正在加载…' : moreError ? '重试加载更多' : '加载更多题目' }} <span aria-hidden="true">↗</span></button></div>
    </div>
  </section>
</template>
