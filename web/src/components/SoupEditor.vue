<script setup>
// 发布组件收集汤面、汤底与分类；字段范围仍由后端最终校验。
import { reactive, ref } from 'vue'
import { send } from '../api'
const emit = defineEmits(['created', 'cancel'])
const form = reactive({ title: '', surface: '', answer: '', genre: '本格', difficulty: 3, horror: 0 })
const error = ref('')
const busy = ref(false)
async function publish() {
  busy.value = true
  error.value = ''
  try { const result = await send('/api/soups', 'POST', form); emit('created', result.id) }
  catch (cause) { error.value = cause.message }
  finally { busy.value = false }
}
</script>
<template>
  <section class="panel"><div class="section-head"><h2>发布海龟汤</h2><button class="text-button" @click="emit('cancel')">取消</button></div>
    <form @submit.prevent="publish">
      <label>标题<input v-model="form.title" maxlength="80" required /></label>
      <label>汤面<textarea v-model="form.surface" rows="5" maxlength="2000" required /></label>
      <label>汤底<textarea v-model="form.answer" rows="7" maxlength="5000" required /></label>
      <div class="form-row"><label>类型<select v-model="form.genre"><option>本格</option><option>变格</option></select></label>
        <label>难度<select v-model.number="form.difficulty"><option v-for="n in 5" :key="n" :value="n">{{ n }}</option></select></label>
        <label>恐怖程度<select v-model.number="form.horror"><option v-for="n in 6" :key="n" :value="n - 1">{{ n - 1 }}</option></select></label></div>
      <button :disabled="busy">发布</button>
    </form><p v-if="error" role="alert">{{ error }}</p>
  </section>
</template>
