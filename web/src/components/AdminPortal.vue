<script setup>
// 后台单独提供登录入口，页面权限以服务端角色为准。
import { ref } from 'vue'
import { send } from '../api'
import AdminPanel from './AdminPanel.vue'
import ContentPanel from './ContentPanel.vue'
import TelemetryPanel from './TelemetryPanel.vue'

defineProps({ user: { type: Object, default: null } })
const emit = defineEmits(['logged-in', 'logout'])
const username = ref('')
const password = ref('')
const error = ref('')
const pages = [
  { id: 'users', title: '账号管理' },
  { id: 'content', title: '题目管理' },
  { id: 'telemetry', title: '运行日志' },
]
const page = ref('users')
async function login() {
  try {
    const user = await send('/api/admin/login', 'POST', { username: username.value, password: password.value })
    password.value = ''
    error.value = ''
    emit('logged-in', user)
  } catch (cause) {
    error.value = cause.message
  }
}
</script>
<template>
  <div v-if="user?.isAdmin" class="admin-layout">
    <aside class="admin-sidebar" aria-label="后台导航">
      <a class="admin-brand" href="/admin">海龟汤后台</a>
      <nav class="admin-nav">
        <span class="admin-nav-title">管理</span>
        <button
          v-for="item in pages"
          :key="item.id"
          :class="{ active: page === item.id }"
          @click="page = item.id"
        >{{ item.title }}</button>
      </nav>
      <a class="admin-back-link" href="/">返回游戏系统</a>
    </aside>
    <div class="admin-workspace">
      <header class="admin-topbar">
        <span>管理后台</span>
        <div class="admin-user">
          <span>{{ user.role === 'super_admin' ? '超级管理员' : '管理员' }} · {{ user.username }}</span>
          <button class="text-button" @click="emit('logout')">退出</button>
        </div>
      </header>
      <main class="admin-main">
        <AdminPanel v-if="page === 'users'" :role="user.role" />
        <ContentPanel v-else-if="page === 'content'" />
        <TelemetryPanel v-else-if="page === 'telemetry'" />
      </main>
    </div>
  </div>
  <div v-else class="site-shell admin-login-page">
    <header class="site-header">
      <div class="header-inner">
        <a class="brand" href="/admin">海龟汤后台</a>
        <div class="header-actions"><a class="text-button" href="/">返回游戏系统</a></div>
      </div>
    </header>
    <main class="page-wrap">
      <section class="panel auth admin-login-card">
        <h1>后台登录</h1>
        <form @submit.prevent="login">
          <label>用户名<input v-model="username" autocomplete="username" required /></label>
          <label>密码<input v-model="password" type="password" autocomplete="current-password" required /></label>
          <button>登录后台</button>
        </form>
        <p v-if="error" role="alert">{{ error }}</p>
      </section>
    </main>
  </div>
</template>
