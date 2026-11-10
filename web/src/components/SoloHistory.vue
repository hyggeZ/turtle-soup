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
  <section class="solo-history">
    <h3>推理记录</h3>
    <p v-if="!events.length" class="muted">还没有提问。</p>
    <article v-for="event in events" :key="event.id" class="solo-event">
      <div class="solo-event-head">
        <strong>{{ title(event.kind) }}</strong>
        <time :datetime="event.createdAt">{{ new Date(event.createdAt).toLocaleString('zh-CN') }}</time>
      </div>
      <p v-if="event.body" class="surface">{{ event.body }}</p>
      <p>{{ result(event.kind, event.result) }}</p>
      <small v-if="event.model" class="muted">{{ event.model }} · {{ Number(event.latencyMs || 0).toFixed(0) }} ms</small>
    </article>
  </section>
</template>
