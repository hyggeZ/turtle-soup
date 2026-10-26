<script setup>
// 账号组件只处理注册和登录，登录成功后把用户交给主页面。
import { ref } from 'vue'
import { send } from '../api'
const emit = defineEmits(['logged-in'])
const mode = ref('login')
const username = ref('')
const password = ref('')
const message = ref('')
const busy = ref(false)
async function submit() {
  busy.value = true
  message.value = ''
  try {
    const data = await send(`/api/${mode.value}`, 'POST', { username: username.value, password: password.value })
    password.value = ''
    if (mode.value === 'register') { mode.value = 'login'; message.value = '注册成功，请登录' }
    else emit('logged-in', data)
  } catch (error) { message.value = error.message }
  finally { busy.value = false }
}
</script>
<template>
  <section class="panel auth"><h2>{{ mode === 'login' ? '登录' : '注册' }}</h2>
    <form @submit.prevent="submit">
      <label>用户名<input v-model="username" autocomplete="username" required /></label>
      <label>密码<input v-model="password" type="password" :autocomplete="mode === 'login' ? 'current-password' : 'new-password'" required /></label>
      <button :disabled="busy">{{ mode === 'login' ? '登录' : '创建账号' }}</button>
    </form>
    <button class="text-button" @click="mode = mode === 'login' ? 'register' : 'login'; message = ''">{{ mode === 'login' ? '没有账号？注册' : '已有账号？登录' }}</button>
    <p class="muted">用户名 3–20 位小写字母、数字或下划线；密码至少 8 字节。</p>
    <p v-if="message" role="status">{{ message }}</p>
  </section>
</template>
