<script setup>
// 后台单独提供登录入口，页面权限以服务端角色为准。
import { ref } from 'vue'
import { send } from '../api'
import AdminPanel from './AdminPanel.vue'

defineProps({ user: { type: Object, default: null } })
const emit = defineEmits(['logged-in', 'logout'])
const username = ref('')
const password = ref('')
const error = ref('')
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
  <div class="site-shell">
    <header class="site-header"><div class="header-inner">
      <a class="brand" href="/admin">海龟汤后台</a>
      <div class="header-actions">
        <a class="text-button" href="/">游戏系统</a>
        <span v-if="user?.isAdmin">@{{ user.username }}</span>
        <button v-if="user?.isAdmin" class="text-button" @click="emit('logout')">退出</button>
      </div>
    </div></header>
    <main class="page-wrap">
      <AdminPanel v-if="user?.isAdmin" :role="user.role" />
      <section v-else class="panel auth">
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
