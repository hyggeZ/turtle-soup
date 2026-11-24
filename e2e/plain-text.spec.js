// 用无执行能力的标签形文本验证页面保留原文，也保留剧透的查看流程。
import { expect, test } from '@playwright/test'
import { resolve, sep } from 'node:path'
import { fileURLToPath } from 'node:url'

const origin = 'http://127.0.0.1:14290'
const user = { id: 23, username: 'reader', role: 'user' }
const plain = label => `${label} <strong data-plain-sentinel>原文</strong> & "引号"`
const stamp = '2026-01-01T00:00:00Z'
const soup = { id: 9, authorId: 51, author: 'author', title: plain('车站'), surface: plain('车站停电'),
  genre: '本格', difficulty: 2, horror: 1, likes: 0, bookmarks: 0, comments: 2, createdAt: stamp }
const comments = [
  { id: 31, authorId: 52, author: 'first_reader', spoiler: false, body: '', createdAt: stamp },
  { id: 30, authorId: 53, author: 'second_reader', spoiler: true, body: '', createdAt: stamp },
]
const soupPath = `/api/soups/${soup.id}`
const answerPath = `${soupPath}/answer`
const answer = plain('汤底')
const normal = { path: `${soupPath}/comments/31/spoiler`, body: plain('普通评论') }
const spoiler = { path: `${soupPath}/comments/30/spoiler`, body: plain('剧透评论') }
const forbidden = '正在参与此题对局，不能查看汤底'
const room = { id: 7, code: 'TXT756', mode: 'casual', status: 'active', myRole: 'player', maxPlayers: 4,
  title: soup.title, genre: soup.genre, difficulty: 2, horror: 1, surface: soup.surface,
  members: [{ id: user.id, username: user.username, role: 'player', online: true }],
  eventCursor: 1, historyBefore: 0, hasMoreNew: false }
const question = { id: 1, kind: 'question', userId: user.id, body: plain('车站的钟正常吗？'), createdAt: stamp }
const roomPath = `/api/rooms/${room.id}`

async function openFixture(page, mode) {
  const dist = resolve(process.env.ROOM_OFFLINE_WEB_DIST || fileURLToPath(new URL('../web/dist', import.meta.url)))
  const opened = []
  let mayReadAnswer = false
  const answers = new Map([[normal.path, { body: normal.body }], [spoiler.path, { body: spoiler.body }], [answerPath, { answer }]])
  const responses = new Map([['/api/me', user], ['/api/soups', [soup]], [soupPath, soup], [`${soupPath}/comments`, comments]])
  await page.route('**/*', route => {
    const { origin: requestOrigin, pathname, searchParams } = new URL(route.request().url())
    if (requestOrigin !== origin) return route.abort()
    if (answers.has(pathname) && route.request().method() === 'POST') {
      opened.push(pathname)
      if (mode === 'guest') return route.fulfill({ status: 401, json: { error: '请先登录' } })
      if (pathname === answerPath && !mayReadAnswer) return route.fulfill({ status: 403, json: { error: forbidden } })
      return route.fulfill({ json: answers.get(pathname) })
    }
    if (pathname === '/api/me' && mode === 'guest') return route.fulfill({ status: 401, json: { error: '请先登录' } })
    if (pathname === roomPath) return route.fulfill({ json: { ...room, events: searchParams.has('after') ? [] : [question] } })
    if (responses.has(pathname)) return route.fulfill({ json: responses.get(pathname) })
    if (pathname.startsWith('/api/')) return route.fulfill({ status: 404, json: { error: '测试接口未声明' } })
    const file = resolve(dist, pathname === '/' ? 'index.html' : pathname.slice(1))
    if (!file.startsWith(dist + sep)) return route.fulfill({ status: 404 })
    return route.fulfill({ path: file })
  })
  if (mode === 'room') {
    await page.addInitScript(({ userId, roomId }) => sessionStorage.setItem(`turtle_room_view_${userId}`, String(roomId)),
      { userId: user.id, roomId: room.id })
    await page.routeWebSocket(origin.replace('http:', 'ws:') + `${roomPath}/ws`, () => {})
  }
  await page.goto(origin)
  return { opened, allowAnswer() { mayReadAnswer = true } }
}

