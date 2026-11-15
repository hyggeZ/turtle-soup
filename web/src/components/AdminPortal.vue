<script setup>
// 后台单独提供登录入口，页面权限以服务端角色为准。
import { computed, ref } from 'vue'
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
  { id: 'users', title: '账号管理', icon: '◎' },
  { id: 'content', title: '题目管理', icon: '◇' },
  { id: 'telemetry', title: '运行日志', icon: '▥' },
]
const page = ref('users')
const activeTitle = computed(() => pages.find(item => item.id === page.value)?.title || '')
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
      <a class="admin-brand" href="/admin"><strong>海龟汤</strong><span>管理后台</span></a>
      <nav class="admin-nav" aria-label="管理功能">
        <button
          v-for="item in pages"
          :key="item.id"
          :class="{ active: page === item.id }"
          :aria-current="page === item.id ? 'page' : null"
          @click="page = item.id"
        ><span class="admin-nav-icon" aria-hidden="true">{{ item.icon }}</span>{{ item.title }}</button>
      </nav>
      <a class="admin-back-link" href="/"><span aria-hidden="true">←</span>返回游戏系统</a>
    </aside>
    <div class="admin-workspace">
      <header class="admin-topbar">
        <div class="admin-breadcrumb"><span>海龟汤后台</span><span aria-hidden="true">／</span><strong>{{ activeTitle }}</strong></div>
        <div class="admin-user">
          <span class="admin-user-role">{{ user.role === 'super_admin' ? '超级管理员' : '管理员' }}</span>
          <span class="admin-user-avatar" aria-hidden="true">{{ user.username?.slice(0, 1).toUpperCase() }}</span>
          <strong>{{ user.username }}</strong>
          <span class="admin-user-sep" aria-hidden="true"></span>
          <button class="admin-logout" @click="emit('logout')">退出</button>
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
    <header class="admin-login-top">
      <a class="admin-login-brand" href="/admin">海龟汤后台</a>
      <a class="admin-login-back" href="/">返回游戏系统 <span aria-hidden="true">↗</span></a>
    </header>
    <main class="admin-login-main">
      <section class="admin-login-card" aria-labelledby="admin-login-title">
        <h1 id="admin-login-title">后台登录</h1>
        <form @submit.prevent="login">
          <label>用户名<input v-model="username" autocomplete="username" placeholder="输入用户名" required /></label>
          <label>密码<input v-model="password" type="password" autocomplete="current-password" placeholder="输入密码" required /></label>
          <button class="admin-primary-button">登录后台 <span aria-hidden="true">→</span></button>
        </form>
        <p v-if="error" class="admin-feedback error" role="alert">{{ error }}</p>
      </section>
    </main>
  </div>
</template>
