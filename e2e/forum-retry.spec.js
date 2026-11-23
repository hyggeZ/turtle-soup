// 用生产构建和固定时钟检查发题与评论是否遵守服务端的冷却截止时间。
import { expect, test } from '@playwright/test'
import { resolve, sep } from 'node:path'
import { fileURLToPath } from 'node:url'

const origin = 'http://127.0.0.1:14287'
const stamp = '2026-01-01T00:00:00Z'
const limit = 5_000
const serverError = '测试服务暂时限制提交'
const user = { id: 23, username: '测试玩家', role: 'user' }
const soup = {
  id: 9, title: '没有亮灯的车站', surface: '车站停电了。', genre: '本格', difficulty: 2, horror: 1,
  authorId: 51, author: '测试作者', createdAt: stamp, likes: 0, bookmarks: 0, comments: 0,
}
const otherSoup = { ...soup, id: 10, title: '空房间的钟' }
const published = { ...soup, id: 11, title: '备用灯的颜色', authorId: user.id, author: user.username }
const publishPath = '/api/soups'
const commentsPath = id => `/api/soups/${id}/comments`
const draft = { title: published.title, surface: '灯突然变了颜色。', answer: '值班员换了灯泡。' }
const commentDraft = '钟停在一个不寻常的时间。'

async function openForum(page, useRetryAfter = true) {
  const dist = resolve(process.env.ROOM_OFFLINE_WEB_DIST || fileURLToPath(new URL('../web/dist', import.meta.url)))
  const start = Date.parse(stamp)
  await page.clock.install({ time: start - 60_000 })
  await page.clock.pauseAt(start)
  // 记录浏览器同步发起的请求，之后再等合成 HTTP 响应和提交按钮退出忙碌状态。
  await page.addInitScript(() => {
    const fetch = window.fetch.bind(window)
    window.forumPostCount = 0
    window.fetch = (path, options) => {
      if (options?.method === 'POST') window.forumPostCount++
      return fetch(path, options)
    }
  })

  let elapsed = 0
  const posts = []
  const replies = new Map([
    ['/api/me', user], ['/api/soups', [soup, otherSoup]],
    ...[soup, otherSoup, published].map(item => [`/api/soups/${item.id}`, item]),
    ...[soup, otherSoup, published].map(item => [commentsPath(item.id), []]),
  ])
  await page.route(`${origin}/**`, route => {
    const req = route.request()
    const { pathname } = new URL(req.url())
    if (req.method() === 'POST' && (pathname === publishPath || pathname.endsWith('/comments'))) {
      const blocked = !posts.length || (useRetryAfter && elapsed < limit)
      posts.push({ path: pathname, elapsed, status: blocked ? 429 : 201, body: req.postDataJSON() })
      if (blocked) {
        const seconds = Math.ceil((limit - elapsed) / 1_000)
        const headers = useRetryAfter ? { 'Retry-After': String(seconds) } : undefined
        return route.fulfill({ status: 429, headers, json: { error: serverError } })
      }
      if (pathname === publishPath) return route.fulfill({ status: 201, json: { id: published.id } })
      return route.fulfill({ status: 201, body: '' })
    }
    if (replies.has(pathname)) return route.fulfill({ json: replies.get(pathname) })
    if (pathname.startsWith('/api/')) return route.fulfill({ status: 404, json: { error: '测试接口未声明' } })
    const file = resolve(dist, pathname === '/' ? 'index.html' : pathname.slice(1))
    if (!file.startsWith(dist + sep)) return route.fulfill({ status: 404 })
    return route.fulfill({ path: file })
  })
  await page.goto(origin)
  await expect(page.getByRole('button', { name: '我的主页', exact: true })).toBeVisible()
  return {
    posts,
    async runFor(ms) {
      elapsed += ms
      await page.clock.runFor(ms)
    },
  }
}

async function openPublish(page) {
  await page.getByRole('button', { name: '发布题目', exact: true }).click()
  await page.getByLabel('标题', { exact: true }).fill(draft.title)
  await page.getByLabel('汤面', { exact: true }).fill(draft.surface)
  await page.getByLabel('汤底', { exact: true }).fill(draft.answer)
  return page.locator('form').locator('button:not([type]),button[type="submit"]')
}

async function openComments(page, { fromHome = true, item = soup } = {}) {
  if (fromHome) await page.getByRole('button', { name: /^题目社区/ }).click()
  await page.getByRole('button', { name: item.title, exact: true }).click()
  const editor = page.getByRole('textbox', { name: '发表评论', exact: true })
  await editor.fill(commentDraft)
  const spoiler = page.getByRole('checkbox', { name: '这条评论包含剧透', exact: true })
  await spoiler.check()
  return { editor, spoiler, button: page.locator('form').locator('button:not([type]),button[type="submit"]') }
}

