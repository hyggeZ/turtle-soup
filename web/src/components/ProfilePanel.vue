<script setup>
// 用户资料展示关注状态及其发布的汤题。
import { onMounted, ref, watch } from 'vue'
import { request, send } from '../api'
import SoupList from './SoupList.vue'
const props = defineProps({ id: Number, user: Object })
const emit = defineEmits(['back', 'open'])
const profile = ref(null)
const error = ref('')
async function load() {
  try { profile.value = await request(`/api/users/${props.id}`); error.value = '' }
  catch (cause) { error.value = cause.message }
}
async function toggle() {
  try { await send(`/api/users/${props.id}/follow`, profile.value.followed ? 'DELETE' : 'PUT'); await load() }
  catch (cause) { error.value = cause.message }
}
watch(() => props.id, load)
onMounted(load)
</script>
<template>
  <section v-if="profile"><div class="panel"><button class="text-button" @click="emit('back')">← 返回广场</button><h2>@{{ profile.username }}</h2>
    <p>关注者 {{ profile.followers }} · 正在关注 {{ profile.following }}</p>
    <button v-if="user && user.id !== profile.id" @click="toggle">{{ profile.followed ? '取消关注' : '关注' }}</button></div>
    <SoupList :author="id" @open="emit('open', $event)" />
    <p v-if="error" role="alert">{{ error }}</p>
  </section><p v-else-if="error" role="alert">{{ error }}</p>
</template>
