<script setup>
// 个人主页公开可读；本人可以编辑资料，其他用户可关注或取消关注。
import { computed, nextTick, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { request, send } from '../../api'
import SoupList from '../community/SoupList.vue'

const props = defineProps({ id: Number, user: { type: Object, default: null } })
const emit = defineEmits(['back', 'open', 'profile', 'logout'])
const profile = ref(null)
const own = computed(() => !!props.user && props.user.id === props.id)
const editing = ref(false)
const saving = ref(false)
const following = ref(false)
const loading = ref(false)
const error = ref('')
const editError = ref('')
const notice = ref('')
const nameInput = ref(null)
const modalPanel = ref(null)
const form = reactive({ displayName: '', bio: '' })
let loadVersion = 0
let previousFocus = null

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
  if (following.value) return
  following.value = true
  try {
    await send(`/api/users/${props.id}/follow`, profile.value.followed ? 'DELETE' : 'PUT')
    await load()
  } catch (cause) {
    error.value = cause.message
  } finally {
    following.value = false
  }
}

async function edit() {
  previousFocus = document.activeElement
  form.displayName = profile.value.displayName
  form.bio = profile.value.bio
  editError.value = ''
  editing.value = true
  notice.value = ''
  await nextTick()
  nameInput.value?.focus()
}

function closeEdit() {
  if (!saving.value) editing.value = false
}

// 编辑弹窗打开时让键盘焦点留在表单内，关闭后回到“编辑资料”。
function editKeydown(event) {
  if (event.key === 'Escape') {
    event.preventDefault()
    closeEdit()
    return
  }
  if (event.key !== 'Tab') return
  const items = [...(modalPanel.value?.querySelectorAll('input:not([disabled]), textarea:not([disabled]), button:not([disabled])') || [])]
  if (!items.length) return
  const first = items[0]
  const last = items.at(-1)
  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault()
    last.focus()
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault()
    first.focus()
  }
}

async function save() {
  if (saving.value) return
  saving.value = true
  editError.value = ''
  try {
    await send(`/api/users/${props.id}/profile`, 'PATCH', { ...form })
    editing.value = false
    await load()
    notice.value = '资料已保存'
  } catch (cause) {
    editError.value = cause.message
  } finally {
    saving.value = false
  }
}

watch(() => props.id, () => { profile.value = null; editing.value = false; notice.value = ''; load() })
watch(editing, async opened => {
  if (opened) return
  await nextTick()
  if (previousFocus?.isConnected) previousFocus.focus()
  previousFocus = null
})
onMounted(load)
onUnmounted(() => {
  loadVersion++
  if (previousFocus?.isConnected) previousFocus.focus()
})
</script>

<template>
  <main class="game-ui game-profile">
    <div class="game-width">
      <button class="game-back" @click="emit('back')"><span aria-hidden="true">←</span> 返回</button>
      <h1 class="game-title">{{ own ? '我的主页' : '用户主页' }}</h1>
      <p v-if="loading && !profile" class="game-feed-state" role="status">正在加载个人主页…</p>
      <div v-else-if="!profile && error" class="game-feed-state" role="alert"><p>{{ error }}</p><button class="game-stamp game-stamp--paper game-stamp--small" @click="load">重试</button></div>

      <template v-if="profile">
        <section class="game-paper-panel game-profile-hero">
          <div class="game-avatar" aria-hidden="true">{{ (profile.displayName || profile.username).slice(0, 1).toUpperCase() }}</div>
          <div class="game-profile-identity"><h2>{{ profile.displayName || profile.username }}</h2><p>@{{ profile.username }}</p><div class="game-profile-stats"><span>{{ profile.followers }} 位关注者</span><span>关注 {{ profile.following }} 人</span></div></div>
          <button v-if="own" class="game-stamp game-stamp--teal game-stamp--small" @click="edit">编辑资料 <span aria-hidden="true">↗</span></button>
          <button v-else-if="user" class="game-stamp game-stamp--teal game-stamp--small" :disabled="following" @click="toggle">{{ profile.followed ? '取消关注' : '关注' }} <span aria-hidden="true">↗</span></button>
        </section>
        <div class="game-profile-content">
          <SoupList :author="id" :own="own" @open="emit('open', $event)" @profile="emit('profile', $event)" />
          <aside class="game-paper-panel game-profile-about"><h3>个人资料</h3><p>用户名　@{{ profile.username }}</p><p>简介<br />{{ profile.bio || '暂无简介' }}</p><button v-if="own" class="game-quiet" @click="emit('logout')">退出登录</button></aside>
        </div>
        <p v-if="notice" class="game-profile-notice" role="status">{{ notice }}</p>
        <p v-if="error" class="game-feed-error" role="alert">{{ error }}</p>
      </template>
    </div>

    <div v-if="editing" class="game-modal-shade" @click.self="closeEdit">
      <section ref="modalPanel" class="game-paper-panel game-profile-modal" role="dialog" aria-modal="true" aria-labelledby="game-edit-title" @keydown="editKeydown">
        <p class="game-eyebrow">PROFILE / 编辑资料</p><h2 id="game-edit-title">编辑资料</h2>
        <form @submit.prevent="save"><label class="game-field">展示名<input ref="nameInput" v-model="form.displayName" maxlength="40" placeholder="留空时显示用户名" /><span class="game-field-help">留空时显示用户名。</span></label><label class="game-field">简介<textarea v-model="form.bio" maxlength="300" rows="3" placeholder="介绍一下自己"></textarea></label>
          <p v-if="editError" class="game-feed-error" role="alert">{{ editError }}</p>
          <div class="game-profile-modal-actions"><button type="button" class="game-stamp game-stamp--paper" :disabled="saving" @click="closeEdit">取消</button><button class="game-stamp game-stamp--teal" :disabled="saving">{{ saving ? '保存中…' : '保存资料' }} <span aria-hidden="true">↗</span></button></div>
        </form>
      </section>
    </div>
  </main>
</template>
