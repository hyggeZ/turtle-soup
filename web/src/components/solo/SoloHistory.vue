<script setup>
// 单人对局记录来自服务端，按事件顺序展示可回看的问答。
defineProps({ events: { type: Array, required: true } })

function title(kind) {
  if (kind === 'question') return '提问'
  if (kind === 'restore') return '还原真相'
  return '查看汤底'
}

function result(kind, value) {
  if (kind === 'question') return `AI 主持：${value}`
  if (kind === 'restore') return `判定：${value}`
  return '本局结束'
}
</script>

<template>
  <section class="play-paper play-solo-history">
    <div class="play-card-heading"><div><span class="play-eyebrow">DEDUCTION LOG</span><h3>推理记录</h3></div><span class="play-count">{{ events.length }} 条</span></div>
    <p v-if="!events.length" class="play-empty">还没有提问。</p>
    <article v-for="(event, index) in events" :key="event.id" class="play-solo-event">
      <span class="play-row-number">{{ String(index + 1).padStart(2, '0') }}</span>
      <div><small>{{ title(event.kind) }} · {{ new Date(event.createdAt).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' }) }}</small><p v-if="event.body" class="surface">{{ event.body }}</p></div>
      <span class="play-verdict" :class="{ yes: ['是', '正确'].includes(event.result), no: ['否', '错误'].includes(event.result) }">{{ event.result || result(event.kind, event.result) }}</span>
    </article>
  </section>
</template>
