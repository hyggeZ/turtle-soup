<script setup>
// 建房与等待页共用选题列表；可以继续加载，不限于最新一页。
import { computed, onMounted, ref, watch } from 'vue'
import { request } from '../api'

const props = defineProps({ modelValue: { type: Number, default: 0 } })
const emit = defineEmits(['update:modelValue'])
const soups = ref([])
const page = ref(0)
const more = ref(true)
const loading = ref(false)
const error = ref('')
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
  try {
    const next = page.value + 1
    const items = await request(`/api/soups?page=${next}`)
    items.forEach(addSoup)
    page.value = next
    more.value = items.length === 20
    error.value = ''
  } catch (cause) {
    error.value = cause.message
  } finally {
    loading.value = false
  }
}

async function loadSelected(id) {
  if (!id || soups.value.some(item => item.id === id)) return
  try {
    addSoup(await request(`/api/soups/${id}`))
  } catch (cause) {
    error.value = cause.message
  }
}

watch(() => props.modelValue, loadSelected)
onMounted(() => {
  loadMore()
  loadSelected(props.modelValue)
})
</script>

<template>
  <div class="soup-picker">
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
