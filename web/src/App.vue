<script setup>
// 页面只管理入口、返回和登录状态；具体玩法留在各模块。
import { onMounted, ref } from 'vue'
import { request, send } from './api'
import AdminPortal from './components/admin/AdminPortal.vue'
import AuthPanel from './components/auth/AuthPanel.vue'
import SoupDetail from './components/community/SoupDetail.vue'
import SoupEditor from './components/community/SoupEditor.vue'
import SoupList from './components/community/SoupList.vue'
import HomePanel from './components/home/HomePanel.vue'
import ProfilePanel from './components/profile/ProfilePanel.vue'
import LobbyPanel from './components/room/LobbyPanel.vue'
import RoomPanel from './components/room/RoomPanel.vue'
import ConfirmDialog from './components/shared/ConfirmDialog.vue'
import SoloPanel from './components/solo/SoloPanel.vue'

const user = ref(null)
const page = ref('home')
const history = ref([])
const soupId = ref(0)
const profileId = ref(0)
const roomId = ref(0)
const editingSoup = ref(0)
const refresh = ref(0)
const notice = ref('')
const pendingRoomCode = ref('')
const pendingRoomDraft = ref(null)
const pendingSoloSoupId = ref(0)
const loginTarget = ref('home')
const hostingRoom = ref(false)
const leaveOpen = ref(false)
const adminPage = window.location.pathname.startsWith('/admin')
let leaveAction = null

function roomViewKey(userId) { return 'turtle_room_view_' + userId }

function view() {
  return {
    page: page.value,
    soupId: soupId.value,
    profileId: profileId.value,
    roomId: roomId.value,
    editingSoup: editingSoup.value,
  }
}

function restore(saved) {
  page.value = saved.page
  soupId.value = saved.soupId || 0
  profileId.value = saved.profileId || 0
  roomId.value = saved.roomId || 0
  editingSoup.value = saved.editingSoup || 0
}

function clearRoomView() {
  if (user.value) sessionStorage.removeItem(roomViewKey(user.value.id))
}

// 主持人离开前确认；其他页面不需要知道房间的离开规则。
function afterRoom(action) {
  if (page.value === 'room' && hostingRoom.value) {
    leaveAction = action
    leaveOpen.value = true
    return
  }
  if (page.value === 'room') clearRoomView()
  action()
}

function confirmLeave() {
  const action = leaveAction
  leaveAction = null
  clearRoomView()
  action?.()
}

function cancelLeave() { leaveAction = null }

function enter(next, values = {}) {
  afterRoom(() => {
    if (page.value !== next) {
      history.value.push(view())
      if (history.value.length > 16) history.value.shift()
    }
    if (values.soupId !== undefined) soupId.value = values.soupId
    if (values.profileId !== undefined) profileId.value = values.profileId
    if (values.roomId !== undefined) roomId.value = values.roomId
    page.value = next
    notice.value = ''
  })
}

function openHome() {
  afterRoom(() => {
    history.value = []
    page.value = 'home'
    notice.value = ''
  })
}

function goBack() {
  afterRoom(() => {
    const fallback = { room: 'lobby', soup: 'community' }
    const previous = history.value.pop()
    restore(previous || { page: fallback[page.value] || 'home' })
    notice.value = ''
  })
}

async function loadUser() {
  try {
    user.value = await request('/api/me')
    // 刷新后恢复上次正在看的房间，服务端仍会检查成员权限。
    if (!adminPage && page.value === 'home') {
      const id = Number(sessionStorage.getItem(roomViewKey(user.value.id)))
      if (Number.isSafeInteger(id) && id > 0) {
        roomId.value = id
        page.value = 'room'
        history.value = [{ page: 'lobby' }]
      }
    }
  } catch {
    user.value = null
  }
}

function openCommunity() { enter('community') }
function openSoup(id) { enter('soup', { soupId: id }) }
function openProfile(id) { enter('profile', { profileId: id }) }
function openLobby() {
  pendingRoomCode.value = ''
  pendingRoomDraft.value = null
  enter('lobby')
}
function openSolo() {
  pendingSoloSoupId.value = 0
  enter('solo')
}

function openEditor(id = 0) {
  editingSoup.value = id
  if (!user.value) {
    openLogin('editor')
    return
  }
  enter('editor')
}

