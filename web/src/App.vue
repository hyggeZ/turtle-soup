<script setup>
// 页面外壳只负责导航和当前登录用户，论坛内容留在各自组件中。
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
function created(id) { refresh.value++; openSoup(id) }
onMounted(loadUser)
</script>

<template>
  <div class="site-shell">
    <header class="site-header">
      <div class="header-inner">
        <button class="brand" @click="page = 'home'">海龟汤</button>
        <nav class="main-nav" aria-label="主导航">
          <button :class="{ active: page === 'home' }" @click="page = 'home'">汤题广场</button>
          <button v-if="user" :class="{ active: page === 'profile' && selected === user.id }" @click="openProfile(user.id)">我的主页</button>
        </nav>
        <div class="header-actions">
          <button v-if="user" class="primary-button" @click="page = 'editor'">发布汤题</button>
          <button v-if="user" class="account-button" @click="openProfile(user.id)">@{{ user.username }}</button>
          <button v-if="user" class="text-button" @click="logout">退出</button>
          <button v-else class="primary-button" @click="page = 'auth'">登录 / 注册</button>
        </div>
      </div>
    </header>

    <main class="page-wrap">
      <section v-if="page === 'home'" class="home-content">
        <h1>汤题广场</h1>
        <SoupList :refresh="refresh" @open="openSoup" @profile="openProfile" />
      </section>

      <AuthPanel v-else-if="page === 'auth'" @logged-in="user = $event; page = 'home'; refresh++" />
      <SoupEditor v-else-if="page === 'editor'" @created="created" @cancel="page = 'home'" />
      <SoupDetail v-else-if="page === 'soup'" :id="selected" :user="user" @back="page = 'home'" @profile="openProfile" />
      <ProfilePanel v-else-if="page === 'profile'" :id="selected" :user="user" @back="page = 'home'" @open="openSoup" />
    </main>
  </div>
</template>
