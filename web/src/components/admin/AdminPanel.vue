<script setup>
// 账号列表与操作按钮由角色决定，服务端会再次检查权限。
import { nextTick, onMounted, onUnmounted, reactive, ref } from 'vue'
import { request, send } from '../../api'

const props = defineProps({ role: { type: String, required: true } })
const emit = defineEmits(['back'])
const users = ref([])
const query = ref('')
const activeQuery = ref('')
const loading = ref(false)
const loadingMore = ref(false)
const hasMore = ref(false)
const moreError = ref('')
const error = ref('')
const message = ref('')
const form = reactive({ username: '', password: '', role: 'user' })
const passwordId = ref(0)
const passwordName = ref('')
const passwordRole = ref('user')
const newPassword = ref('')
const modal = ref('')
const modalBox = ref(null)
const createInput = ref(null)
const passwordInput = ref(null)
const fieldsLocked = ref(true)
const updatingId = ref(0)
const saving = ref(false)
let returnFocus = null
let loadVersion = 0

async function openCreate() {
  form.username = ''
  form.password = ''
  form.role = 'user'
  fieldsLocked.value = true
  error.value = ''
  returnFocus = document.activeElement
  modal.value = 'create'
  await nextTick()
  createInput.value?.focus()
}
async function openPassword(item) {
  passwordId.value = item.id
  passwordName.value = item.username
  passwordRole.value = item.role
  newPassword.value = ''
  error.value = ''
  returnFocus = document.activeElement
  modal.value = 'password'
  await nextTick()
  passwordInput.value?.focus()
}
async function closeModal() {
  modal.value = ''
  form.password = ''
  newPassword.value = ''
  await nextTick()
  if (returnFocus?.isConnected) returnFocus.focus()
  returnFocus = null
}
function trapFocus(event) {
  const items = modalBox.value?.querySelectorAll('button:not([disabled]), input:not([disabled]), select:not([disabled])')
  if (!items?.length) return
  const first = items[0]
  const last = items[items.length - 1]
  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault()
    last.focus()
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault()
    first.focus()
  }
}

async function load() {
  const version = ++loadVersion
  activeQuery.value = query.value.trim()
  users.value = []
  hasMore.value = false
  loadingMore.value = false
  moreError.value = ''
  error.value = ''
  loading.value = true
  try {
    const params = new URLSearchParams({ q: activeQuery.value })
    const next = await request(`/api/admin/users?${params}`)
    if (version !== loadVersion) return
    users.value = next
    hasMore.value = next.length === 100
  } catch (cause) {
    if (version === loadVersion) error.value = cause.message
  } finally {
    if (version === loadVersion) loading.value = false
  }
}

