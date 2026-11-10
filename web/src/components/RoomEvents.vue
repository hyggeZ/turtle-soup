<script setup>
// 对局记录由服务端按身份过滤；这里仅展示当前用户可看的内容。
import { computed } from 'vue'

const props = defineProps({ room: { type: Object, required: true }, busy: { type: Boolean, default: false } })
const emit = defineEmits(['action'])

const names = computed(() => Object.fromEntries(props.room.members.map(item => [item.id, item.username])))
const replied = computed(() => new Set(props.room.events.filter(item => item.parentId).map(item => item.parentId)))

function label(event) {
  if (event.kind === 'question') return event.body
  if (event.kind === 'answer') return `主持回答：${event.result}`
  if (event.kind === 'restore') return event.body || '提交了还原'
  if (event.kind === 'restore_result') return `还原判定：${event.result}`
  if (event.kind === 'answer_request') return '申请查看汤底'
  if (event.kind === 'answer_result') return `查看汤底申请：${event.result}`
  return event.kind
}

function choices(event) {
  if (event.kind === 'question') return ['是', '否', '无关']
  if (event.kind === 'restore') return ['正确', '错误', '不完整']
  if (event.kind === 'answer_request') return ['同意', '拒绝']
  return []
}

function actionType(kind) {
  if (kind === 'question') return 'answer'
  if (kind === 'restore') return 'judge'
  return 'answer_decide'
}
</script>

<template>
  <section class="room-events">
    <div class="section-head"><h3>对局记录</h3><span class="meta">{{ room.events.length }} 条</span></div>
    <p v-if="!room.events.length" class="empty-state">还没有对局记录。</p>
    <article v-for="event in room.events" :key="event.id" class="event-row">
      <div class="event-meta">
        <strong>{{ names[event.subjectUserId || event.userId] || '成员' }}</strong>
        <time :datetime="event.createdAt">{{ new Date(event.createdAt).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' }) }}</time>
      </div>
      <div class="event-content">
        <span>{{ label(event) }}</span>
        <div v-if="room.myRole === 'host' && room.status === 'active' && !replied.has(event.id) && choices(event).length" class="event-actions">
          <button v-for="result in choices(event)" :key="result" :disabled="busy" @click="emit('action', actionType(event.kind), { eventId: event.id, result })">
            {{ result }}
          </button>
        </div>
      </div>
    </article>
  </section>
</template>