function openLogin(target = 'home') {
  loginTarget.value = target
  enter('auth')
}

function loginForLobby(code = '', draft = null) {
  pendingRoomCode.value = code
  pendingRoomDraft.value = draft
  openLogin('lobby')
}

function loginForSolo(id = 0) {
  pendingSoloSoupId.value = id
  openLogin('solo')
}

function loggedIn(nextUser) {
  user.value = nextUser
  const target = loginTarget.value
  if (history.value.at(-1)?.page === target) history.value.pop()
  page.value = target
  loginTarget.value = 'home'
  refresh.value++
}

async function logout() {
  afterRoom(async () => {
    await send('/api/logout', 'POST')
    if (user.value) {
      sessionStorage.removeItem('turtle_solo_action_' + user.value.id)
      clearRoomView()
      const roomPrefix = 'turtle_room_action_' + user.value.id + '_'
      const soloPrefix = 'turtle_solo_action_' + user.value.id + '_'
      for (let i = sessionStorage.length - 1; i >= 0; i--) {
        const key = sessionStorage.key(i)
        if (key?.startsWith(roomPrefix) || key?.startsWith(soloPrefix)) sessionStorage.removeItem(key)
      }
    }
    user.value = null
    page.value = 'home'
    history.value = []
    notice.value = ''
    refresh.value++
  })
}

function openRoom(id) {
  pendingRoomCode.value = ''
  pendingRoomDraft.value = null
  enter('room', { roomId: id })
  if (user.value) sessionStorage.setItem(roomViewKey(user.value.id), String(id))
}

function saved(id, message) {
  refresh.value++
  if (history.value.at(-1)?.page === 'soup') history.value.pop()
  soupId.value = id
  page.value = 'soup'
  notice.value = message
}

function deleted() {
  refresh.value++
  const previous = history.value.pop()
  restore(previous || { page: 'community' })
  notice.value = '题目已删除'
}

onMounted(loadUser)
</script>

<template>
  <AdminPortal v-if="adminPage" :user="user" @logged-in="user = $event" @logout="logout" />
  <div v-else class="game-app">
    <HomePanel v-if="page === 'home'" :user="user" @lobby="openLobby" @solo="openSolo" @community="openCommunity" @login="openLogin" @profile="openProfile(user.id)" @write="openEditor()" />
    <section v-else-if="page === 'community'" class="game-community-page">
      <p v-if="notice" class="game-notice" role="status">{{ notice }}</p>
      <SoupList :refresh="refresh" @back="goBack" @open="openSoup" @profile="openProfile" />
    </section>
    <AuthPanel v-else-if="page === 'auth'" @logged-in="loggedIn" @back="goBack" />
    <SoupEditor v-else-if="page === 'editor' && user" :edit-id="editingSoup" @created="id => saved(id, '题目已发布')" @saved="id => saved(id, '修改已保存')" @cancel="goBack" />
    <section v-else-if="page === 'soup'" class="game-detail-page">
      <p v-if="notice" class="game-notice" role="status">{{ notice }}</p>
      <SoupDetail :id="soupId" :user="user" @back="goBack" @profile="openProfile" @edit="openEditor" @deleted="deleted" />
    </section>
    <ProfilePanel v-else-if="page === 'profile'" :id="profileId" :user="user" @back="goBack" @open="openSoup" @profile="openProfile" @logout="logout" />
    <LobbyPanel v-else-if="page === 'lobby'" :user="user" :initial-code="pendingRoomCode" :initial-draft="pendingRoomDraft" @back="goBack" @room="openRoom" @login="loginForLobby" />
    <RoomPanel v-else-if="page === 'room'" :id="roomId" :user="user" @back="goBack" @hosting="hostingRoom = $event" />
    <SoloPanel v-else-if="page === 'solo'" :user="user" :initial-soup-id="pendingSoloSoupId" @back="goBack" @login="loginForSolo" />
    <AuthPanel v-else @logged-in="loggedIn" @back="openHome" />
  </div>
  <ConfirmDialog
    v-model:open="leaveOpen"
    title="离开当前对局？"
    message="房主离开后，对局会暂停。你返回房间后可以继续主持。"
    confirm-text="确认离开"
    @confirm="confirmLeave"
    @cancel="cancelLeave"
  />
</template>
