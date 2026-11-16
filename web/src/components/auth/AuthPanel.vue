<script setup>
// 登录与注册共用表单；提交中的按钮禁用，避免重复发送请求。
import { ref } from 'vue'
import { send } from '../../api'

const emit = defineEmits(['logged-in', 'back'])
const mode = ref('login')
const username = ref('')
const password = ref('')
const message = ref('')
const busy = ref(false)

function switchMode() {
  mode.value = mode.value === 'login' ? 'register' : 'login'
  message.value = ''
  password.value = ''
}

async function submit() {
  if (busy.value) return
  busy.value = true
  message.value = ''
  try {
    const data = await send(`/api/${mode.value}`, 'POST', { username: username.value, password: password.value })
    password.value = ''
    if (mode.value === 'register') {
      mode.value = 'login'
      message.value = '注册成功，请登录'
    } else {
      emit('logged-in', data)
    }
  } catch (error) {
    message.value = error.message
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <main class="game-ui game-auth">
    <div class="game-auth-layout">
      <button class="game-back" @click="emit('back')"><span aria-hidden="true">←</span> 返回</button>
      <section class="game-paper-panel game-auth-paper">
        <i class="game-corner-tape" aria-hidden="true"></i>
        <span class="game-turtle-mark" aria-hidden="true"></span>
        <p class="game-eyebrow">ACCOUNT / {{ mode === 'login' ? '登录' : '注册' }}</p>
        <h1>{{ mode === 'login' ? '欢迎回来' : '加入海龟汤' }}</h1>
        <p class="game-auth-intro">{{ mode === 'login' ? '登录后可以加入房间、收藏和发布汤题。' : '创建账号后，就可以参与对局。' }}</p>
        <form class="game-auth-form" @submit.prevent="submit">
          <label class="game-field">用户名
            <input v-model.trim="username" autocomplete="username" :placeholder="mode === 'login' ? '请输入用户名' : '3–20 位小写字母、数字或下划线'" required />
          </label>
          <label class="game-field">密码
            <input v-model="password" type="password" :autocomplete="mode === 'login' ? 'current-password' : 'new-password'" :placeholder="mode === 'login' ? '请输入密码' : '至少 8 字节'" required />
            <span v-if="mode === 'register'" class="game-field-help">请使用不与其他网站重复的密码。</span>
          </label>
          <button class="game-stamp" :disabled="busy">{{ busy ? '请稍候…' : mode === 'login' ? '登录' : '创建账号' }} <span aria-hidden="true">↗</span></button>
        </form>
        <p v-if="message" class="game-auth-message" role="status">{{ message }}</p>
        <p class="game-auth-switch">{{ mode === 'login' ? '还没有账号？' : '已有账号？' }} <button class="game-text-link" @click="switchMode">{{ mode === 'login' ? '创建账号' : '去登录' }}</button></p>
      </section>
      <div class="game-auth-decoration" aria-hidden="true"><span>CASE / 001</span><p>每个谜题，<br />都有另一面。</p><i></i></div>
    </div>
  </main>
</template>
