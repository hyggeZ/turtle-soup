<script setup>
// 页面外壳管理导航与登录状态，业务操作分别放在组件中。
import { onMounted, ref } from 'vue'
import { request, send } from './api'
import AuthPanel from './components/AuthPanel.vue'
import SoupEditor from './components/SoupEditor.vue'
import SoupList from './components/SoupList.vue'
import SoupDetail from './components/SoupDetail.vue'
import ProfilePanel from './components/ProfilePanel.vue'
import LobbyPanel from './components/LobbyPanel.vue'
import RoomPanel from './components/RoomPanel.vue'
import AdminPanel from './components/AdminPanel.vue'

const user = ref(null)
const page = ref('home')
const selected = ref(0)
const refresh = ref(0)
async function loadUser() {
  try { user.value = await request('/api/me') }
  catch { user.value = null }
}
async function logout() {
  await send('/api/logout', 'POST')
  user.value = null
  page.value = 'home'
  refresh.value++
}
function openSoup(id) { selected.value = id; page.value = 'soup' }
function openProfile(id) { selected.value = id; page.value = 'profile' }
function openRoom(id) { selected.value = id; page.value = 'room' }
function created(id) { refresh.value++; openSoup(id) }
onMounted(loadUser)
</script>
<template>
  <div class="site-shell">
    <header class="site-header">
      <div class="header-inner">
        <button class="brand" @click="page = 'home'">海龟汤</button>
        <div class="header-actions">
          <button v-if="user" class="account-button" @click="openProfile(user.id)">@{{ user.username }}</button>
          <button v-if="user" class="text-button" @click="logout">退出</button>
          <button v-else class="text-button" @click="page = 'auth'">登录 / 注册</button>
        </div>
      </div>
    </header>
    <div class="body-layout">
      <aside class="side-nav" aria-label="页面导航">
      <button :class="{ active: page === 'home' || page === 'soup' }" @click="page = 'home'">题目社区</button>
      <button :class="{ active: page === 'lobby' || page === 'room' }" @click="page = 'lobby'">房间大厅</button>
      <button v-if="user" :class="{ active: page === 'editor' }" @click="page = 'editor'">发布汤题</button>
      <button v-if="user" :class="{ active: page === 'profile' }" @click="openProfile(user.id)">我的主页</button>
      <button v-if="user?.isAdmin" :class="{ active: page === 'admin' }" @click="page = 'admin'">账号管理</button>
      <button v-if="!user" :class="{ active: page === 'auth' }" @click="page = 'auth'">登录 / 注册</button>
      </aside>
      <main class="page-wrap">
      <section v-if="page === 'home'" class="home-content">
        <h1>题目社区</h1>
        <SoupList :refresh="refresh" @open="openSoup" @profile="openProfile" />
      </section>
      <AuthPanel v-else-if="page === 'auth'" @logged-in="user = $event; page = 'home'; refresh++" />
      <SoupEditor v-else-if="page === 'editor' && user" @created="created" @cancel="page = 'home'" />
      <SoupDetail v-else-if="page === 'soup'" :id="selected" :user="user" @back="page = 'home'" @profile="openProfile" />
      <ProfilePanel v-else-if="page === 'profile' && user" :id="selected" :user="user" @back="page = 'home'" @open="openSoup" />
      <LobbyPanel v-else-if="page === 'lobby'" :user="user" @room="openRoom" @login="page = 'auth'" />
      <RoomPanel v-else-if="page === 'room'" :id="selected" :user="user" @back="page = 'lobby'" />
      <AdminPanel v-else-if="page === 'admin' && user?.isAdmin" />
      <AuthPanel v-else @logged-in="user = $event; page = 'home'" />
      </main>
    </div>
  </div>
</template>
