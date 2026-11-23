<script setup>
// 审计列表使用服务端游标，仅保留返回上一页所需的游标。
import { nextTick, onMounted, onUnmounted, ref } from 'vue'
import { request } from '../../api'

const emit = defineEmits(['back'])
const heading = ref(null)
const listHeading = ref(null)
const items = ref([])
const cursors = ref([0])
const nextBefore = ref(0)
const loading = ref(false)
const error = ref('')
const controller = new AbortController()
const time = new Intl.DateTimeFormat('zh-CN', {
  year: 'numeric', month: '2-digit', day: '2-digit',
  hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23',
})

const actions = {
  user_create: '创建账号',
  user_password_change: '修改密码',
  user_disable: '停用账号',
  user_enable: '启用账号',
  soup_hide: '隐藏题目',
  soup_restore: '恢复题目公开',
}
const subjects = { user: '账号', soup: '题目' }

async function load(trail = cursors.value) {
  if (loading.value) return
  const before = trail.at(-1)
  const path = before ? `/api/admin/audit?before=${before}` : '/api/admin/audit'
  loading.value = true
  error.value = ''
  try {
    const page = await request(path, { signal: controller.signal })
    const changed = trail.length !== cursors.value.length
    items.value = page.items
    nextBefore.value = page.nextBefore
    cursors.value = trail
    if (changed) {
      await nextTick()
      listHeading.value?.focus()
    }
  } catch (cause) {
    if (!controller.signal.aborted) error.value = cause.message
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  heading.value?.focus()
  load()
})
onUnmounted(() => controller.abort())
</script>

<template>
  <main class="inner-screen admin-audit-page">
    <div class="page-heading">
      <button type="button" class="back-ticket" @click="emit('back')"><span aria-hidden="true">←&nbsp;</span>返回后台</button>
      <div><h1 ref="heading" tabindex="-1">操作审计</h1></div>
    </div>
    <section class="paper audit-paper" aria-labelledby="audit-list-heading">
      <div class="paper-heading"><h2 id="audit-list-heading" ref="listHeading" tabindex="-1">操作审计记录</h2></div>
      <p v-if="error" class="admin-error" role="alert">{{ error }}</p>
      <p v-if="loading" class="admin-state" role="status">正在加载操作记录…</p>
      <p v-else-if="!items.length && !error" class="admin-state">暂无操作记录。</p>
      <div v-if="items.length" class="table-wrap">
        <table class="dossier-table audit-table" role="table" aria-labelledby="audit-list-heading" :aria-busy="loading">
          <thead role="rowgroup">
            <tr role="row"><th scope="col">时间</th><th scope="col">操作者</th><th scope="col">动作</th><th scope="col">对象</th></tr>
          </thead>
          <tbody role="rowgroup">
            <tr v-for="item in items" :key="item.id" role="row">
              <td role="cell"><time :datetime="item.createdAt">{{ time.format(new Date(item.createdAt)) }}</time></td>
              <td role="cell">{{ item.actorName ? `@${item.actorName}` : `账号 #${item.actorId}` }}</td>
              <td role="cell">{{ actions[item.action] || item.action }}</td>
              <td role="cell">{{ subjects[item.subjectType] || '对象' }} #{{ item.subjectId }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <div v-if="items.length || error || cursors.length > 1" class="paper-foot">
        <span v-if="items.length || cursors.length > 1" role="status">第 {{ cursors.length }} 页</span>
        <button v-if="!items.length && error" type="button" class="mini-button" :disabled="loading" @click="load()">重试</button>
        <nav v-if="cursors.length > 1 || nextBefore" class="audit-pagination" aria-label="操作审计分页">
          <button v-if="cursors.length > 1" type="button" class="mini-button" :disabled="loading" @click="load(cursors.slice(0, -1))">上一页</button>
          <button v-if="nextBefore" type="button" class="mini-button" :disabled="loading" @click="load([...cursors, nextBefore])">下一页</button>
        </nav>
      </div>
    </section>
  </main>
</template>

<style scoped>
.admin-ui .audit-paper { padding: 30px 36px 16px; }
.admin-ui .audit-table { min-width: 0; table-layout: fixed; }
.admin-ui .audit-table th:first-child { width: 29%; }
.admin-ui .audit-table td { height: 55px; overflow-wrap: anywhere; }
.admin-ui .audit-table td:nth-child(2) { white-space: normal; }
.audit-table time { font-variant-numeric: tabular-nums; }
.admin-audit-page :is(h1, h2):focus { outline: none; }
.audit-pagination { display: flex; flex-wrap: wrap; gap: 8px; margin-left: auto; }

@media (max-width: 620px) {
  .admin-ui .audit-paper { padding: 18px 13px 12px; }
  .audit-table, .audit-table tbody { display: block; }
  .audit-table thead { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); }
  .audit-table tbody tr {
    display: grid;
    grid-template-columns: minmax(0, 1.5fr) minmax(0, 1fr);
    gap: 5px 12px;
    padding: 12px 6px;
    border-bottom: 1px dashed #d5b29a;
  }
  .audit-table tbody tr:last-child { border-bottom: 0; }
  .admin-ui .audit-table td { display: block; height: auto; padding: 0; border: 0; font-size: 12px; }
  .audit-table td:nth-child(2) { grid-area: 2 / 1; }
  .audit-table td:nth-child(3) { grid-area: 1 / 2; font-weight: 750; }
  .audit-table td:nth-child(4) { grid-area: 2 / 2; }
  .audit-pagination .mini-button { min-height: 40px; }
}
</style>