async function tryDuringCooldown(page, fixture, button, method) {
  const count = await page.evaluate(() => window.forumPostCount)
  if (method === 'click') await button.click({ force: true })
  else await button.evaluate(element => element.form.requestSubmit())
  const now = await page.evaluate(() => window.forumPostCount)
  if (now > count) {
    await expect.poll(() => fixture.posts.length).toBe(now)
    await expect(button).not.toHaveText(/正在保存|正在发表/)
  }
}

async function checkCooldown(page, fixture, button, kind) {
  await button.click()
  await expect.poll(() => fixture.posts.length).toBe(1)
  await expect(button).not.toHaveText(/正在保存|正在发表/)
  await expect.soft(page.getByText('请在 5 秒后重试', { exact: true })).toBeVisible({ timeout: 1000 })
  await expect.soft(button).toBeDisabled({ timeout: 1000 })
  for (const method of ['click', 'submit', 'click', 'submit']) await tryDuringCooldown(page, fixture, button, method)
  const extras = fixture.posts.length - 1
  console.log(`${kind}: 429 后窗口内重复请求=${extras}`)
  expect.soft(extras, '窗口内四次重复操作不应发送请求').toBe(0)
  await fixture.runFor(limit - 1)
  await expect.soft(page.getByText('请在 1 秒后重试', { exact: true })).toBeVisible({ timeout: 1000 })
  await fixture.runFor(1)
  await expect(button).toBeEnabled()
  expect(fixture.posts.length).toBe(extras + 1)
}

test('发题被限流后保留草稿，期限内零额外请求，到期仅手动重试', async ({ page }) => {
  const fixture = await openForum(page)
  const button = await openPublish(page)
  await checkCooldown(page, fixture, button, '发题')
  await expect(page.getByLabel('标题', { exact: true })).toHaveValue(draft.title)
  await expect(page.getByLabel('汤面', { exact: true })).toHaveValue(draft.surface)
  await expect(page.getByLabel('汤底', { exact: true })).toHaveValue(draft.answer)
  await button.click()
  await expect(page.getByText('题目已发布', { exact: true })).toBeVisible()
  expect(fixture.posts.at(-1)).toMatchObject({ status: 201, path: publishPath, elapsed: limit, body: draft })
})

test('评论被限流后保留正文和剧透勾选，期限内零额外请求，到期仅手动重试', async ({ page }) => {
  const fixture = await openForum(page)
  const { button, editor, spoiler } = await openComments(page)
  await checkCooldown(page, fixture, button, '评论')
  await expect(editor).toHaveValue(commentDraft)
  await expect(spoiler).toBeChecked()
  await button.click()
  await expect(editor).toHaveValue('')
  await expect(spoiler).not.toBeChecked()
  expect(fixture.posts.at(-1)).toMatchObject({ status: 201, path: commentsPath(soup.id), elapsed: limit, body: { body: commentDraft, spoiler: true } })
})

test('同一账号换题时仍处于评论冷却窗口', async ({ page }) => {
  const fixture = await openForum(page)
  const first = await openComments(page)
  await first.button.click()
  await expect.poll(() => fixture.posts.length).toBe(1)
  await expect(first.button).not.toHaveText(/正在发表/)
  await fixture.runFor(2_000)
  await page.getByRole('button', { name: '返回', exact: true }).click()
  const second = await openComments(page, { fromHome: false, item: otherSoup })
  await expect.soft(second.button).toBeDisabled({ timeout: 1000 })
  await expect.soft(page.getByText('请在 3 秒后重试', { exact: true })).toBeVisible({ timeout: 1000 })
  await tryDuringCooldown(page, fixture, second.button, 'submit')
  expect.soft(fixture.posts.map(item => item.path)).toEqual([commentsPath(soup.id)])
  await fixture.runFor(3_000)
  await expect(second.button).toBeEnabled()
  await second.button.click()
  await expect(second.editor).toHaveValue('')
  expect(fixture.posts.at(-1)).toMatchObject({ status: 201, path: commentsPath(otherSoup.id), elapsed: limit })
})

test('429 没有 Retry-After 时保持服务端提示并允许手动重试', async ({ page }) => {
  const fixture = await openForum(page, false)
  const button = await openPublish(page)
  await button.click()
  await expect(page.getByRole('alert')).toContainText(serverError)
  await expect(button).toBeEnabled()
  await expect(page.getByText(/请在 \d+ 秒后重试/)).toHaveCount(0)
  await button.click()
  await expect(page.getByText('题目已发布', { exact: true })).toBeVisible()
  expect(fixture.posts.map(item => item.status)).toEqual([429, 201])
})
