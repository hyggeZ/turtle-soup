<script setup>
// 主持待办由服务端按当前角色和房间状态计算，不依赖已加载的历史页。
import { computed } from 'vue'

const props = defineProps({
  room: { type: Object, required: true },
  tasks: { type: Object, required: true },
  cursor: { type: Number, default: 0 },
  loading: { type: Boolean, default: false },
  error: { type: String, default: '' },
  busy: { type: Boolean, default: false },
})
const emit = defineEmits(['action', 'next', 'reset'])
const names = computed(() => Object.fromEntries(props.room.members.map(member => [member.id, member.username])))

function title(task) {
  if (task.kind === 'question') return '玩家提问'
  if (task.kind === 'restore') return '真相还原'
  if (task.kind === 'answer_request') return '查看汤底申请'
  return '待处理事项'
}

function choices(task) {
  if (task.kind === 'question') return ['是', '否', '无关']
  if (task.kind === 'restore') return ['正确', '错误', '不完整']
  if (task.kind === 'answer_request') return ['同意', '拒绝']
  return []
}

function actionType(task) {
  if (task.kind === 'question') return 'answer'
  if (task.kind === 'restore') return 'judge'
  return 'answer_decide'
}
</script>

<template>
  <section class="play-paper play-task-card">
    <div class="play-card-heading"><h3>主持待办</h3><span class="play-count">本页 {{ tasks.items.length }} 项</span></div>
    <p v-if="room.status === 'paused' && tasks.items.length" class="play-task-note">对局暂停，主持人重连后可继续处理。</p>
    <p v-if="!tasks.items.length" class="play-empty">本页没有待处理事项。</p>
    <article v-for="task in tasks.items" :key="task.id" class="play-record-row">
      <span class="play-record-avatar" :class="`play-avatar-${task.kind}`">{{ (names[task.userId] || '成员').slice(0, 1) }}</span>
      <div class="play-record-body">
        <small>{{ names[task.userId] || '成员' }} · {{ title(task) }} · {{ new Date(task.createdAt).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' }) }}</small>
        <p v-if="task.kind !== 'answer_request'">{{ task.body }}</p>
        <div v-if="choices(task).length" class="play-verdict-actions">
          <button v-for="(result, index) in choices(task)" :key="result" type="button" class="play-ticket play-ticket-small"
            :class="index === 0 ? 'play-ticket-cyan' : index === 1 ? 'play-ticket-coral' : 'play-ticket-plain'"
            :disabled="busy || loading || room.status !== 'active'"
            @click="emit('action', actionType(task), { eventId: task.id, result })">{{ result }}</button>
        </div>
      </div>
    </article>
    <div v-if="cursor > 0 || tasks.nextAfter > 0 || error" class="play-task-pager">
      <button v-if="cursor > 0" class="play-ticket play-ticket-small play-ticket-plain" type="button" :disabled="loading || busy" @click="emit('reset')">回到首批</button>
      <button v-if="tasks.nextAfter > 0" class="play-ticket play-ticket-small play-ticket-cyan" type="button" :disabled="loading || busy" @click="emit('next')">{{ loading ? '正在加载…' : '下一批' }}</button>
      <span v-if="loading && !tasks.nextAfter" role="status">正在读取待办…</span>
      <span v-if="error" role="alert">{{ error }}</span>
    </div>
  </section>
</template>
