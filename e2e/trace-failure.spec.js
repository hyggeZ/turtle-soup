// 使用后台生产构建和合成 Trace，区分首个记录到的失败与时间线排序。
import { expect, test } from '@playwright/test'
import { resolve, sep } from 'node:path'
import { fileURLToPath } from 'node:url'

const origin = 'http://127.0.0.1:14286'
const traceId = '0123456789abcdef0123456789abcdef'
const firstLabel = '首个记录到的失败阶段'
const noFailure = '本次请求未记录失败阶段。'

async function openTrace(page, status, spans) {
  const dist = resolve(process.env.ROOM_OFFLINE_WEB_DIST || fileURLToPath(new URL('../web/dist', import.meta.url)))
  const trace = {
    traceId, status, spans, time: '2026-01-01T00:00:00Z', method: 'POST', route: '/api/ai/solo/:id/actions',
    durationMs: 11, authMs: 1, dbMs: 2, dbCount: 1,
  }
  const responses = new Map([
    ['/api/me', { id: 3, username: '测试管理员', role: 'super_admin', isAdmin: true }],
    ['/api/admin/ai-metrics', []],
    ['/api/admin/telemetry', {
      logBytes: 8192, maxLogBytes: 20_971_520, recentCount: 1, errorCount: Number(status >= 400),
      connections: 0, routes: [], recent: [trace],
    }],
  ])
  await page.route(`${origin}/**`, route => {
    const { pathname } = new URL(route.request().url())
    if (responses.has(pathname)) return route.fulfill({ json: responses.get(pathname) })
    const file = resolve(dist, pathname === '/admin' ? 'index.html' : pathname.slice(1))
    if (!file.startsWith(dist + sep)) return route.fulfill({ status: 404 })
    return route.fulfill({ path: file })
  })
  await page.goto(`${origin}/admin`)
  await page.getByRole('link', { name: /查看运行日志/ }).click()
  const row = page.getByRole('table', { name: '最近请求' }).getByRole('row')
    .filter({ has: page.getByRole('cell', { name: `POST ${trace.route}`, exact: true }) })
  await row.getByRole('button', { name: '查看链路', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '链路详情', exact: true })
  await expect(dialog).toContainText(traceId)
  return { dialog, timeline: dialog.getByRole('region', { name: '阶段时间线', exact: true }) }
}

function firstFailureStage(dialog) {
  return dialog.getByText(firstLabel, { exact: true }).locator('..').locator('strong')
}

function failureMark(timeline, stage) {
  return timeline.getByTitle(stage, { exact: true }).locator('em')
}

test('子阶段先记录失败，即使父阶段在时间线更早开始', async ({ page }) => {
  const { dialog, timeline } = await openTrace(page, 502, [
    { name: 'auth.session', startMs: 0, durationMs: 1 },
    { name: 'ai.history', startMs: 2, durationMs: 1 },
    { name: 'ai.model', startMs: 3, durationMs: 5, failed: true },
    { name: 'ai.decision', startMs: 1, durationMs: 8, failed: true },
  ])
  await expect(timeline.locator('[title]').nth(1)).toHaveAttribute('title', 'ai.decision')
  await expect(timeline.locator('[title]').nth(3)).toHaveAttribute('title', 'ai.model')
  await expect.soft(firstFailureStage(dialog), '首个记录的失败应显示模型调用').toHaveText('模型调用', { timeout: 1000 })
  await expect.soft(failureMark(timeline, 'ai.model'), '模型时间线行应突出首个失败').toHaveText('首个失败', { timeout: 1000 })
  await expect(failureMark(timeline, 'ai.decision')).toHaveText('失败')
})

test('旧链路缺少阶段起点也能从记录顺序识别数据库取连接失败', async ({ page }) => {
  const { dialog, timeline } = await openTrace(page, 503, [
    { name: 'auth.session', durationMs: 1 },
    { name: 'db.acquire', durationMs: 2, failed: true },
    { name: 'ai.decision', durationMs: 7, failed: true },
  ])
  await expect(dialog.getByText('旧记录没有阶段起点，暂时只能查看各段耗时。', { exact: true })).toBeVisible()
  await expect.soft(firstFailureStage(dialog), '旧记录也应指出第一个失败').toHaveText('数据库取连接', { timeout: 1000 })
  await expect.soft(failureMark(timeline, 'db.acquire')).toHaveText('首个失败', { timeout: 1000 })
  await expect(failureMark(timeline, 'ai.decision')).toHaveText('失败')
})

test('错误响应没有失败标记时明确说明遥测未记录阶段', async ({ page }) => {
  const { dialog } = await openTrace(page, 404, [{ name: 'auth.session', startMs: 0, durationMs: 1 }])
  await expect(dialog.getByText(firstLabel, { exact: true })).toHaveCount(0)
  await expect(dialog.getByText(noFailure, { exact: true })).toBeVisible({ timeout: 1000 })
})

test('成功且无失败记录的链路保持简洁', async ({ page }) => {
  const { dialog, timeline } = await openTrace(page, 200, [{ name: 'db.query', startMs: 2, durationMs: 3, failed: false }])
  await expect(timeline.getByTitle('db.query', { exact: true })).toBeVisible()
  await expect(dialog.getByText(firstLabel, { exact: true })).toHaveCount(0)
  await expect(dialog.getByText(noFailure, { exact: true })).toHaveCount(0)
})
