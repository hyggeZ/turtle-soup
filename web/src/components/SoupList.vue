<script setup>
// 广场与用户主页复用列表，作者 ID 用于筛选。
import { onMounted, onUnmounted, ref, watch } from 'vue'
import { request } from '../api'

const props = defineProps({ author: { type: Number, default: 0 }, own: { type: Boolean, default: false }, refresh: { type: Number, default: 0 } })
const emit = defineEmits(['open', 'profile', 'lobby', 'solo', 'write'])
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
  <div :class="author ? 'profile-feed' : 'community-grid'">
    <section class="feed-section">
      <div class="feed-heading"><h2>{{ author ? '发布的汤' : '全部题目' }}</h2></div>
      <div v-if="error" class="feed-state" role="alert">
        <p>{{ error }}</p>
        <button class="small-button" @click="load">重试</button>
      </div>
      <p v-if="loading" class="feed-state" role="status">正在加载汤题…</p>
      <div v-else-if="!items.length && !error" class="feed-state feed-empty">
        <span class="empty-mark" aria-hidden="true">?</span>
        <div><strong>{{ own ? '你还没有发布汤题' : '还没有汤题' }}</strong><p>{{ own ? '发布后，题目会显示在这里。' : '稍后再来看看。' }}</p></div>
        <button v-if="own" class="primary-button" @click="emit('write')">＋ 发布汤题</button>
      </div>

      <article v-for="item in items" :key="item.id" class="soup-entry">
        <button class="entry-title" @click="emit('open', item.id)">{{ item.title }} <span aria-hidden="true">↗</span></button>
        <p class="surface entry-preview">{{ item.surface }}</p>
        <div class="entry-tags">
          <span v-if="item.hidden" class="hidden-tag">已隐藏 · 仅作者可见</span>
          <span>{{ item.genre }}</span>
          <span>难度 {{ item.difficulty }}/5</span>
          <span>恐怖 {{ item.horror }}/5</span>
        </div>
        <div class="entry-bottom">
          <div class="entry-author">
            <button class="text-button" @click="emit('profile', item.authorId)">@{{ item.author }}</button>
            <time :datetime="item.createdAt">{{ new Date(item.createdAt).toLocaleDateString('zh-CN') }}</time>
          </div>
          <div class="entry-stats"><span>♡ {{ item.likes }}</span><span>☆ {{ item.bookmarks }}</span><span>评论 {{ item.comments }}</span></div>
        </div>
      </article>

      <p v-if="moreError" role="alert">{{ moreError }}</p>
      <button v-if="hasMore" class="small-button feed-more" :disabled="loadingMore" @click="loadMore">
        {{ loadingMore ? '正在加载…' : moreError ? '重试加载更多' : '加载更多题目' }}
      </button>
    </section>
    <aside v-if="!author" class="community-aside">
      <h3>怎么玩</h3><span class="aside-mark" aria-hidden="true"></span>
      <p>阅读汤面后，用“是 / 否 / 无关”的问题逐步还原真相。</p>
      <div class="aside-links">
        <button @click="emit('lobby')">进入房间大厅 ↗</button>
        <button @click="emit('solo')">开始单人试玩 ↗</button>
        <button @click="emit('write')">发布自己的题目 ↗</button>
      </div>
    </aside>
  </div>
</template>
