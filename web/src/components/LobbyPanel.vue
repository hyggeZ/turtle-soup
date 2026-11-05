<script setup>
// 大厅展示公开房间；房间码也可用于加入未公开的房间。
import { onMounted, reactive, ref } from 'vue'
import { request, send } from '../api'
const props = defineProps({ user: { type: Object, default: null } })
const emit = defineEmits(['room', 'login'])
const rooms = ref([])
const soups = ref([])
const code = ref('')
const error = ref('')
const form = reactive({ soupId: 0, mode: 'casual', maxPlayers: 6, public: true })
async function load() {
  try {
    rooms.value = await request('/api/rooms')
    soups.value = await request('/api/soups?page=1')
    error.value = ''
  } catch (cause) {
    error.value = cause.message
  }
}
async function join(roomCode) {
  if (!props.user) {
    emit('login')
    return
  }
  try {
    const room = await send('/api/rooms/join', 'POST', { code: roomCode })
    emit('room', room.id)
  } catch (cause) {
    error.value = cause.message
  }
}
async function create() {
  if (!props.user) {
    emit('login')
    return
  }
  try {
    const room = await send('/api/rooms', 'POST', form)
    emit('room', room.id)
  } catch (cause) {
    error.value = cause.message
  }
}
onMounted(load)
</script>
<template>
  <section class="panel"><div class="section-head"><h1>房间大厅</h1><button class="text-button" @click="load">刷新</button></div>
    <form class="inline-form" @submit.prevent="join(code)"><input v-model="code" placeholder="输入房间码" required /><button>加入房间</button></form>
    <p v-if="error" role="alert">{{ error }}</p><h2>公开房间</h2>
    <p v-if="!rooms.length" class="empty-state">暂无公开房间。</p>
    <article v-for="room in rooms" :key="room.id" class="room-row"><div><strong>{{ room.title }}</strong><p class="meta">{{ room.mode === 'casual' ? '共探' : '竞答' }} · {{ room.status === 'waiting' ? '等待开始' : '进行中' }} · {{ room.players }}/{{ room.maxPlayers }} 人 · {{ room.genre }} · 难度 {{ room.difficulty }}/5</p></div><button class="small-button" @click="join(room.code)">加入</button></article>
    <h2>创建房间</h2><form @submit.prevent="create"><label>题目<select v-model.number="form.soupId" required><option :value="0" disabled>选择题目</option><option v-for="soup in soups" :key="soup.id" :value="soup.id">{{ soup.title }}</option></select></label>
      <div class="form-row"><label>模式<select v-model="form.mode"><option value="casual">共探</option><option value="competitive">竞答</option></select></label><label>人数上限<input v-model.number="form.maxPlayers" type="number" min="2" max="20" /></label></div>
      <label class="check-line"><input v-model="form.public" type="checkbox" />发布到大厅</label><button>创建</button></form>
  </section>
</template>
