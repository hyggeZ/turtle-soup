<script setup>
// 展示有界日志统计和单次请求的阶段耗时。
import { computed, nextTick, onMounted, ref } from 'vue'
import { request } from '../../api'

const emit = defineEmits(['back'])
const data = ref(null)
const aiMetrics = ref([])
const selected = ref(null)
const error = ref('')
const loading = ref(false)
const modalBox = ref(null)
let returnFocus = null

function number(value) { return Number(value || 0).toFixed(2) }
function size(bytes) {
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / 1024 / 1024).toFixed(2)} MB`
}
const maxP95 = computed(() => Math.max(1, ...(data.value?.routes || []).map(item => item.p95Ms)))
// 后台刷新本身也会写入请求日志，阶段卡优先展示实际业务请求。
const latest = computed(() => {
  const recent = data.value?.recent || []
  return recent.find(item => !item.route?.startsWith('/api/admin/telemetry') && item.route !== '/api/admin/ai-metrics') || recent[0] || null
})
const latestStages = computed(() => {
  const item = latest.value
  if (!item) return []
  const ai = item.spans?.filter(span => span.name.startsWith('ai.')) || []
  if (ai.length) return ai.map(span => ({ name: stageName(span.name), key: span.name, ms: span.durationMs }))
  return [
    { name: '会话校验', key: 'auth.session', ms: item.authMs },
    { name: '数据库查询', key: 'db.query', ms: item.dbMs },
  ]
})
const aiTrace = computed(() => selected.value?.spans?.some(span => span.name.startsWith('ai.')) || false)
function stageName(key) {
  return {
    'ai.history': '历史读取',
    'ai.model': '模型调用',
    'ai.validate': '结果校验',
    'ai.persist': '结果保存',
  }[key] || key
}
function percent(value, total) {
  if (!total || !value) return 0
  return Math.min(100, Math.max(2, Number(value) / Number(total) * 100))
}

// 链路弹窗打开后移入焦点，关闭时归还给原按钮。
async function openTrace(item) {
  returnFocus = document.activeElement
  selected.value = item
  await nextTick()
  modalBox.value?.querySelector('button')?.focus()
}

async function closeTrace() {
  selected.value = null
  await nextTick()
  if (returnFocus?.isConnected) returnFocus.focus()
  returnFocus = null
}

function trapFocus(event) {
  event.preventDefault()
  modalBox.value?.querySelector('button')?.focus()
}

async function load() {
  loading.value = true
  try {
    const [trace, ai] = await Promise.all([
      request('/api/admin/telemetry'),
      request('/api/admin/ai-metrics'),
    ])
    data.value = trace
    aiMetrics.value = ai
    error.value = ''
    if (selected.value) {
      const current = data.value.recent.find(item => item.traceId === selected.value.traceId)
      if (current) selected.value = current
      else closeTrace()
    }
  } catch (cause) {
    error.value = cause.message
  } finally {
    loading.value = false
  }
}
onMounted(load)
</script>
<template>
  <main class="inner-screen admin-logs-page">
    <div class="page-heading">
      <button type="button" class="back-ticket" @click="emit('back')">← 返回后台</button>
      <div><p class="eyebrow">03 / TRACES</p><h1>运行日志</h1></div>
      <button type="button" class="action-button primary" :disabled="loading" @click="load">{{ loading ? '正在刷新…' : '刷新数据 ↻' }}</button>
    </div>
    <p v-if="error" class="admin-error" role="alert">{{ error }}</p>
    <p v-if="data?.writeError" class="admin-error" role="alert">日志写入失败：{{ data.writeError }}</p>
    <p v-if="loading && !data" class="admin-state on-night" role="status">正在加载运行日志…</p>
    <template v-if="data">
      <div class="metrics-grid">
        <article class="metric-ticket"><span class="metric-icon" aria-hidden="true">▥</span><span class="metric-label">日志占用</span><strong>{{ size(data.logBytes) }}</strong><div class="meter"><span :style="{ width: `${percent(data.logBytes, data.maxLogBytes)}%` }"></span></div><small>容量上限 {{ size(data.maxLogBytes) }}</small></article>
        <article class="metric-ticket"><span class="metric-icon cyan" aria-hidden="true">⌁</span><span class="metric-label">近期请求</span><strong>{{ data.recentCount }} <small>次</small></strong><div class="metric-rule"></div><small>其中 WebSocket 连接 {{ data.connections }} 次</small></article>
        <article class="metric-ticket"><span class="metric-icon red" aria-hidden="true">!</span><span class="metric-label">近期错误</span><strong>{{ data.errorCount }} <small>次</small></strong><div class="metric-rule"></div><small>最近 300 次请求中的 HTTP 4xx / 5xx</small></article>
      </div>
      <div class="log-grid">
        <section class="paper log-card">
          <div class="log-head"><span class="section-number">01</span><div><h2>接口耗时</h2><small>最近 300 次请求</small></div></div>
          <p v-if="!data.routes.length" class="admin-state">还没有请求记录。</p>
          <div v-for="route in data.routes" :key="route.route" class="route-row">
            <div><strong>{{ route.route }}</strong><small>{{ route.count }} 次 · 错误 {{ route.errors }} 次 · 平均 {{ number(route.avgMs) }} ms</small></div>
            <span class="route-bar"><i :style="{ width: `${percent(route.p95Ms, maxP95)}%` }"></i></span>
            <b>P95&nbsp; {{ number(route.p95Ms) }} ms</b>
          </div>
        </section>
        <section class="paper log-card stages-card">
          <div class="log-head"><span class="section-number cyan-bg">02</span><div><h2>阶段耗时</h2><small>最近一条请求</small></div></div>
          <template v-if="latest">
            <div class="stage-route">{{ latest.method }} {{ latest.route }} <span>{{ latest.status }}</span></div>
            <div v-for="stage in latestStages" :key="stage.key" class="stage-row"><span :title="stage.key">{{ stage.name }}</span><span class="stage-track"><i :style="{ width: `${percent(stage.ms, latest.durationMs)}%` }"></i></span><b>{{ number(stage.ms) }} ms</b></div>
            <div class="stage-row"><span>总耗时</span><span class="stage-track"><i style="width:100%"></i></span><b>{{ number(latest.durationMs) }} ms</b></div>
            <p class="subtle-note">阶段可能重叠，耗时不能直接相加。</p>
          </template>
          <p v-else class="admin-state">还没有请求记录。</p>
        </section>
      </div>
      <section class="paper recent-card">
        <div class="paper-heading"><div><span class="section-number">03</span><h2>最近请求</h2><small>最多 50 条</small></div></div>
        <div class="table-wrap"><table class="dossier-table trace-table" aria-label="最近请求">
          <thead><tr><th>时间</th><th>接口</th><th>状态</th><th>总耗时</th><th>会话</th><th>数据库</th><th>查询数</th><th>操作</th></tr></thead>
          <tbody><tr v-for="item in data.recent" :key="item.traceId">
            <td>{{ new Date(item.time).toLocaleString('zh-CN') }}</td><td>{{ item.method }} {{ item.route }}</td><td><span class="status" :class="{ hidden: item.status >= 400 }"><i aria-hidden="true"></i>{{ item.status }}</span></td>
            <td>{{ number(item.durationMs) }} ms{{ item.status === 101 ? '（连接时长）' : '' }}</td><td>{{ number(item.authMs) }} ms</td><td>{{ number(item.dbMs) }} ms</td><td>{{ item.dbCount }}</td>
            <td><button type="button" class="mini-button" @click="openTrace(item)">查看链路</button></td>
          </tr></tbody>
        </table></div>
        <p v-if="!data.recent.length" class="admin-state">还没有请求记录。</p>
      </section>
      <section class="paper recent-card ai-metrics-card">
        <div class="paper-heading"><div><span class="section-number cyan-bg">04</span><h2>AI 判定尝试</h2><small>近 7 天 · 按模型与提示词版本统计</small></div></div>
        <div v-if="aiMetrics.length" class="table-wrap"><table class="dossier-table" aria-label="AI 判定尝试">
          <thead><tr><th>模型</th><th>提示词</th><th>尝试</th><th>成功</th><th>格式错误</th><th>其他失败</th><th>平均判定耗时</th><th>判定 P95</th></tr></thead>
          <tbody><tr v-for="item in aiMetrics" :key="`${item.model}:${item.promptVersion}`"><td><strong>{{ item.model }}</strong></td><td>{{ item.promptVersion }}</td><td>{{ item.calls }}</td><td>{{ item.successes }}</td><td>{{ item.invalidOutput }}</td><td>{{ item.otherErrors }}</td><td>{{ number(item.avgMs) }} ms</td><td>{{ number(item.p95Ms) }} ms</td></tr></tbody>
        </table></div>
        <p v-else class="admin-state">还没有 AI 判定记录。</p>
        <p class="subtle-note">判定耗时包含历史读取、模型请求和结果校验，不含最终落库。</p>
      </section>
    </template>

    <div v-if="selected" class="modal-layer admin-open" @click.self="closeTrace">
      <section ref="modalBox" class="modal-paper trace-paper" role="dialog" aria-modal="true" aria-labelledby="trace-modal-title" @keydown.esc.stop.prevent="closeTrace" @keydown.tab="trapFocus">
        <div class="modal-heading"><span class="modal-symbol cyan" aria-hidden="true">⌁</span><div><p class="eyebrow">{{ aiTrace ? 'TRACE / AI REQUEST' : 'TRACE / REQUEST' }}</p><h2 id="trace-modal-title">链路详情</h2></div><button type="button" class="modal-close" aria-label="关闭弹窗" @click="closeTrace">×</button></div>
        <div class="trace-content">
          <div class="trace-route"><strong>{{ selected.method }} {{ selected.route }}</strong><span class="status" :class="{ hidden: selected.status >= 400 }"><i aria-hidden="true"></i>{{ selected.status }}</span></div>
          <p class="trace-id">Trace ID&nbsp; {{ selected.traceId }}</p>
          <div class="trace-total"><span>总耗时</span><strong>{{ number(selected.durationMs) }} <small>ms</small></strong></div>
          <div v-if="selected.spans?.length" class="trace-timeline" :class="{ 'ai-timeline': aiTrace }"><div v-for="(span, index) in selected.spans" :key="index"><i class="line-dot" aria-hidden="true"></i><span>{{ span.name }}</span><em>{{ number(span.durationMs) }} ms</em><b :style="{ width: `${percent(span.durationMs, selected.durationMs)}%` }"></b></div></div>
          <p v-else class="admin-state">本次请求没有阶段记录。</p>
          <p class="subtle-note">阶段可能重叠，耗时不能直接相加。</p>
        </div>
      </section>
    </div>
  </main>
</template>
