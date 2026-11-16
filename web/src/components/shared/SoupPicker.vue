<script setup>
// 选题筛选在服务端完成，使用游标读取后续题目。
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { request } from '../../api'

const props = defineProps({ modelValue: { type: Number, default: 0 }, preview: { type: Boolean, default: false } })
const emit = defineEmits(['update:modelValue'])
const soups = ref([])
const cursor = ref(null)
const more = ref(true)
const loading = ref(false)
const error = ref('')
const filters = reactive({ genre: '', difficulty: 0, horror: -1 })
let version = 0
const selected = computed({
  get: () => props.modelValue,
  set: value => emit('update:modelValue', value),
})
const chosenSoup = computed(() => soups.value.find(item => item.id === props.modelValue))

function addSoup(soup) {
  if (!soups.value.some(item => item.id === soup.id)) soups.value.push(soup)
}

async function loadMore() {
  if (loading.value || !more.value) return
  loading.value = true
  const current = version
  try {
    const query = new URLSearchParams()
    if (cursor.value) {
      query.set('before', cursor.value.createdAt)
      query.set('beforeId', String(cursor.value.id))
    }
    if (filters.genre) query.set('genre', filters.genre)
    if (filters.difficulty) query.set('difficulty', String(filters.difficulty))
    if (filters.horror >= 0) query.set('horror', String(filters.horror))
    const items = await request(`/api/soups?${query}`)
    if (current !== version) return
    items.forEach(addSoup)
    if (items.length) cursor.value = items.at(-1)
    more.value = items.length === 20
    error.value = ''
  } catch (cause) {
    if (current === version) error.value = cause.message
  } finally {
    if (current === version) loading.value = false
  }
}

async function loadSelected(id) {
  if (!id || soups.value.some(item => item.id === id)) return
  try {
    const soup = await request(`/api/soups/${id}`)
    if (id === props.modelValue) addSoup(soup)
  } catch (cause) {
    if (id === props.modelValue) error.value = cause.message
  }
}

watch(() => [filters.genre, filters.difficulty, filters.horror], () => {
  // 切换条件后清空游标，旧请求返回时也不会混入新列表。
  version++
  soups.value = []
  cursor.value = null
  more.value = true
  loading.value = false
  error.value = ''
  selected.value = 0
  loadMore()
})
watch(() => props.modelValue, loadSelected)
onMounted(() => {
  loadMore()
  loadSelected(props.modelValue)
})
</script>

<template>
  <div class="play-soup-picker">
    <div class="play-picker-filters">
      <label class="play-field">类型
        <select v-model="filters.genre"><option value="">全部</option><option value="本格">本格</option><option value="变格">变格</option></select>
      </label>
      <label class="play-field">难度
        <select v-model.number="filters.difficulty"><option :value="0">全部</option><option v-for="level in 5" :key="level" :value="level">{{ level }}/5</option></select>
      </label>
      <label class="play-field">恐怖程度
        <select v-model.number="filters.horror"><option :value="-1">全部</option><option v-for="level in 6" :key="level - 1" :value="level - 1">{{ level - 1 }}/5</option></select>
      </label>
    </div>
    <div class="play-picker-list" role="group" aria-label="选择题目">
      <button v-for="(soup, index) in soups" :key="soup.id" type="button" class="play-picker-item" :class="{ selected: selected === soup.id }" :aria-pressed="selected === soup.id" @click="selected = soup.id">
        <span class="play-row-number">{{ String(index + 1).padStart(2, '0') }}</span>
        <span><strong>{{ soup.title }}</strong><small>{{ soup.genre }} · 难度 {{ soup.difficulty }}/5 · 恐怖 {{ soup.horror }}/5</small></span>
        <span class="play-picker-check">{{ selected === soup.id ? '✓' : '→' }}</span>
      </button>
      <p v-if="loading && !soups.length" class="play-empty" role="status">正在加载题目…</p>
      <p v-if="!loading && !soups.length && !error" class="play-empty">暂无可选题目。</p>
    </div>
    <button v-if="more" type="button" class="play-link play-picker-more" :disabled="loading" @click="loadMore">{{ loading ? '加载中…' : '查看更多题目 ↓' }}</button>
    <div v-if="preview && chosenSoup" class="play-picker-preview"><strong>{{ chosenSoup.title }}</strong><p class="surface">{{ chosenSoup.surface }}</p></div>
    <p v-if="error" class="play-alert" role="alert">{{ error }}</p>
  </div>
</template>
