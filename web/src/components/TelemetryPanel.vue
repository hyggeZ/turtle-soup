<script setup>
// 展示有界日志统计和单次请求的阶段耗时。
import { computed, nextTick, onMounted, ref } from 'vue'
import { request } from '../api'

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
const latest = computed(() => data.value?.recent?.[0] || null)

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
  <section class="admin-panel telemetry-page">
    <div class="admin-page-heading">
      <h1>运行日志</h1>
      <button type="button" class="admin-primary-button" :disabled="loading" @click="load">刷新数据</button>
    </div>
    <p v-if="error" class="admin-feedback error" role="alert">{{ error }}</p>
    <p v-if="data?.writeError" class="admin-feedback error" role="alert">日志写入失败：{{ data.writeError }}</p>
    <template v-if="data">
      <div class="telemetry-summary">
        <article><span>日志占用</span><strong>{{ size(data.logBytes) }}</strong><small>上限 {{ size(data.maxLogBytes) }}</small></article>
        <article><span>近期请求</span><strong>{{ data.recentCount }}</strong><small>其中 WebSocket 连接 {{ data.connections }} 次</small></article>
        <article><span>近期错误</span><strong>{{ data.errorCount }}</strong><small>最近 300 次请求的 HTTP 4xx 与 5xx</small></article>
      </div>
      <div class="telemetry-panels">
        <section class="admin-section telemetry-route-section">
          <div class="admin-section-heading"><h2>接口耗时</h2><span>最近 300 次请求</span></div>
          <p v-if="!data.routes.length" class="admin-empty">还没有请求记录。</p>
          <div v-for="route in data.routes" :key="route.route" class="telemetry-route">
            <div><strong>{{ route.route }}</strong><span>{{ route.count }} 次 · 错误 {{ route.errors }} 次</span></div>
            <div class="telemetry-values">平均 {{ number(route.avgMs) }} ms · P95 {{ number(route.p95Ms) }} ms</div>
            <div class="telemetry-bar"><span :style="{ width: `${Math.max(2, route.p95Ms / maxP95 * 100)}%` }"></span></div>
          </div>
        </section>
        <section class="admin-section telemetry-stage-section">
          <div class="admin-section-heading"><h2>阶段耗时</h2><span>最近一条请求</span></div>
          <template v-if="latest">
            <div class="telemetry-stage-route">{{ latest.method }} {{ latest.route }} <span>状态 {{ latest.status }}</span></div>
            <div class="telemetry-stage"><span>会话校验</span><strong>{{ number(latest.authMs) }} ms</strong></div>
            <div class="telemetry-stage"><span>数据库查询</span><strong>{{ number(latest.dbMs) }} ms</strong></div>
            <div class="telemetry-stage"><span>总耗时</span><strong>{{ number(latest.durationMs) }} ms</strong></div>
            <p class="telemetry-note">阶段可能重叠，耗时不能直接相加。</p>
          </template>
          <p v-else class="admin-empty">还没有请求记录。</p>
        </section>
      </div>
      <section class="admin-section telemetry-request-section">
        <div class="admin-section-heading"><h2>最近请求</h2><span>最多 50 条</span></div>
        <div class="admin-table-wrap">
          <table class="simple-table telemetry-table" aria-label="最近请求">
            <thead><tr><th>时间</th><th>接口</th><th>状态</th><th>总耗时</th><th>会话校验</th><th>数据库</th><th>查询数</th><th></th></tr></thead>
            <tbody>
              <tr v-for="item in data.recent" :key="item.traceId">
                <td>{{ new Date(item.time).toLocaleString('zh-CN') }}</td>
                <td>{{ item.method }} {{ item.route }}</td>
                <td>{{ item.status }}</td>
                <td>{{ number(item.durationMs) }} ms{{ item.status === 101 ? '（连接时长）' : '' }}</td>
                <td>{{ number(item.authMs) }} ms</td>
                <td>{{ number(item.dbMs) }} ms</td>
                <td>{{ item.dbCount }}</td>
                <td><button type="button" class="admin-table-button alt" @click="openTrace(item)">查看链路</button></td>
              </tr>
            </tbody>
          </table>
        </div>
        <p v-if="!data.recent.length" class="admin-empty">还没有请求记录。</p>
      </section>
      <section class="admin-section telemetry-ai-section">
        <div class="admin-section-heading"><h2>AI 判定尝试</h2><span>近 7 天，按模型与提示词版本统计</span></div>
        <p v-if="!aiMetrics.length" class="admin-empty">还没有 AI 判定记录。</p>
        <div v-else class="admin-table-wrap">
          <table class="simple-table telemetry-table" aria-label="AI 判定尝试">
            <thead><tr><th>模型</th><th>提示词</th><th>尝试</th><th>成功</th><th>格式错误</th><th>其他失败</th><th>平均判定耗时</th><th>判定 P95</th></tr></thead>
            <tbody>
              <tr v-for="item in aiMetrics" :key="`${item.model}:${item.promptVersion}`">
                <td>{{ item.model }}</td><td>{{ item.promptVersion }}</td>
                <td>{{ item.calls }}</td><td>{{ item.successes }}</td>
                <td>{{ item.invalidOutput }}</td><td>{{ item.otherErrors }}</td>
                <td>{{ number(item.avgMs) }} ms</td><td>{{ number(item.p95Ms) }} ms</td>
              </tr>
            </tbody>
          </table>
        </div>
        <p class="telemetry-note">判定阶段耗时包含读取对局历史、模型请求和结果校验，不含最终落库。</p>
      </section>
    </template>
    <p v-else-if="loading" class="admin-empty" role="status">正在加载运行日志…</p>
    <div v-if="selected" class="admin-modal-backdrop" @click.self="closeTrace">
      <section ref="modalBox" class="admin-modal telemetry-detail" role="dialog" aria-modal="true" aria-labelledby="trace-modal-title" @keydown.esc.stop.prevent="closeTrace" @keydown.tab="trapFocus">
        <div class="admin-modal-heading"><h2 id="trace-modal-title">链路详情</h2><button type="button" class="admin-close-button" aria-label="关闭弹窗" @click="closeTrace">×</button></div>
        <p class="telemetry-trace-route">{{ selected.method }} {{ selected.route }} <span>状态 {{ selected.status }}</span></p>
        <p class="telemetry-id">Trace ID：{{ selected.traceId }}</p>
        <p class="telemetry-total">总耗时 {{ number(selected.durationMs) }} ms</p>
        <div v-for="(span, index) in selected.spans" :key="index" class="telemetry-span"><span>{{ span.name }}</span><strong>{{ number(span.durationMs) }} ms</strong></div>
        <p v-if="!selected.spans.length" class="admin-empty">本次请求没有阶段记录。</p>
        <p class="telemetry-note">部分阶段可能重叠，耗时不能直接相加。</p>
      </section>
    </div>
  </section>
</template>
