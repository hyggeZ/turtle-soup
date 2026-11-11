<script setup>
// 广场与用户主页复用列表，作者 ID 用于筛选。
import { onMounted, onUnmounted, ref, watch } from 'vue'
import { request } from '../api'

const props = defineProps({ author: { type: Number, default: 0 }, refresh: { type: Number, default: 0 } })
const emit = defineEmits(['open', 'profile'])
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
  <section class="feed-section">
    <div class="feed-heading">
      <h2>{{ author ? '发布的汤' : '最新发布' }}</h2>
      <span class="feed-count">已显示 {{ items.length }} 道</span>
    </div>
    <div v-if="error" role="alert">
      <p>{{ error }}</p>
      <button class="small-button" @click="load">重试</button>
    </div>
    <p v-if="loading" role="status" class="empty-state">正在加载汤题…</p>
    <p v-else-if="!items.length && !error" class="empty-state">还没有汤题。</p>

    <article v-for="item in items" :key="item.id" class="soup-entry">
      <button class="entry-title" @click="emit('open', item.id)">{{ item.title }}</button>
      <p class="surface entry-preview">{{ item.surface }}</p>
      <div class="entry-tags">
        <span v-if="item.hidden">已隐藏 · 仅作者可见</span>
        <span>{{ item.genre }}</span>
        <span>难度 {{ item.difficulty }}/5</span>
        <span>恐怖 {{ item.horror }}/5</span>
      </div>
      <div class="entry-bottom">
        <div class="entry-author">
          <button class="text-button" @click="emit('profile', item.authorId)">@{{ item.author }}</button>
          <span>·</span>
          <time :datetime="item.createdAt">{{ new Date(item.createdAt).toLocaleDateString('zh-CN') }}</time>
        </div>
        <div class="entry-stats"><span>♡ {{ item.likes }}</span><span>☆ {{ item.bookmarks }}</span><span>评论 {{ item.comments }}</span></div>
      </div>
    </article>

    <p v-if="moreError" role="alert">{{ moreError }}</p>
    <button v-if="hasMore" class="small-button" :disabled="loadingMore" @click="loadMore">
      {{ loadingMore ? '正在加载…' : moreError ? '重试加载更多' : '加载更多题目' }}
    </button>
  </section>
</template>