async function literal(scope, text) {
  await expect(scope.getByText(text, { exact: true })).toBeVisible()
  await expect(scope.locator('[data-plain-sentinel]')).toHaveCount(0)
}

async function openCommunity(page) {
  await page.getByRole('button', { name: /^题目社区/ }).click()
  return page.getByRole('button', { name: soup.title, exact: true })
}

test('社区列表和详情按原文显示汤题，访客不能展开折叠内容', async ({ page }) => {
  const { opened } = await openFixture(page, 'guest')
  const entry = await openCommunity(page)
  await literal(page, soup.title)
  await literal(page, soup.surface)
  await entry.click()
  await expect(page.getByRole('heading', { name: '题目详情' })).toBeVisible()
  await literal(page, soup.title)
  await literal(page, soup.surface)
  await expect(page.getByText('评论内容已折叠 · 登录后可查看', { exact: true })).toBeVisible()
  await expect(page.getByText('剧透评论 · 登录后可查看', { exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: /^(查看|查看汤底)$/ })).toHaveCount(0)
  for (const body of [normal.body, spoiler.body, answer]) await expect(page.getByText(body, { exact: true })).toHaveCount(0)
  expect(opened).toEqual([])
})

test('主动展开评论和汤底后仍按原文显示，拒绝时不展示内容', async ({ page }) => {
  const fixture = await openFixture(page, 'reader')
  await expect(page.getByRole('button', { name: '我的主页', exact: true })).toBeVisible()
  const entry = await openCommunity(page)
  await entry.click()
  const openNormal = page.locator('.game-comment').filter({ hasText: '@first_reader' }).getByRole('button', { name: '查看', exact: true })
  const openSpoiler = page.locator('.game-comment').filter({ hasText: '@second_reader' }).getByRole('button', { name: '查看', exact: true })
  const commentDialog = page.getByRole('dialog', { name: '确定查看评论？' })
  await openNormal.click()
  await commentDialog.getByRole('button', { name: '取消', exact: true }).click()
  await expect(page.getByText(normal.body, { exact: true })).toHaveCount(0)
  expect(fixture.opened).toEqual([])
  await openNormal.click()
  await commentDialog.getByRole('button', { name: '确认查看', exact: true }).click()
  await literal(page.locator('.game-comment-list'), normal.body)
  await openSpoiler.click()
  await commentDialog.getByRole('button', { name: '确认查看', exact: true }).click()
  await literal(page.locator('.game-comment-list'), spoiler.body)

  const openAnswer = page.getByRole('button', { name: '查看汤底', exact: true })
  const answerDialog = page.getByRole('dialog', { name: '确定查看汤底？' })
  await openAnswer.click()
  await answerDialog.getByRole('button', { name: '确认查看', exact: true }).click()
  await expect(page.getByText(forbidden, { exact: true })).toBeVisible()
  await expect(page.getByText(answer, { exact: true })).toHaveCount(0)
  fixture.allowAnswer()
  await openAnswer.click()
  await answerDialog.getByRole('button', { name: '确认查看', exact: true }).click()
  await literal(page.locator('.game-answer'), answer)
  expect(fixture.opened).toEqual([normal.path, spoiler.path, answerPath, answerPath])
})

test('房间提问按原文显示且不生成标签元素', async ({ page }) => {
  const { opened } = await openFixture(page, 'room')
  await expect(page.getByRole('heading', { name: '共探房间' })).toBeVisible()
  await literal(page.getByRole('region', { name: '对局记录' }), question.body)
  expect(opened).toEqual([])
})
