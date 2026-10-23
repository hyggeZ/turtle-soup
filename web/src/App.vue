<script setup>
import { onMounted, ref } from 'vue'

const mode = ref('login')
const username = ref('')
const password = ref('')
const user = ref(null)
const message = ref('')
const busy = ref(false)

async function request(path, options = {}) {
  const response = await fetch(path, {
    credentials: 'same-origin',
    ...options,
  })
  if (response.status === 204) return null
  const data = await response.json()
  if (!response.ok) throw new Error(data.error || '请求失败')
  return data
}

async function loadMe() {
  try {
    user.value = await request('/api/me')
  } catch {
    user.value = null
  }
}

async function submit() {
  busy.value = true
  message.value = ''
  try {
    const path = mode.value === 'register' ? '/api/register' : '/api/login'
    const data = await request(path, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username: username.value, password: password.value }),
    })
    password.value = ''
    if (mode.value === 'register') {
      mode.value = 'login'
      message.value = `账号 ${data.username} 注册成功，请登录`
    } else {
      user.value = data
    }
  } catch (error) {
    message.value = error.message
  } finally {
    busy.value = false
  }
}

async function logout() {
  busy.value = true
  message.value = ''
  try {
    await request('/api/logout', { method: 'POST' })
    user.value = null
    message.value = '已退出登录'
  } catch (error) {
    message.value = error.message
  } finally {
    busy.value = false
  }
}

onMounted(loadMe)
</script>

<template>
  <main class="card">
    <h1>海龟汤</h1>
    <p class="intro">多人推理游戏</p>

    <section v-if="user">
      <h2>欢迎，{{ user.username }}</h2>
      <p>你的用户 ID：{{ user.id }}</p>
      <button :disabled="busy" @click="logout">退出登录</button>
    </section>

    <section v-else>
      <h2>{{ mode === 'login' ? '登录' : '注册' }}</h2>
      <form @submit.prevent="submit">
        <label for="username">用户名</label>
        <input id="username" v-model="username" autocomplete="username" required />
        <label for="password">密码</label>
        <input
          id="password"
          v-model="password"
          type="password"
          :autocomplete="mode === 'login' ? 'current-password' : 'new-password'"
          required
        />
        <button :disabled="busy">{{ mode === 'login' ? '登录' : '创建账号' }}</button>
      </form>
      <button class="link" @click="mode = mode === 'login' ? 'register' : 'login'; message = ''">
        {{ mode === 'login' ? '还没有账号？去注册' : '已有账号？去登录' }}
      </button>
      <p v-if="mode === 'register'" class="hint">用户名：3–20 位字母、数字或下划线；密码至少 8 位。</p>
    </section>

    <p v-if="message" class="message" role="status">{{ message }}</p>
  </main>
</template>
