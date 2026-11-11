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
import SoloPanel from './components/SoloPanel.vue'
import AdminPortal from './components/AdminPortal.vue'

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
function openEditor(id = 0) {
  if (!canLeaveRoom()) return
  editingSoup.value = id
  notice.value = ''
  page.value = 'editor'
}
function cancelEditor() { page.value = editingSoup.value ? 'soup' : 'home' }
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
function deleted() { refresh.value++; selected.value = 0; page.value = 'home'; notice.value = '题目已删除' }
onMounted(loadUser)
</script>
<template>
  <AdminPortal v-if="adminPage" :user="user" @logged-in="user = $event" @logout="logout" />
  <div v-else class="site-shell">
    <header class="site-header">
      <div class="header-inner">
        <button class="brand" @click="openHome">海龟汤</button>
        <div class="header-actions">
          <button v-if="user" class="account-button" @click="openProfile(user.id)">@{{ user.username }}</button>
          <a v-if="user?.isAdmin" class="text-button" href="/admin" @click="followAdminLink">后台管理</a>
          <button v-if="user" class="text-button" @click="logout">退出</button>
          <button v-else class="text-button" @click="openLogin()">登录 / 注册</button>
        </div>
      </div>
    </header>
    <div class="body-layout">
      <aside class="side-nav" aria-label="页面导航">
        <button :class="{ active: page === 'home' || page === 'soup' }" @click="openHome">题目社区</button>
        <button :class="{ active: page === 'lobby' || page === 'room' }" @click="openLobby">房间大厅</button>
        <button :class="{ active: page === 'solo' }" @click="openSolo">单人模式</button>
        <button v-if="user" :class="{ active: page === 'editor' }" @click="openEditor()">发布汤题</button>
        <button v-if="user" :class="{ active: page === 'profile' }" @click="openProfile(user.id)">我的主页</button>
        <button v-if="!user" :class="{ active: page === 'auth' }" @click="openLogin()">登录 / 注册</button>
      </aside>
      <main class="page-wrap">
        <section v-if="page === 'home'" class="home-content">
          <h1>题目社区</h1>
          <p v-if="notice" class="room-notice" role="status">{{ notice }}</p>
          <SoupList :refresh="refresh" @open="openSoup" @profile="openProfile" />
        </section>
        <AuthPanel v-else-if="page === 'auth'" @logged-in="loggedIn" />
        <SoupEditor v-else-if="page === 'editor' && user" :edit-id="editingSoup" @created="created" @saved="saved" @cancel="cancelEditor" />
        <div v-else-if="page === 'soup'">
          <p v-if="notice" class="panel room-notice" role="status">{{ notice }}</p>
          <SoupDetail :id="selected" :user="user" @back="openHome" @profile="openProfile" @play="openLobbyForSoup" @solo="openSoloForSoup" @edit="openEditor" @deleted="deleted" />
        </div>
        <ProfilePanel v-else-if="page === 'profile'" :id="selected" :user="user" @back="page = 'home'" @open="openSoup" />
        <LobbyPanel v-else-if="page === 'lobby'" :user="user" :initial-soup-id="chosenSoup" :initial-code="pendingRoomCode" @room="openRoom" @login="loginForLobby" />
        <RoomPanel v-else-if="page === 'room'" :id="selected" :user="user" @back="openLobby" @hosting="hostingRoom = $event" />
        <SoloPanel v-else-if="page === 'solo'" :user="user" :initial-soup-id="chosenSoloSoup" @login="openLogin('solo')" />
        <AuthPanel v-else @logged-in="loggedIn" />
      </main>
    </div>
  </div>
</template>
