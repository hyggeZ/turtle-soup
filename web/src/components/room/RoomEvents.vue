<script setup>
// 对局记录由服务端按身份过滤；这里仅展示当前用户可看的内容。
import { computed } from 'vue'

const props = defineProps({
  room: { type: Object, required: true },
  busy: { type: Boolean, default: false },
  historyBefore: { type: Number, default: 0 },
  historyLoading: { type: Boolean, default: false },
  historyError: { type: String, default: '' },
})
const emit = defineEmits(['action', 'older'])

const names = computed(() => Object.fromEntries(props.room.members.map(item => [item.id, item.username])))
const replied = computed(() => new Set(props.room.events.filter(item => item.parentId).map(item => item.parentId)))
const players = computed(() => new Set(props.room.members.filter(item => item.role === 'player').map(item => item.id)))
const rows = computed(() => {
  // 已加载的提问与回答放在同一行，分页缺少原提问时继续显示回答。
  const questions = new Set(props.room.events.filter(item => item.kind === 'question').map(item => item.id))
  const answers = new Map(props.room.events
    .filter(item => item.kind === 'answer' && item.result && questions.has(item.parentId))
    .map(item => [item.parentId, item]))
  return props.room.events
    .filter(item => item.kind !== 'answer' || answers.get(item.parentId)?.id !== item.id)
    .map(event => ({ event, result: answers.get(event.id)?.result || event.result }))
})

function staleRestore(event) {
  return event.kind === 'restore' && !replied.value.has(event.id) && !players.value.has(event.userId)
}

function label(event) {
  if (event.kind === 'question') return event.body
  if (event.kind === 'answer') return '主持回答'
  if (event.kind === 'restore') return event.body || '提交了还原'
  if (event.kind === 'restore_result') return event.result === '已失效' ? '还原已失效' : '还原判定'
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
  <section class="play-paper play-record-card room-skin">
    <div class="play-card-heading">
      <h3 class="room-record-title room-skin">提问记录</h3>
      <span class="play-count">{{ rows.length }} 条记录</span>
    </div>
    <div v-if="room.status !== 'waiting' && (historyBefore > 0 || historyLoading || historyError)" class="play-record-history">
      <button v-if="historyBefore > 0" class="play-ticket play-ticket-small play-ticket-plain" type="button"
        :disabled="historyLoading" @click="emit('older')">{{ historyLoading ? '正在加载…' : '加载更早记录' }}</button>
      <span v-if="historyError" role="alert">{{ historyError }}</span>
    </div>
    <p v-if="!rows.length" class="play-empty">{{ historyBefore > 0 ? '这段记录暂无可见内容，可继续查看更早记录。' : '暂无可见对局记录。' }}</p>
    <div v-else class="room-record-scroll" tabindex="0" role="region" aria-label="对局记录">
      <article v-for="{ event, result } in rows" :key="event.id" class="play-record-row">
        <span class="play-record-avatar" :data-portrait="((event.subjectUserId || event.userId) - 1) % 4 + 1" aria-hidden="true">{{ (names[event.subjectUserId || event.userId] || '成员').slice(0, 1) }}</span>
        <div class="play-record-body">
          <small>{{ names[event.subjectUserId || event.userId] || '成员' }} · {{ new Date(event.createdAt).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' }) }}</small>
          <p>{{ label(event) }}</p>
          <div v-if="!room.tasks && room.myRole === 'host' && room.status === 'active' && !replied.has(event.id) && !staleRestore(event) && choices(event).length" class="play-verdict-actions">
            <button v-for="choice in choices(event)" :key="choice" class="play-ticket play-ticket-small play-ticket-plain" :disabled="busy" @click="emit('action', actionType(event.kind), { eventId: event.id, result: choice })">{{ choice }}</button>
          </div>
          <small v-if="room.myRole === 'host' && room.status === 'active' && staleRestore(event)">提交者已离席，本次还原失效</small>
        </div>
        <span v-if="result" class="play-verdict" :class="{ yes: ['是', '正确', '同意'].includes(result), no: ['否', '错误', '拒绝'].includes(result) }">{{ result }}</span>
      </article>
    </div>
  </section>
</template>