// 搜索框尚未提交的新文字不影响当前列表的翻页查询。
async function loadMore() {
  if (loading.value || loadingMore.value || !hasMore.value || !users.value.length) return
  const version = loadVersion
  const params = new URLSearchParams({ q: activeQuery.value, before: String(users.value.at(-1).id) })
  loadingMore.value = true
  moreError.value = ''
  try {
    const next = await request(`/api/admin/users?${params}`)
    if (version !== loadVersion) return
    users.value = [...users.value, ...next]
    hasMore.value = next.length === 100
  } catch (cause) {
    if (version === loadVersion) moreError.value = cause.message
  } finally {
    if (version === loadVersion) loadingMore.value = false
  }
}
async function create() {
  if (saving.value) return
  saving.value = true
  try {
    await send('/api/admin/users', 'POST', form)
    message.value = '账号已创建'
    error.value = ''
    form.username = ''
    form.password = ''
    closeModal()
    await load()
  } catch (cause) {
    error.value = cause.message
  } finally {
    saving.value = false
  }
}
async function setDisabled(item) {
  if (updatingId.value) return
  const disabled = !item.disabled
  updatingId.value = item.id
  try {
    await send(`/api/admin/users/${item.id}`, 'PATCH', { disabled })
    if (users.value.includes(item)) item.disabled = disabled
    message.value = '账号状态已更新'
    error.value = ''
  } catch (cause) {
    error.value = cause.message
  } finally {
    updatingId.value = 0
  }
}
async function changePassword() {
  if (saving.value) return
  saving.value = true
  try {
    await send(`/api/admin/users/${passwordId.value}`, 'PATCH', { password: newPassword.value })
    message.value = '密码已修改，该账号需要重新登录'
    error.value = ''
    passwordId.value = 0
    closeModal()
  } catch (cause) {
    error.value = cause.message
  } finally {
    saving.value = false
  }
}
function canManage(item) {
  return item.role === 'user' || (props.role === 'super_admin' && item.role === 'admin')
}
function roleLabel(role) {
  return { super_admin: '超级管理员', admin: '管理员', user: '普通用户' }[role] || role
}
function roleClass(role) {
  return { super_admin: 'super', admin: 'admin' }[role] || ''
}
function idLabel(id) { return `#${String(id).padStart(4, '0')}` }
onMounted(load)
onUnmounted(() => { loadVersion++ })
</script>
<template>
  <main class="inner-screen admin-account-page">
    <div class="page-heading">
      <button type="button" class="back-ticket" @click="emit('back')">← 返回后台</button>
      <div><p class="eyebrow">01 / ACCOUNTS</p><h1>账号管理</h1></div>
      <span class="heading-stamp">账号档案</span>
    </div>

    <section class="paper panel-account">
      <div class="paper-heading">
        <div><span class="section-number">01</span><h2>账号列表</h2><small>已显示 {{ users.length }} 个账号</small></div>
        <button type="button" class="action-button primary" @click="openCreate"><span aria-hidden="true">＋</span>创建账号</button>
      </div>
      <form class="toolbar" @submit.prevent="load">
        <label class="search-field"><span aria-hidden="true">⌕</span><input v-model="query" aria-label="搜索用户名" placeholder="搜索用户名" /></label>
        <button type="submit" class="action-button ink">搜索 <span aria-hidden="true">→</span></button>
      </form>
      <p v-if="error && !modal" class="admin-error" role="alert">{{ error }}</p>
      <p v-if="message" class="admin-success" role="status">{{ message }}</p>

      <div class="table-wrap">
        <table class="dossier-table" aria-label="账号列表">
          <thead><tr><th>ID</th><th>用户名</th><th>角色</th><th>状态</th><th>操作</th></tr></thead>
          <tbody>
            <tr v-for="item in users" :key="item.id">
              <td class="id">{{ idLabel(item.id) }}</td>
              <td><span class="avatar" :class="roleClass(item.role)" aria-hidden="true">{{ item.username.slice(0, 1).toUpperCase() }}</span><strong>{{ item.username }}</strong></td>
              <td><span class="pill" :class="roleClass(item.role)">{{ roleLabel(item.role) }}</span></td>
              <td><span class="status" :class="{ disabled: item.disabled }"><i aria-hidden="true"></i>{{ item.disabled ? '已停用' : '正常' }}</span></td>
              <td><div v-if="canManage(item)" class="row-actions">
                <button type="button" class="mini-button" :class="item.disabled ? 'restore' : 'danger'" :disabled="!!updatingId" @click="setDisabled(item)">{{ item.disabled ? '恢复' : '停用' }}</button>
                <button type="button" class="mini-button" @click="openPassword(item)">修改密码</button>
              </div><span v-else class="locked">{{ item.role === 'super_admin' ? '最高权限账号' : '无权限操作' }}</span></td>
            </tr>
          </tbody>
        </table>
      </div>
      <div class="mobile-accounts" aria-label="账号列表">
        <article v-for="item in users" :key="item.id" class="account-card">
          <div><span class="avatar" :class="roleClass(item.role)" aria-hidden="true">{{ item.username.slice(0, 1).toUpperCase() }}</span><strong>{{ item.username }}</strong><small>{{ idLabel(item.id) }}</small></div>
          <span class="pill" :class="roleClass(item.role)">{{ roleLabel(item.role) }}</span>
          <span class="status" :class="{ disabled: item.disabled }"><i aria-hidden="true"></i>{{ item.disabled ? '已停用' : '正常' }}</span>
          <div v-if="canManage(item)" class="row-actions">
            <button type="button" class="mini-button" :class="item.disabled ? 'restore' : 'danger'" :disabled="!!updatingId" @click="setDisabled(item)">{{ item.disabled ? '恢复' : '停用' }}</button>
            <button type="button" class="mini-button" @click="openPassword(item)">修改密码</button>
          </div>
        </article>
      </div>
      <p v-if="loading" class="admin-state" role="status">正在加载账号…</p>
      <p v-else-if="!users.length && !error" class="admin-state">没有找到账号。</p>
      <p v-if="moreError" class="admin-error" role="alert">{{ moreError }}</p>
      <div class="paper-foot"><span>已显示 {{ users.length }} 个账号</span><button v-if="hasMore" type="button" class="mini-button" :disabled="loadingMore" @click="loadMore">{{ loadingMore ? '正在加载…' : moreError ? '重试加载更多' : '加载更多 →' }}</button></div>
    </section>

    <div v-if="modal" class="modal-layer admin-open" @click.self="closeModal">
      <section ref="modalBox" class="modal-paper" role="dialog" aria-modal="true" aria-labelledby="account-modal-title" @keydown.esc.stop.prevent="closeModal" @keydown.tab="trapFocus">
        <div class="modal-heading">
          <span class="modal-symbol" :class="{ key: modal === 'password' }" aria-hidden="true">{{ modal === 'create' ? '＋' : '✳' }}</span>
          <div><p class="eyebrow">{{ modal === 'create' ? 'ACCOUNTS / NEW' : 'ACCOUNTS / SECURITY' }}</p><h2 id="account-modal-title">{{ modal === 'create' ? '创建账号' : '修改密码' }}</h2></div>
          <button type="button" class="modal-close" aria-label="关闭弹窗" @click="closeModal">×</button>
        </div>
        <form v-if="modal === 'create'" class="modal-form" autocomplete="off" @submit.prevent="create">
          <label>用户名<input ref="createInput" v-model="form.username" name="new-account-name" autocomplete="off" :readonly="fieldsLocked" @focus="fieldsLocked = false" required maxlength="20" placeholder="输入用户名" /></label>
          <label>初始密码<input v-model="form.password" name="new-account-secret" type="password" autocomplete="new-password" :readonly="fieldsLocked" @focus="fieldsLocked = false" required minlength="8" placeholder="至少 8 位" /></label>
          <label>角色<select v-model="form.role"><option value="user">普通用户</option><option v-if="role === 'super_admin'" value="admin">管理员</option></select></label>
          <p v-if="error" class="admin-error" role="alert">{{ error }}</p>
          <div class="modal-actions"><button type="button" class="action-button quiet" @click="closeModal">取消</button><button type="submit" class="action-button primary" :disabled="saving">创建账号 <span aria-hidden="true">→</span></button></div>
        </form>
        <form v-else class="modal-form" autocomplete="off" @submit.prevent="changePassword">
          <div class="account-strip"><span class="avatar" :class="roleClass(passwordRole)" aria-hidden="true">{{ passwordName.slice(0, 1).toUpperCase() }}</span><span>账号 <strong>{{ passwordName }}</strong></span><span class="pill" :class="roleClass(passwordRole)">{{ roleLabel(passwordRole) }}</span></div>
          <label>新密码<input ref="passwordInput" v-model="newPassword" name="updated-account-secret" type="password" autocomplete="new-password" required minlength="8" placeholder="输入新密码" /></label>
          <p class="form-note">修改后，该账号需要重新登录。</p>
          <p v-if="error" class="admin-error" role="alert">{{ error }}</p>
          <div class="modal-actions"><button type="button" class="action-button quiet" @click="closeModal">取消</button><button type="submit" class="action-button primary" :disabled="saving">保存密码 <span aria-hidden="true">→</span></button></div>
        </form>
      </section>
    </div>
  </main>
</template>