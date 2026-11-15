<script setup>
// 账号组件只处理注册和登录，登录成功后把用户交给主页面。
import { ref } from 'vue'
import { send } from '../api'
const emit = defineEmits(['logged-in', 'back'])
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
  <section class="auth-scene">
    <div class="auth-sheet">
      <button type="button" class="auth-back text-button" @click="emit('back')">← 返回首页</button>
      <p class="auth-eyebrow">ACCOUNT / {{ mode === 'login' ? '登录' : '注册' }}</p>
      <h1>{{ mode === 'login' ? '欢迎回来' : '加入海龟汤' }}</h1>
      <p class="auth-intro">{{ mode === 'login' ? '登录后可以加入房间、收藏题目和发布海龟汤。' : '创建账号，开始收藏和分享你的故事。' }}</p>
      <form class="auth-form" @submit.prevent="submit">
        <label class="auth-field">用户名
          <input v-model.trim="username" autocomplete="username" placeholder="请输入用户名" required />
        </label>
        <label class="auth-field">密码
          <input v-model="password" type="password" :autocomplete="mode === 'login' ? 'current-password' : 'new-password'" placeholder="请输入密码" required />
        </label>
        <p v-if="mode === 'register'" class="auth-help">用户名 3–20 位小写字母、数字或下划线；密码至少 8 字节。</p>
        <button class="auth-submit" :disabled="busy">{{ busy ? '请稍候…' : mode === 'login' ? '登录 ↗' : '创建账号 ↗' }}</button>
      </form>
      <p v-if="message" class="auth-message" role="status">{{ message }}</p>
      <p class="auth-switch">
        {{ mode === 'login' ? '还没有账号？' : '已有账号？' }}
        <button type="button" class="text-button" @click="mode = mode === 'login' ? 'register' : 'login'; message = ''">{{ mode === 'login' ? '创建账号' : '去登录' }}</button>
      </p>
    </div>
    <div class="auth-side-copy" aria-hidden="true">一个问题，<br />一个意想不到的答案。<span></span></div>
  </section>
</template>
