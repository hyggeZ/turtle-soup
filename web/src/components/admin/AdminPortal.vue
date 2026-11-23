<script setup>
// 后台入口负责登录和模块切换，具体操作由服务端校验权限。
import { computed, nextTick, ref } from 'vue'
import { send } from '../../api'
import AdminPanel from './AdminPanel.vue'
import AuditPanel from './AuditPanel.vue'
import ContentPanel from './ContentPanel.vue'
import TelemetryPanel from './TelemetryPanel.vue'

const props = defineProps({ user: { type: Object, default: null } })
const emit = defineEmits(['logged-in', 'logout'])
const page = ref('home')
const auditEntry = ref(null)
const username = ref('')
const password = ref('')
const error = ref('')
const loggingIn = ref(false)
const canAudit = computed(() => props.user?.role === 'super_admin')

function backFromAudit() {
  page.value = 'home'
  nextTick(() => auditEntry.value?.focus())
}

async function login() {
  if (loggingIn.value) return
  loggingIn.value = true
  error.value = ''
  try {
    const user = await send('/api/admin/login', 'POST', {
      username: username.value,
      password: password.value,
    })
    password.value = ''
    page.value = 'home'
    emit('logged-in', user)
  } catch (cause) {
    error.value = cause.message
  } finally {
    loggingIn.value = false
  }
}
</script>

<template>
  <div class="admin-ui">
    <div class="night-scene" aria-hidden="true"></div>

    <main v-if="user?.isAdmin && (page === 'home' || (page === 'audit' && !canAudit))" class="admin-home">
      <div class="home-top">
        <a class="back-ticket" href="/">← 游戏首页</a>
        <div class="identity-ticket">
          <span class="identity-dot" aria-hidden="true">{{ user.username?.slice(0, 1).toUpperCase() }}</span>
          <span><strong>{{ user.username }}</strong><small>{{ user.role === 'super_admin' ? '超级管理员' : '管理员' }}</small></span>
          <button type="button" @click="emit('logout')">退出</button>
        </div>
      </div>
      <div class="home-title">
        <p class="eyebrow">TURTLE SOUP · CONTROL ROOM</p>
        <div class="title-line"><span class="turtle-mark" aria-hidden="true"></span><h1>管理后台</h1></div>
        <p>选择一项管理工作</p>
      </div>
      <div class="module-grid" :class="{ 'has-audit': canAudit }">
        <a href="/admin" class="module-ticket module-users" @click.prevent="page = 'users'">
          <span class="module-top"><span>01 / ACCOUNTS</span><span class="module-seal">◎</span></span>
          <svg viewBox="0 0 96 96" class="module-icon" aria-hidden="true"><circle cx="48" cy="28" r="12"/><path d="M24 74c0-15 10-25 24-25s24 10 24 25"/><circle cx="19" cy="40" r="7"/><path d="M7 66c1-10 6-17 14-19"/><circle cx="77" cy="40" r="7"/><path d="M89 66c-1-10-6-17-14-19"/></svg>
          <h2>账号管理</h2><p>查看账号、创建账号</p>
          <span class="module-enter">进入账号管理 <b>→</b></span>
        </a>
        <a href="/admin" class="module-ticket module-soups" @click.prevent="page = 'content'">
          <span class="module-top"><span>02 / STORIES</span><span class="module-seal">◇</span></span>
          <svg viewBox="0 0 96 96" class="module-icon" aria-hidden="true"><path d="M14 19c13-4 25-3 34 3 9-6 21-7 34-3v53c-13-4-25-3-34 3-9-6-21-7-34-3z"/><path d="M48 22v53M24 34h14M24 46h14M58 34h14M58 46h14"/></svg>
          <h2>题目管理</h2><p>检查发布内容与公开状态</p>
          <span class="module-enter">进入题目管理 <b>→</b></span>
        </a>
        <a href="/admin" class="module-ticket module-logs" @click.prevent="page = 'telemetry'">
          <span class="module-top"><span>03 / TRACES</span><span class="module-seal">⌁</span></span>
          <svg viewBox="0 0 96 96" class="module-icon" aria-hidden="true"><path d="M11 73h74M17 62V44M30 62V29M43 62V49M56 62V20M69 62V37M82 62V27"/><circle cx="56" cy="20" r="4"/></svg>
          <h2>运行日志</h2><p>查看接口耗时与请求链路</p>
          <span class="module-enter">查看运行日志 <b>→</b></span>
        </a>
        <a v-if="canAudit" ref="auditEntry" href="/admin" class="module-ticket module-audit" aria-label="进入操作审计" @click.prevent="page = 'audit'">
          <svg viewBox="0 0 96 96" class="module-icon" aria-hidden="true"><path d="M24 16h48v64H24zM35 34h26M35 48h26M35 62h17"/></svg>
          <h2>操作审计</h2><p>查看账号与题目变更</p>
          <span class="module-enter">进入操作审计 <b aria-hidden="true">→</b></span>
        </a>
      </div>
    </main>

    <AdminPanel v-else-if="user?.isAdmin && page === 'users'" :role="user.role" @back="page = 'home'" />
    <ContentPanel v-else-if="user?.isAdmin && page === 'content'" :role="user.role" @back="page = 'home'" />
    <TelemetryPanel v-else-if="user?.isAdmin && page === 'telemetry'" @back="page = 'home'" />
    <AuditPanel v-else-if="user?.isAdmin && canAudit && page === 'audit'" @back="backFromAudit" />

    <main v-else class="admin-login">
      <a class="back-ticket" href="/">← 游戏首页</a>
      <div class="login-layout">
        <div class="login-intro">
          <p class="eyebrow">TURTLE SOUP · CONTROL ROOM</p>
          <div class="title-line"><span class="turtle-mark" aria-hidden="true"></span><h1>管理后台</h1></div>
          <span class="intro-stroke" aria-hidden="true"></span>
        </div>
        <section class="login-paper" aria-labelledby="admin-login-title">
          <div class="login-paper-head"><span class="modal-symbol key" aria-hidden="true">✳</span><div><p class="eyebrow">ADMIN ACCESS</p><h2 id="admin-login-title">后台登录</h2></div></div>
          <form class="modal-form" @submit.prevent="login">
            <label>用户名<input v-model="username" autocomplete="username" placeholder="输入用户名" required /></label>
            <label>密码<input v-model="password" type="password" autocomplete="current-password" placeholder="输入密码" required /></label>
            <p v-if="error" class="admin-error" role="alert">{{ error }}</p>
            <button type="submit" class="action-button primary" :disabled="loggingIn">{{ loggingIn ? '正在登录…' : '登录后台' }} <span aria-hidden="true">→</span></button>
          </form>
        </section>
      </div>
    </main>
  </div>
</template>
