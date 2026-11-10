<script setup>
// 账号列表与操作按钮由角色决定，服务端会再次检查权限。
import { nextTick, onMounted, reactive, ref } from 'vue'
import { request, send } from '../api'

const props = defineProps({ role: { type: String, required: true } })
const users = ref([])
const query = ref('')
const error = ref('')
const message = ref('')
const form = reactive({ username: '', password: '', role: 'user' })
const passwordId = ref(0)
const passwordName = ref('')
const newPassword = ref('')
const modal = ref('')
const modalBox = ref(null)
const createInput = ref(null)
const passwordInput = ref(null)
const fieldsLocked = ref(true)
let returnFocus = null

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
  try {
    users.value = await request(`/api/admin/users?q=${encodeURIComponent(query.value)}`)
    error.value = ''
  } catch (cause) {
    error.value = cause.message
  }
}
async function create() {
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
  }
}
async function setDisabled(item) {
  try {
    await send(`/api/admin/users/${item.id}`, 'PATCH', { disabled: !item.disabled })
    message.value = '账号状态已更新'
    await load()
  } catch (cause) {
    error.value = cause.message
  }
}
async function changePassword() {
  try {
    await send(`/api/admin/users/${passwordId.value}`, 'PATCH', { password: newPassword.value })
    message.value = '密码已修改，该账号需要重新登录'
    error.value = ''
    passwordId.value = 0
    closeModal()
  } catch (cause) {
    error.value = cause.message
  }
}
function canManage(item) {
  return item.role === 'user' || (props.role === 'super_admin' && item.role === 'admin')
}
onMounted(load)
</script>
<template>
  <section class="panel admin-panel">
    <div class="admin-page-heading">
      <div><p class="admin-eyebrow">用户与权限</p><h1>账号管理</h1></div>
      <button class="admin-primary-button" @click="openCreate"><span aria-hidden="true">＋</span> 创建账号</button>
    </div>

    <section class="admin-section">
      <div class="admin-section-heading"><h2>账号列表</h2><span>{{ users.length }} 个账号</span></div>
      <form class="inline-form admin-search" @submit.prevent="load">
        <input v-model="query" aria-label="搜索用户名" placeholder="搜索用户名" />
        <button class="admin-search-button">搜索</button>
      </form>
      <p v-if="error && !modal" role="alert">{{ error }}</p>
      <p v-if="message" role="status">{{ message }}</p>
      <p class="admin-scroll-hint">左右滑动表格查看操作</p>
      <div class="admin-table-wrap">
        <table class="simple-table">
          <thead><tr><th>ID</th><th>用户名</th><th>角色</th><th>状态</th><th>操作</th></tr></thead>
          <tbody>
            <tr v-for="item in users" :key="item.id">
              <td>{{ item.id }}</td>
              <td>{{ item.username }}</td>
              <td><span class="admin-role" :class="item.role">{{ item.role === 'super_admin' ? '超级管理员' : item.role === 'admin' ? '管理员' : '普通用户' }}</span></td>
              <td><span class="admin-status" :class="{ disabled: item.disabled }">{{ item.disabled ? '已停用' : '正常' }}</span></td>
              <td v-if="canManage(item)">
                <div class="admin-row-actions">
                  <button class="admin-table-button" @click="setDisabled(item)">{{ item.disabled ? '恢复' : '停用' }}</button>
                  <button class="admin-table-button" @click="openPassword(item)">修改密码</button>
                </div>
              </td>
              <td v-else></td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>

    <div v-if="modal" class="admin-modal-backdrop" @click.self="closeModal">
      <section ref="modalBox" class="admin-modal" role="dialog" aria-modal="true" :aria-label="modal === 'create' ? '创建账号' : '修改密码'" @keydown.esc.stop.prevent="closeModal" @keydown.tab="trapFocus">
        <div class="admin-modal-heading">
          <div><p class="admin-eyebrow">账号管理</p><h2>{{ modal === 'create' ? '创建账号' : '修改密码' }}</h2></div>
          <button type="button" class="admin-close-button" aria-label="关闭弹窗" @click="closeModal">×</button>
        </div>
        <form v-if="modal === 'create'" autocomplete="off" @submit.prevent="create">
          <label>用户名
            <input ref="createInput" v-model="form.username" name="new-account-name" autocomplete="off" :readonly="fieldsLocked" @focus="fieldsLocked = false" required maxlength="20" placeholder="输入用户名" />
          </label>
          <label>初始密码
            <input v-model="form.password" name="new-account-secret" type="password" autocomplete="new-password" :readonly="fieldsLocked" @focus="fieldsLocked = false" required minlength="8" placeholder="至少 8 位" />
          </label>
          <label>角色
            <select v-model="form.role">
              <option value="user">普通用户</option>
              <option v-if="role === 'super_admin'" value="admin">管理员</option>
            </select>
          </label>
          <p v-if="error" role="alert">{{ error }}</p>
          <div class="admin-modal-actions"><button type="button" class="admin-secondary-button" @click="closeModal">取消</button><button class="admin-primary-button">创建账号</button></div>
        </form>
        <form v-else autocomplete="off" @submit.prevent="changePassword">
          <p class="admin-form-note">账号：{{ passwordName }}</p>
          <label>新密码
            <input ref="passwordInput" v-model="newPassword" name="updated-account-secret" type="password" autocomplete="new-password" required minlength="8" placeholder="输入新密码" />
          </label>
          <p class="admin-form-note">修改后，该账号需要重新登录。</p>
          <p v-if="error" role="alert">{{ error }}</p>
          <div class="admin-modal-actions"><button type="button" class="admin-secondary-button" @click="closeModal">取消</button><button class="admin-primary-button">保存密码</button></div>
        </form>
      </section>
    </div>
  </section>
</template>
