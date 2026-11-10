<script setup>
// 选题筛选在服务端完成，翻页不会遗漏较早发布的符合条件题目。
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { request } from '../api'

const props = defineProps({ modelValue: { type: Number, default: 0 } })
const emit = defineEmits(['update:modelValue'])
const soups = ref([])
const page = ref(0)
const more = ref(true)
const loading = ref(false)
const error = ref('')
const filters = reactive({ genre: '', difficulty: 0, horror: -1 })
let version = 0
const selected = computed({
  get: () => props.modelValue,
  set: value => emit('update:modelValue', value),
})

function addSoup(soup) {
  if (!soups.value.some(item => item.id === soup.id)) soups.value.push(soup)
}

async function loadMore() {
  if (loading.value || !more.value) return
  loading.value = true
  const current = version
  try {
    const next = page.value + 1
    const query = new URLSearchParams({ page: String(next) })
    if (filters.genre) query.set('genre', filters.genre)
    if (filters.difficulty) query.set('difficulty', String(filters.difficulty))
    if (filters.horror >= 0) query.set('horror', String(filters.horror))
    const items = await request(`/api/soups?${query}`)
    if (current !== version) return
    items.forEach(addSoup)
    page.value = next
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
  // 切换条件后重新查询第一页，旧请求返回时也不会混入新列表。
  version++
  soups.value = []
  page.value = 0
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
  <div class="soup-picker">
    <div class="form-row soup-filter-row">
      <label>类型
        <select v-model="filters.genre">
          <option value="">全部</option>
          <option value="本格">本格</option>
          <option value="变格">变格</option>
        </select>
      </label>
      <label>难度
        <select v-model.number="filters.difficulty">
          <option :value="0">全部</option>
          <option v-for="level in 5" :key="level" :value="level">{{ level }}/5</option>
        </select>
      </label>
      <label>恐怖程度
        <select v-model.number="filters.horror">
          <option :value="-1">全部</option>
          <option v-for="level in 6" :key="level - 1" :value="level - 1">{{ level - 1 }}/5</option>
        </select>
      </label>
    </div>
    <label>题目
      <select v-model.number="selected" required>
        <option :value="0" disabled>选择题目</option>
        <option v-for="soup in soups" :key="soup.id" :value="soup.id">
          {{ soup.title }} · {{ soup.genre }} · 难度 {{ soup.difficulty }}/5 · 恐怖 {{ soup.horror }}/5
        </option>
      </select>
    </label>
    <button v-if="more" type="button" class="text-button" :disabled="loading" @click="loadMore">
      {{ loading ? '加载中…' : '查看更多题目' }}
    </button>
    <p v-if="error" role="alert">{{ error }}</p>
  </div>
</template>

<style scoped>
.soup-filter-row { width: 100%; }
</style>
