<script setup>
// 大厅展示公开房间和我已加入的房间；房间码可以进入未公开的房间。
import { onUnmounted, reactive, ref, watch } from 'vue'
import { request, send } from '../api'
import SoupPicker from './SoupPicker.vue'

const props = defineProps({ user: { type: Object, default: null }, initialSoupId: { type: Number, default: 0 }, initialCode: { type: String, default: '' } })
const emit = defineEmits(['room', 'login'])
const rooms = ref([])
const mine = ref([])
const code = ref(props.initialCode)
const error = ref('')
const loading = ref(false)
const loadingMore = ref(false)
const hasMoreRooms = ref(false)
const moreError = ref('')
const busy = ref(false)
const form = reactive({ soupId: props.initialSoupId, mode: 'casual', maxPlayers: 6, public: true, hostSelf: true })
let loadVersion = 0

async function load() {
  const version = ++loadVersion
  const userId = props.user?.id
  loading.value = true
  loadingMore.value = false
  hasMoreRooms.value = false
  moreError.value = ''
  rooms.value = []
  mine.value = []
  try {
    const publicRooms = await request('/api/rooms')
    const ownRooms = userId ? await request('/api/rooms/mine') : []
    if (version !== loadVersion || props.user?.id !== userId) return
    rooms.value = publicRooms
    hasMoreRooms.value = publicRooms.length === 50
    mine.value = ownRooms
    error.value = ''
  } catch (cause) {
    if (version === loadVersion) error.value = cause.message
  } finally {
    if (version === loadVersion) loading.value = false
  }
}

// 公开房间按末条 ID 加载更早的页面，刷新会重新从第一页开始。
async function loadMore() {
  if (loading.value || loadingMore.value || !hasMoreRooms.value || !rooms.value.length) return
  const version = loadVersion
  const before = rooms.value.at(-1).id
  loadingMore.value = true
  moreError.value = ''
  try {
    const next = await request(`/api/rooms?before=${before}`)
    if (version !== loadVersion) return
    rooms.value = [...rooms.value, ...next]
    hasMoreRooms.value = next.length === 50
  } catch (cause) {
    if (version === loadVersion) moreError.value = cause.message
  } finally {
    if (version === loadVersion) loadingMore.value = false
  }
}

async function join(roomCode) {
  if (!props.user) return emit('login', roomCode)
  if (busy.value) return
  busy.value = true
  try {
    const room = await send('/api/rooms/join', 'POST', { code: roomCode.trim().toUpperCase() })
    emit('room', room.id)
  } catch (cause) {
    error.value = cause.message
  } finally {
    busy.value = false
  }
}

async function create() {
  if (!props.user) return emit('login')
  if (busy.value || !form.soupId) return
  busy.value = true
  try {
    const room = await send('/api/rooms', 'POST', { ...form })
    emit('room', room.id)
  } catch (cause) {
    error.value = cause.message
  } finally {
    busy.value = false
  }
}

watch(() => props.user?.id, load, { immediate: true })
onUnmounted(() => { loadVersion++ })
</script>

<template>
  <section class="panel lobby-page">
    <div class="section-head">
      <h1>房间大厅</h1>
      <button class="text-button" :disabled="loading" @click="load">刷新</button>
    </div>
    <p v-if="error" role="alert">{{ error }}</p>
    <form class="inline-form" @submit.prevent="join(code)">
      <input v-model="code" maxlength="32" placeholder="输入房间码" aria-label="房间码" required />
      <button :disabled="busy || !code.trim()">加入房间</button>
    </form>

    <section v-if="user" class="lobby-section">
      <h2>我的房间</h2>
      <p v-if="!mine.length && !loading" class="empty-state">暂无房间。</p>
      <article v-for="room in mine" :key="room.id" class="room-row">
        <div>
          <strong>{{ room.title }}</strong>
          <p class="meta">
            {{ room.mode === 'casual' ? '共探' : '竞答' }} ·
            {{ room.status === 'waiting' ? '等待开始' : room.status === 'paused' ? '已暂停' : room.status === 'closed' ? '已关闭' : '进行中' }} ·
            {{ room.myRole === 'host' ? '主持' : room.myRole === 'player' ? '玩家' : '旁观' }} ·
            玩家 {{ room.players }}/{{ room.maxPlayers }} · {{ room.public ? '公开' : '私密' }}
          </p>
        </div>
        <button class="small-button" @click="emit('room', room.id)">{{ room.status === 'closed' ? '查看' : '进入' }}</button>
      </article>
    </section>

    <section class="lobby-section">
      <h2>公开房间</h2>
      <p v-if="!rooms.length && !loading" class="empty-state">暂无公开房间。</p>
      <article v-for="room in rooms" :key="room.id" class="room-row">
        <div>
          <strong>{{ room.title }}</strong>
          <p class="meta">
            {{ room.mode === 'casual' ? '共探' : '竞答' }} · {{ room.status === 'waiting' ? '等待开始' : '进行中' }} ·
            玩家 {{ room.players }}/{{ room.maxPlayers }} · {{ room.genre }} · 难度 {{ room.difficulty }}/5 · 恐怖 {{ room.horror }}/5
          </p>
        </div>
        <button class="small-button" :disabled="busy" @click="join(room.code)">{{ room.status === 'active' && room.mode === 'competitive' ? '旁观' : '进入' }}</button>
      </article>
      <p v-if="moreError" role="alert">{{ moreError }}</p>
      <button v-if="hasMoreRooms" class="small-button" :disabled="loadingMore" @click="loadMore">{{ loadingMore ? '正在加载…' : moreError ? '重试加载更多' : '加载更多房间' }}</button>
    </section>

    <section class="lobby-section">
      <h2>创建房间</h2>
      <form class="lobby-create-form" @submit.prevent="create">
        <SoupPicker v-model="form.soupId" />
        <div class="form-row">
          <label>玩法
            <select v-model="form.mode">
              <option value="casual">共探</option>
              <option value="competitive">竞答</option>
            </select>
          </label>
          <label>玩家人数上限
            <input v-model.number="form.maxPlayers" type="number" min="2" max="20" required />
          </label>
        </div>
        <label class="check-line"><input v-model="form.hostSelf" type="checkbox" />我来主持</label>
        <label class="check-line"><input v-model="form.public" type="checkbox" />在大厅公开</label>
        <p v-if="!form.hostSelf" class="meta">你将以玩家身份入场，等待其他人申请主持。</p>
        <button :disabled="busy || !form.soupId">创建房间</button>
      </form>
    </section>
  </section>
</template>
