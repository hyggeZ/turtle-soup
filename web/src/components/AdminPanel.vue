<script setup>
// 账号列表与操作按钮由角色决定，服务端会再次检查权限。
import { onMounted, reactive, ref } from 'vue'
import { request, send } from '../api'

const props = defineProps({ role: { type: String, required: true } })
const users = ref([])
const query = ref('')
const error = ref('')
const message = ref('')
const form = reactive({ username: '', password: '', role: 'user' })
const passwordId = ref(0)
const newPassword = ref('')

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
async function resetPassword() {
  try {
    await send(`/api/admin/users/${passwordId.value}`, 'PATCH', { password: newPassword.value })
    message.value = '密码已重置，该账号需要重新登录'
    error.value = ''
    passwordId.value = 0
    newPassword.value = ''
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
    <h1>账号管理</h1>
    <section class="admin-section">
      <h2>创建账号</h2>
      <form @submit.prevent="create">
        <div class="admin-create-grid">
          <label>用户名<input v-model="form.username" required maxlength="20" /></label>
          <label>初始密码<input v-model="form.password" type="password" required minlength="8" /></label>
          <label>角色
            <select v-model="form.role">
              <option value="user">普通用户</option>
              <option v-if="role === 'super_admin'" value="admin">管理员</option>
            </select>
          </label>
        </div>
        <button class="admin-primary-button">创建账号</button>
      </form>
    </section>

    <section class="admin-section">
      <h2>账号列表</h2>
      <form class="inline-form admin-search" @submit.prevent="load">
        <input v-model="query" aria-label="搜索用户名" placeholder="搜索用户名" />
        <button class="admin-search-button">搜索</button>
      </form>
      <p v-if="error" role="alert">{{ error }}</p>
      <p v-if="message" role="status">{{ message }}</p>
      <p class="admin-scroll-hint">左右滑动表格查看操作</p>
      <div class="admin-table-wrap">
        <table class="simple-table">
          <thead><tr><th>ID</th><th>用户名</th><th>角色</th><th>状态</th><th>操作</th></tr></thead>
          <tbody>
            <tr v-for="item in users" :key="item.id">
              <td>{{ item.id }}</td>
              <td>{{ item.username }}</td>
              <td><span class="admin-role">{{ item.role === 'super_admin' ? '超级管理员' : item.role === 'admin' ? '管理员' : '普通用户' }}</span></td>
              <td><span class="admin-status" :class="{ disabled: item.disabled }">{{ item.disabled ? '已停用' : '正常' }}</span></td>
              <td v-if="canManage(item)">
                <div class="admin-row-actions">
                  <button class="admin-table-button" @click="setDisabled(item)">{{ item.disabled ? '恢复' : '停用' }}</button>
                  <button class="admin-table-button" @click="passwordId = item.id; newPassword = ''">重置密码</button>
                </div>
              </td>
              <td v-else></td>
            </tr>
          </tbody>
        </table>
      </div>
      <form v-if="passwordId" class="reset-form" @submit.prevent="resetPassword">
        <label>为账号 {{ passwordId }} 设置新密码<input v-model="newPassword" type="password" minlength="8" required /></label>
        <div class="admin-row-actions">
          <button class="admin-primary-button">确认重置</button>
          <button type="button" class="admin-table-button" @click="passwordId = 0">取消</button>
        </div>
      </form>
    </section>
  </section>
</template>
