<script setup>
// 对局记录由服务端按身份过滤；这里仅展示当前用户可看的内容。
import { computed } from 'vue'

const props = defineProps({ room: { type: Object, required: true }, busy: { type: Boolean, default: false } })
const emit = defineEmits(['action'])

const names = computed(() => Object.fromEntries(props.room.members.map(item => [item.id, item.username])))
const replied = computed(() => new Set(props.room.events.filter(item => item.parentId).map(item => item.parentId)))

function label(event) {
  if (event.kind === 'question') return event.body
  if (event.kind === 'answer') return '主持回答'
  if (event.kind === 'restore') return event.body || '提交了还原'
  if (event.kind === 'restore_result') return '还原判定'
  if (event.kind === 'answer_request') return '申请查看汤底'
  if (event.kind === 'answer_result') return '查看汤底申请'
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
  <section class="play-paper play-record-card">
    <div class="play-card-heading"><div><span class="play-eyebrow">QUESTION LOG</span><h3>提问记录</h3></div><span class="play-count">{{ room.events.length }} 条线索</span></div>
    <p v-if="!room.events.length" class="play-empty">还没有对局记录。</p>
    <article v-for="(event, index) in room.events" :key="event.id" class="play-record-row">
      <span class="play-row-number">{{ String(index + 1).padStart(2, '0') }}</span>
      <span class="play-record-avatar" :class="`play-avatar-${event.kind}`">{{ (names[event.subjectUserId || event.userId] || '成员').slice(0, 1) }}</span>
      <div class="play-record-body">
        <small>{{ names[event.subjectUserId || event.userId] || '成员' }} · {{ new Date(event.createdAt).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' }) }}</small>
        <p>{{ label(event) }}</p>
        <div v-if="room.myRole === 'host' && room.status === 'active' && !replied.has(event.id) && choices(event).length" class="play-verdict-actions">
          <button v-for="(result, choiceIndex) in choices(event)" :key="result" class="play-ticket play-ticket-small" :class="choiceIndex === 0 ? 'play-ticket-cyan' : choiceIndex === 1 ? 'play-ticket-coral' : 'play-ticket-plain'" :disabled="busy" @click="emit('action', actionType(event.kind), { eventId: event.id, result })">{{ result }}</button>
        </div>
      </div>
      <span v-if="event.result" class="play-verdict" :class="{ yes: ['是', '正确', '同意'].includes(event.result), no: ['否', '错误', '拒绝'].includes(event.result) }">{{ event.result }}</span>
    </article>
  </section>
</template>
