<script setup>
// 展示有界日志统计和单次请求的阶段耗时。
import { computed, onMounted, ref } from 'vue'
import { request } from '../api'

const data = ref(null)
const aiMetrics = ref([])
const selected = ref(null)
const error = ref('')
const loading = ref(false)

function number(value) { return Number(value || 0).toFixed(2) }
function size(bytes) {
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / 1024 / 1024).toFixed(2)} MB`
}
const maxP95 = computed(() => Math.max(1, ...(data.value?.routes || []).map(item => item.p95Ms)))

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
      selected.value = data.value.recent.find(item => item.traceId === selected.value.traceId) || null
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
  <section class="panel admin-panel telemetry-page">
    <div class="admin-page-heading">
      <div><p class="admin-eyebrow">服务监控</p><h1>运行日志</h1></div>
      <button class="admin-primary-button" :disabled="loading" @click="load">刷新数据</button>
    </div>
    <p v-if="error" role="alert">{{ error }}</p>
    <p v-if="data?.writeError" role="alert">日志写入失败：{{ data.writeError }}</p>
    <template v-if="data">
      <div class="telemetry-summary">
        <article><span>日志占用</span><strong>{{ size(data.logBytes) }}</strong><small>上限 {{ size(data.maxLogBytes) }}</small></article>
        <article><span>近期请求</span><strong>{{ data.recentCount }}</strong><small>其中 WebSocket 连接 {{ data.connections }} 次</small></article>
        <article><span>近期错误</span><strong>{{ data.errorCount }}</strong><small>最近 300 次请求的 HTTP 4xx 与 5xx</small></article>
      </div>
      <section class="admin-section">
        <div class="admin-section-heading"><h2>接口耗时</h2><span>最近 300 次请求，不含 WebSocket 连接时长</span></div>
        <p v-if="!data.routes.length" class="muted">还没有请求记录。</p>
        <div v-for="route in data.routes" :key="route.route" class="telemetry-route">
          <div><strong>{{ route.route }}</strong><span>{{ route.count }} 次 · 错误 {{ route.errors }} 次</span></div>
          <div class="telemetry-bar"><span :style="{ width: `${Math.max(2, route.p95Ms / maxP95 * 100)}%` }"></span></div>
          <div>平均 {{ number(route.avgMs) }} ms · P95 {{ number(route.p95Ms) }} ms</div>
        </div>
      </section>
      <section class="admin-section">
        <div class="admin-section-heading"><h2>最近请求</h2><span>最多 50 条</span></div>
        <p class="muted">会话校验可能包含数据库查询，因此阶段耗时不能直接相加。</p>
        <div class="admin-table-wrap">
          <table class="simple-table telemetry-table">
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
                <td><button class="admin-table-button" @click="selected = item">查看链路</button></td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
      <section class="admin-section">
        <div class="admin-section-heading"><h2>AI 模型调用</h2><span>近 7 天，按模型与提示词版本统计</span></div>
        <p v-if="!aiMetrics.length" class="muted">还没有模型调用记录。</p>
        <div v-else class="admin-table-wrap">
          <table class="simple-table telemetry-table">
            <thead><tr><th>模型</th><th>提示词</th><th>调用</th><th>成功</th><th>格式错误</th><th>服务错误</th><th>平均耗时</th><th>P95</th></tr></thead>
            <tbody>
              <tr v-for="item in aiMetrics" :key="`${item.model}:${item.promptVersion}`">
                <td>{{ item.model }}</td><td>{{ item.promptVersion }}</td>
                <td>{{ item.calls }}</td><td>{{ item.successes }}</td>
                <td>{{ item.invalidOutput }}</td><td>{{ item.providerError }}</td>
                <td>{{ number(item.avgMs) }} ms</td><td>{{ number(item.p95Ms) }} ms</td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
    </template>
    <div v-if="selected" class="admin-modal-backdrop" @click.self="selected = null">
      <section class="admin-modal telemetry-detail" role="dialog" aria-modal="true" aria-label="请求链路">
        <div class="admin-modal-heading"><div><p class="admin-eyebrow">请求链路</p><h2>{{ selected.method }} {{ selected.route }}</h2></div><button class="admin-close-button" aria-label="关闭弹窗" @click="selected = null">×</button></div>
        <p class="telemetry-id">Trace ID：{{ selected.traceId }}</p>
        <p>总耗时 {{ number(selected.durationMs) }} ms · 状态 {{ selected.status }}</p>
        <div v-for="(span, index) in selected.spans" :key="index" class="telemetry-span"><span>{{ span.name }}</span><strong>{{ number(span.durationMs) }} ms</strong></div>
      </section>
    </div>
  </section>
</template>
