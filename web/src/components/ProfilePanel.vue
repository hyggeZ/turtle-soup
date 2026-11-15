<script setup>
// 用户主页公开可读；资料编辑只在查看本人主页时出现。
import { onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { request, send } from '../api'
import SoupList from './SoupList.vue'

const props = defineProps({ id: Number, user: { type: Object, default: null } })
const emit = defineEmits(['back', 'open', 'write', 'profile'])
const profile = ref(null)
const editing = ref(false)
const saving = ref(false)
const loading = ref(false)
const error = ref('')
const notice = ref('')
const form = reactive({ displayName: '', bio: '' })
let loadVersion = 0

async function load() {
  const id = props.id
  const version = ++loadVersion
  loading.value = true
  try {
    const next = await request(`/api/users/${id}`)
    if (version !== loadVersion || id !== props.id) return
    profile.value = next
    error.value = ''
  } catch (cause) {
    if (version !== loadVersion || id !== props.id) return
    profile.value = null
    error.value = cause.message
  } finally {
    if (version === loadVersion) loading.value = false
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

watch(() => props.id, () => { profile.value = null; editing.value = false; notice.value = ''; load() })
onMounted(load)
onUnmounted(() => { loadVersion++ })
</script>

<template>
  <section v-if="profile" class="profile-page-h4">
    <header class="profile-hero">
      <button class="text-button" @click="emit('back')">← 返回社区</button>
      <div class="profile-hero-row">
        <div class="profile-avatar" aria-hidden="true">{{ (profile.displayName || profile.username).slice(0, 1).toUpperCase() }}</div>
        <div class="profile-identity"><h1>{{ profile.displayName || profile.username }}</h1><p>@{{ profile.username }}</p></div>
        <button v-if="user && user.id !== profile.id" class="profile-hero-action" @click="toggle">{{ profile.followed ? '取消关注' : '关注' }} ↗</button>
        <button v-if="user?.id === profile.id && !editing" class="profile-hero-action" @click="edit">编辑资料 ↗</button>
      </div>
    </header>
    <div class="profile-body-h4">
      <div class="profile-feed-wrap"><SoupList :author="id" :own="user?.id === profile.id" @open="emit('open', $event)" @profile="emit('profile', $event)" @write="emit('write')" /></div>
      <aside class="profile-about">
        <h3>个人资料</h3><span class="aside-mark" aria-hidden="true"></span>
        <template v-if="!editing">
          <dl><div><dt>用户名</dt><dd>@{{ profile.username }}</dd></div><div><dt>展示名</dt><dd>{{ profile.displayName || profile.username }}</dd></div><div><dt>简介</dt><dd>{{ profile.bio || '暂无简介' }}</dd></div></dl>
          <p class="profile-counts">关注者 {{ profile.followers }} <span>·</span> 正在关注 {{ profile.following }}</p>
          <button v-if="user?.id === profile.id" class="detail-text-button" @click="edit">编辑资料 ↗</button>
        </template>
        <form v-else class="profile-edit" @submit.prevent="save">
          <label>展示名<input v-model="form.displayName" maxlength="40" placeholder="留空时显示用户名" /></label>
          <label>简介<textarea v-model="form.bio" maxlength="300" rows="4" placeholder="介绍一下自己"></textarea></label>
          <div><button class="primary-button" :disabled="saving">{{ saving ? '保存中…' : '保存' }}</button><button type="button" class="text-button" :disabled="saving" @click="editing = false">取消</button></div>
        </form>
        <p v-if="notice" class="profile-notice" role="status">{{ notice }}</p>
        <p v-if="error" role="alert">{{ error }}</p>
      </aside>
    </div>
  </section>
  <div v-else class="profile-state"><div v-if="error" role="alert"><p>{{ error }}</p><button class="small-button" @click="load">重试</button></div><p v-else-if="loading" role="status">正在加载个人主页…</p></div>
</template>
