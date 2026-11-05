<script setup>
// 房间每两秒读取一次状态；所有动作都由后端校验权限和顺序。
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { request, send } from '../api'
const props = defineProps({ id: Number, user: { type: Object, default: null } })
const emit = defineEmits(['back'])
const room = ref(null)
const error = ref('')
const question = ref('')
const restore = ref('')
let timer
const names = computed(() => Object.fromEntries((room.value?.members || []).map(item => [item.id, item.username])))
const replied = computed(() => new Set((room.value?.events || []).filter(item => item.parentId).map(item => item.parentId)))
const myTurn = computed(() => room.value?.mode === 'casual' || room.value?.turnUserId === props.user?.id)
async function load() {
  try {
    room.value = await request(`/api/rooms/${props.id}`)
    error.value = ''
  } catch (cause) {
    error.value = cause.message
  }
}
async function act(type, values = {}) {
  try {
    await send(`/api/rooms/${props.id}/actions`, 'POST', { type, operationId: crypto.randomUUID(), ...values })
    await load()
    return true
  } catch (cause) {
    error.value = cause.message
    return false
  }
}
async function ask() {
  if (!question.value.trim()) return
  if (await act('question', { body: question.value })) question.value = ''
}
async function submitRestore() {
  if (!restore.value.trim()) return
  if (await act('restore', { body: restore.value })) restore.value = ''
}
function eventText(event) {
  if (event.kind === 'question') return event.body
  if (event.kind === 'answer') return `主持回答：${event.result}`
  if (event.kind === 'restore') return event.body || '提交了还原'
  if (event.kind === 'restore_result') return `还原判定：${event.result}`
  if (event.kind === 'answer_request') return '申请查看汤底'
  return `查看汤底：${event.result}`
}
onMounted(() => {
  load()
  timer = setInterval(load, 2000)
})
onUnmounted(() => clearInterval(timer))
watch(() => props.id, load)
</script>
<template>
  <section class="panel">
    <div class="section-head">
      <h1>房间</h1>
      <button class="text-button" @click="emit('back')">返回大厅</button>
    </div>
    <p v-if="error" role="alert">{{ error }}</p>
    <template v-if="room">
      <div class="room-summary">
        <h2>{{ room.title }}</h2>
        <p class="meta">房间码 {{ room.code }} · {{ room.mode === 'casual' ? '共探' : '竞答' }} · {{ room.status === 'waiting' ? '等待开始' : room.status === 'active' ? '进行中' : '已结束' }}</p>
        <p class="surface">{{ room.surface }}</p>
      </div>
      <h3>成员</h3>
      <div class="members">
        <span v-for="member in room.members" :key="member.id">{{ member.username }}（{{ member.role === 'host' ? '主持' : member.role === 'player' ? '玩家' : '旁观' }}{{ member.correct ? '，已答对' : '' }}）</span>
      </div>
      <div v-if="room.status === 'waiting'" class="room-controls">
        <button v-if="room.myRole !== 'host' && !room.members.find(item => item.id === user?.id)?.hostRequest" class="small-button" @click="act('host_request')">申请主持</button>
        <template v-if="room.ownerId === user?.id">
          <button v-for="member in room.members.filter(item => item.hostRequest)" :key="member.id" class="small-button" @click="act('host_approve', { userId: member.id })">同意 {{ member.username }} 主持</button>
          <button class="primary-button" @click="act('start')">开始游戏</button>
        </template>
        <button v-else-if="room.myRole === 'host'" class="primary-button" @click="act('start')">开始游戏</button>
      </div>
      <template v-if="room.status === 'active'">
        <p v-if="room.mode === 'competitive'" class="meta">当前提问：{{ names[room.turnUserId] || '无' }} · 截止 {{ room.turnDeadline ? new Date(room.turnDeadline).toLocaleTimeString('zh-CN') : '' }} <button v-if="Date.now() > new Date(room.turnDeadline).getTime()" class="text-button" @click="act('skip_turn')">跳过超时回合</button></p>
        <form v-if="room.myRole === 'player'" @submit.prevent="ask">
          <label>提问<input v-model="question" maxlength="500" :disabled="!myTurn" placeholder="向主持人提问" /></label>
          <button :disabled="!myTurn">发送问题</button>
        </form>
        <form v-if="room.myRole === 'player'" @submit.prevent="submitRestore">
          <label>还原真相<textarea v-model="restore" rows="3" maxlength="2000" /></label>
          <button>提交还原</button>
        </form>
        <button v-if="room.myRole === 'player'" class="small-button" @click="act('answer_request')">申请查看汤底</button>
        <button v-if="room.myRole === 'host' && room.mode === 'casual'" class="small-button" @click="act('finish')">结束游戏</button>
      </template>
      <div v-if="room.answer" class="answer">
        <h3>汤底</h3><p>{{ room.answer }}</p>
      </div>
      <h3>对局记录</h3>
      <p v-if="!room.events.length" class="empty-state">还没有操作记录。</p>
      <article v-for="event in room.events" :key="event.id" class="event-row">
        <strong>{{ names[event.userId] }}</strong><span>{{ eventText(event) }}</span>
        <div v-if="room.myRole === 'host' && room.status === 'active' && !replied.has(event.id)" class="event-actions">
          <template v-if="event.kind === 'question'">
            <button v-for="result in ['是','否','无关']" :key="result" @click="act('answer', { eventId: event.id, result })">{{ result }}</button>
          </template>
          <template v-if="event.kind === 'restore'">
            <button v-for="result in ['正确','错误','不完整']" :key="result" @click="act('judge', { eventId: event.id, result })">{{ result }}</button>
          </template>
          <template v-if="event.kind === 'answer_request'">
            <button v-for="result in ['同意','拒绝']" :key="result" @click="act('answer_decide', { eventId: event.id, result })">{{ result }}</button>
          </template>
        </div>
      </article>
    </template>
  </section>
</template>
