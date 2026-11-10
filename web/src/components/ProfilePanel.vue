<script setup>
// 用户主页公开可读；资料编辑只在查看本人主页时出现。
import { onMounted, reactive, ref, watch } from 'vue'
import { request, send } from '../api'
import SoupList from './SoupList.vue'

const props = defineProps({ id: Number, user: { type: Object, default: null } })
const emit = defineEmits(['back', 'open'])
const profile = ref(null)
const editing = ref(false)
const saving = ref(false)
const error = ref('')
const notice = ref('')
const form = reactive({ displayName: '', bio: '' })

async function load() {
  try {
    profile.value = await request(`/api/users/${props.id}`)
    error.value = ''
  } catch (cause) {
    profile.value = null
    error.value = cause.message
  }
}

async function toggle() {
  try {
    await send(`/api/users/${props.id}/follow`, profile.value.followed ? 'DELETE' : 'PUT')
    await load()
  } catch (cause) {
    error.value = cause.message
  }
}

function edit() {
  form.displayName = profile.value.displayName
  form.bio = profile.value.bio
  editing.value = true
  notice.value = ''
}

async function save() {
  if (saving.value) return
  saving.value = true
  try {
    await send(`/api/users/${props.id}/profile`, 'PATCH', { ...form })
    editing.value = false
    await load()
    notice.value = '资料已保存'
  } catch (cause) {
    error.value = cause.message
  } finally {
    saving.value = false
  }
}

watch(() => props.id, () => { editing.value = false; notice.value = ''; load() })
onMounted(load)
</script>

<template>
  <section v-if="profile">
    <div class="panel">
      <button class="text-button" @click="emit('back')">← 返回广场</button>
      <h2>{{ profile.displayName || profile.username }}</h2>
      <p class="meta">@{{ profile.username }} · 关注者 {{ profile.followers }} · 正在关注 {{ profile.following }}</p>
      <p v-if="profile.bio">{{ profile.bio }}</p>
      <p v-if="notice" class="room-notice" role="status">{{ notice }}</p>
      <button v-if="user && user.id !== profile.id" @click="toggle">{{ profile.followed ? '取消关注' : '关注' }}</button>
      <button v-if="user?.id === profile.id && !editing" @click="edit">编辑资料</button>
      <form v-if="editing" @submit.prevent="save">
        <label>展示名<input v-model="form.displayName" maxlength="40" placeholder="留空时显示用户名" /></label>
        <label>简介<textarea v-model="form.bio" maxlength="300" rows="4"></textarea></label>
        <div class="actions">
          <button :disabled="saving">{{ saving ? '保存中…' : '保存' }}</button>
          <button type="button" class="text-button" :disabled="saving" @click="editing = false">取消</button>
        </div>
      </form>
      <p v-if="error" role="alert">{{ error }}</p>
    </div>
    <SoupList :author="id" @open="emit('open', $event)" />
  </section>
  <p v-else-if="error" role="alert">{{ error }}</p>
</template>
