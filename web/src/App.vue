<script setup>
// 页面外壳管理导航与登录状态，业务操作分别放在组件中。
import { computed, onMounted, ref } from 'vue'
import { request, send } from './api'
import AuthPanel from './components/AuthPanel.vue'
import SoupEditor from './components/SoupEditor.vue'
import SoupList from './components/SoupList.vue'
import SoupDetail from './components/SoupDetail.vue'
import ProfilePanel from './components/ProfilePanel.vue'
import LobbyPanel from './components/LobbyPanel.vue'
import RoomPanel from './components/RoomPanel.vue'
import SoloPanel from './components/SoloPanel.vue'
import AdminPortal from './components/AdminPortal.vue'
import HomePanel from './components/HomePanel.vue'

const user = ref(null)
const page = ref('home')
const selected = ref(0)
const refresh = ref(0)
const editingSoup = ref(0)
const notice = ref('')
const chosenSoup = ref(0)
const chosenSoloSoup = ref(0)
const pendingRoomCode = ref('')
const loginTarget = ref('home')
const hostingRoom = ref(false)
const adminPage = window.location.pathname.startsWith('/admin')
const banner = computed(() => ({
  community: { label: 'STORIES / 题目社区', title: '挑一个故事，从这里开始。' },
  soup: { label: 'STORY / 题目详情', title: '读完汤面，开始提问。' },
  editor: { label: 'CREATE / 发布汤题', title: '把你的故事留在这里。' },
  lobby: { label: 'ROOMS / 房间大厅', title: '和朋友一起推理。' },
  room: { label: 'ROOM / 当前房间', title: '一起寻找真相。' },
  solo: { label: 'SOLO / 单人模式', title: '一个人，也能开始推理。' },
})[page.value])

function roomViewKey(userId) { return `turtle_room_view_${userId}` }

