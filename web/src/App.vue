<script setup>
// 主页面只负责页面切换和当前登录用户，不保存论坛业务数据。
import { onMounted, ref } from 'vue'
import { request, send } from './api'
import AuthPanel from './components/AuthPanel.vue'
import SoupEditor from './components/SoupEditor.vue'
import SoupList from './components/SoupList.vue'
import SoupDetail from './components/SoupDetail.vue'
import ProfilePanel from './components/ProfilePanel.vue'

const user = ref(null)
const page = ref('home')
const selected = ref(0)
const refresh = ref(0)
async function loadUser() { try { user.value = await request('/api/me') } catch { user.value = null } }
async function logout() { await send('/api/logout', 'POST'); user.value = null; page.value = 'home'; refresh.value++ }
function openSoup(id) { selected.value = id; page.value = 'soup' }
function openProfile(id) { selected.value = id; page.value = 'profile' }
function created(id) { refresh.value++; openSoup(id) }
onMounted(loadUser)
</script>
<template>
  <div class="layout">
    <header><button class="brand" @click="page = 'home'">海龟汤</button><nav>
      <button class="text-button" @click="page = 'home'">汤题广场</button>
      <button v-if="user" class="text-button" @click="openProfile(user.id)">@{{ user.username }}</button>
      <button v-if="user" class="text-button" @click="logout">退出</button>
      <button v-else class="text-button" @click="page = 'auth'">登录 / 注册</button>
    </nav></header>
    <main>
      <div v-if="page === 'home'" class="section-head"><div><h1>汤题广场</h1><p class="muted">读汤面，猜出背后的故事。</p></div><button v-if="user" @click="page = 'editor'">发布汤题</button></div>
      <SoupList v-if="page === 'home'" :refresh="refresh" @open="openSoup" @profile="openProfile" />
      <AuthPanel v-else-if="page === 'auth'" @logged-in="user = $event; page = 'home'; refresh++" />
      <SoupEditor v-else-if="page === 'editor'" @created="created" @cancel="page = 'home'" />
      <SoupDetail v-else-if="page === 'soup'" :id="selected" :user="user" @back="page = 'home'" @profile="openProfile" />
      <ProfilePanel v-else-if="page === 'profile'" :id="selected" :user="user" @back="page = 'home'" @open="openSoup" />
    </main>
  </div>
</template>
