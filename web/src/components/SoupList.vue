<script setup>
// 广场与用户主页复用同一列表，作者 ID 用于筛选。
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
  } catch (cause) { error.value = cause.message }
}
watch(() => [props.author, props.refresh], () => { page.value = 1; load() })
onMounted(load)
</script>
<template>
  <section><h2>{{ author ? '发布的汤' : '汤题广场' }}</h2>
    <p v-if="error" role="alert">{{ error }}</p><p v-if="!items.length && !error" class="muted">还没有汤题。</p>
    <article v-for="item in items" :key="item.id" class="panel soup-card">
      <button class="title-button" @click="emit('open', item.id)">{{ item.title }}</button>
      <p class="surface">{{ item.surface }}</p>
      <div class="meta"><span>{{ item.genre }} · 难度 {{ item.difficulty }}/5 · 恐怖 {{ item.horror }}/5</span>
        <button class="text-button" @click="emit('profile', item.authorId)">@{{ item.author }}</button></div>
      <p class="muted">♥ {{ item.likes }}　★ {{ item.bookmarks }}　评论 {{ item.comments }}</p>
    </article>
    <div class="pager"><button :disabled="page === 1" @click="page--; load()">上一页</button><span>第 {{ page }} 页</span><button :disabled="items.length < 20" @click="page++; load()">下一页</button></div>
  </section>
</template>