async function loadUser() {
  try {
    user.value = await request('/api/me')
    // 刷新后恢复此账号正在看的房间，具体权限仍由服务端检查。
    if (!adminPage && page.value === 'home') {
      const roomId = Number(sessionStorage.getItem(roomViewKey(user.value.id)))
      if (Number.isSafeInteger(roomId) && roomId > 0) {
        selected.value = roomId
        page.value = 'room'
      }
    }
  }
  catch { user.value = null }
}
function canLeaveRoom() {
  if (page.value !== 'room') return true
  if (hostingRoom.value && !window.confirm('离开房间页面会暂停对局，重返房间后继续主持。确定离开吗？')) return false
  if (user.value) sessionStorage.removeItem(roomViewKey(user.value.id))
  return true
}
function openHome() { if (canLeaveRoom()) { notice.value = ''; page.value = 'home' } }
function openCommunity() { if (canLeaveRoom()) { notice.value = ''; page.value = 'community' } }
function openEditor(id = 0) {
  if (!canLeaveRoom()) return
  editingSoup.value = id
  notice.value = ''
  if (!user.value) { openLogin('editor'); return }
  page.value = 'editor'
}
function cancelEditor() { page.value = editingSoup.value ? 'soup' : 'community' }
function followAdminLink(event) { if (!canLeaveRoom()) event.preventDefault() }
async function logout() {
  if (!canLeaveRoom()) return
  await send('/api/logout', 'POST')
  if (user.value) {
    sessionStorage.removeItem(`turtle_solo_action_${user.value.id}`)
    sessionStorage.removeItem(roomViewKey(user.value.id))
    const roomPrefix = `turtle_room_action_${user.value.id}_`
    const soloPrefix = `turtle_solo_action_${user.value.id}_`
    for (let i = sessionStorage.length - 1; i >= 0; i--) {
      const key = sessionStorage.key(i)
      if (key?.startsWith(roomPrefix) || key?.startsWith(soloPrefix)) sessionStorage.removeItem(key)
    }
  }
  user.value = null
  page.value = 'home'
  notice.value = ''
  refresh.value++
}
function openSoup(id) { if (canLeaveRoom()) { selected.value = id; notice.value = ''; page.value = 'soup' } }
function openProfile(id) { if (canLeaveRoom()) { selected.value = id; page.value = 'profile' } }
function openRoom(id) {
  if (!canLeaveRoom()) return
  pendingRoomCode.value = ''
  selected.value = id
  page.value = 'room'
  if (user.value) sessionStorage.setItem(roomViewKey(user.value.id), String(id))
}
function openLobby() { if (canLeaveRoom()) { chosenSoup.value = 0; page.value = 'lobby' } }
function openLobbyForSoup(id) { if (canLeaveRoom()) { chosenSoup.value = id; page.value = 'lobby' } }
function openSolo() { if (canLeaveRoom()) { chosenSoloSoup.value = 0; page.value = 'solo' } }
function openSoloForSoup(id) { if (canLeaveRoom()) { chosenSoloSoup.value = id; page.value = 'solo' } }
function openLogin(target = 'home') { loginTarget.value = target; page.value = 'auth' }
function loginForLobby(code = '') { pendingRoomCode.value = code; openLogin('lobby') }
function loggedIn(nextUser) {
  user.value = nextUser
  page.value = loginTarget.value
  loginTarget.value = 'home'
  refresh.value++
}
function created(id) { refresh.value++; openSoup(id); notice.value = '题目已发布' }
function saved(id) { refresh.value++; openSoup(id); notice.value = '修改已保存' }
function deleted() { refresh.value++; selected.value = 0; page.value = 'community'; notice.value = '题目已删除' }
onMounted(loadUser)
</script>
<template>
  <AdminPortal v-if="adminPage" :user="user" @logged-in="user = $event" @logout="logout" />
  <div v-else class="site-shell" :class="{ 'site-shell--wide': page === 'home' || page === 'auth' }">
    <header class="site-header">
      <div class="header-inner">
        <button class="brand" @click="openHome">海龟汤</button>
        <nav class="top-nav" aria-label="主导航">
          <button :class="{ active: page === 'home' }" @click="openHome">首页</button>
          <button :class="{ active: page === 'community' || page === 'soup' || page === 'editor' || page === 'profile' }" @click="openCommunity">题目社区</button>
          <button :class="{ active: page === 'lobby' || page === 'room' }" @click="openLobby">房间大厅</button>
          <button :class="{ active: page === 'solo' }" @click="openSolo">单人模式</button>
          <a v-if="user?.isAdmin" class="top-admin-link" href="/admin" @click="followAdminLink">后台管理</a>
        </nav>
        <div class="header-actions">
          <button v-if="user" class="header-publish" @click="openEditor()">＋ 发布汤题</button>
          <button v-if="user" class="account-button" @click="openProfile(user.id)">@{{ user.username }}</button>
          <a v-if="user?.isAdmin" class="text-button" href="/admin" @click="followAdminLink">后台管理</a>
          <button v-if="user" class="text-button" @click="logout">退出</button>
          <button v-else class="text-button" @click="openLogin()">登录 / 注册</button>
        </div>
      </div>
    </header>
    <div class="body-layout">
      <aside v-if="page !== 'home' && page !== 'auth'" class="side-nav" aria-label="页面导航">
        <span class="side-nav-label">PLAY / 探索</span>
        <button :class="{ active: page === 'home' }" @click="openHome"><span aria-hidden="true">⌂</span>首页</button>
        <button :class="{ active: page === 'community' || page === 'soup' }" @click="openCommunity"><span aria-hidden="true">▤</span>题目社区</button>
        <button :class="{ active: page === 'lobby' || page === 'room' }" @click="openLobby"><span aria-hidden="true">◫</span>房间大厅</button>
        <button :class="{ active: page === 'solo' }" @click="openSolo"><span aria-hidden="true">◇</span>单人模式</button>
        <button v-if="user" :class="{ active: page === 'editor' }" @click="openEditor()"><span aria-hidden="true">＋</span>发布汤题</button>
        <button v-if="user" :class="{ active: page === 'profile' }" @click="openProfile(user.id)"><span aria-hidden="true">◎</span>我的主页</button>
      </aside>
      <main class="page-wrap">
        <div v-if="banner" class="page-banner">
          <div class="page-banner-inner">
            <span class="page-banner-label">{{ banner.label }}</span>
            <h1>{{ banner.title }}</h1>
          </div>
        </div>
        <div class="page-content" :class="{ 'page-content--full': page === 'home' || page === 'auth' || page === 'profile' }">
          <HomePanel v-if="page === 'home'" @lobby="openLobby" @solo="openSolo" @community="openCommunity" />
          <section v-else-if="page === 'community'" class="community-content">
            <p v-if="notice" class="room-notice" role="status">{{ notice }}</p>
            <SoupList :refresh="refresh" @open="openSoup" @profile="openProfile" @lobby="openLobby" @solo="openSolo" @write="openEditor" />
          </section>
          <AuthPanel v-else-if="page === 'auth'" @logged-in="loggedIn" @back="openHome" />
          <SoupEditor v-else-if="page === 'editor' && user" :edit-id="editingSoup" @created="created" @saved="saved" @cancel="cancelEditor" />
          <div v-else-if="page === 'soup'">
            <p v-if="notice" class="panel room-notice" role="status">{{ notice }}</p>
            <SoupDetail :id="selected" :user="user" @back="openCommunity" @profile="openProfile" @play="openLobbyForSoup" @solo="openSoloForSoup" @edit="openEditor" @deleted="deleted" />
          </div>
          <ProfilePanel v-else-if="page === 'profile'" :id="selected" :user="user" @back="openCommunity" @open="openSoup" @profile="openProfile" @write="openEditor" />
          <LobbyPanel v-else-if="page === 'lobby'" :user="user" :initial-soup-id="chosenSoup" :initial-code="pendingRoomCode" @room="openRoom" @login="loginForLobby" />
          <RoomPanel v-else-if="page === 'room'" :id="selected" :user="user" @back="openLobby" @hosting="hostingRoom = $event" />
          <SoloPanel v-else-if="page === 'solo'" :user="user" :initial-soup-id="chosenSoloSoup" @login="openLogin('solo')" />
          <AuthPanel v-else @logged-in="loggedIn" @back="openHome" />
        </div>
      </main>
    </div>
  </div>
</template>
