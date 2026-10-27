<script setup>
// 广场与用户主页复用列表，作者 ID 用于筛选。
import { onMounted, ref, watch } from 'vue'
import { request } from '../api'

const props = defineProps({ author: { type: Number, default: 0 }, refresh: { type: Number, default: 0 } })
const emit = defineEmits(['open', 'profile'])
const items = ref([])
const page = ref(1)
const error = ref('')

async function load() {
  try {
    const query = new URLSearchParams({ page: String(page.value) })
    if (props.author) query.set('author', String(props.author))
    items.value = await request(`/api/soups?${query}`)
    error.value = ''
  } catch (cause) {
    error.value = cause.message
  }
}

watch(() => [props.author, props.refresh], () => { page.value = 1; load() })
onMounted(load)
</script>

<template>
  <section class="feed-section">
    <div class="feed-heading">
      <h2>{{ author ? '发布的汤' : '最新发布' }}</h2>
      <span class="feed-count">第 {{ page }} 页</span>
    </div>
    <p v-if="error" role="alert">{{ error }}</p>
    <p v-if="!items.length && !error" class="empty-state">还没有汤题。</p>

    <article v-for="item in items" :key="item.id" class="soup-entry">
      <button class="entry-title" @click="emit('open', item.id)">{{ item.title }}</button>
      <p class="surface entry-preview">{{ item.surface }}</p>
      <div class="entry-tags">
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

    <div class="pager">
      <button :disabled="page === 1" @click="page--; load()">上一页</button>
      <span>第 {{ page }} 页</span>
      <button :disabled="items.length < 20" @click="page++; load()">下一页</button>
    </div>
  </section>
</template>
