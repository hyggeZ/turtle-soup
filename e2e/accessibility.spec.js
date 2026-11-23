// 用生产构建和合成接口检查 Chromium 计算出的可访问树与焦点。
import { expect, test } from '@playwright/test'
import { access } from 'node:fs/promises'
import { resolve, sep } from 'node:path'
import { fileURLToPath } from 'node:url'

const origin = 'http://127.0.0.1:14285'
const user = { id: 23, username: '小林', role: 'user' }
const soup = {
  id: 9, title: '没有亮灯的车站', surface: '车站停电了。', genre: '本格', difficulty: 2, horror: 1,
  authorId: 51, author: '测试作者', createdAt: '2026-01-01T00:00:00Z', likes: 0, bookmarks: 0, comments: 0,
}
const roomId = 7
const answerPath = `/api/soups/${soup.id}/answer`
const answer = '值班员关闭了备用电源。'
const passwordHint = '密码需为 8–72 字节。请使用不与其他网站重复的密码。'
const spoilerWarning = '查看后会知道完整真相，之后不能再以玩家身份参与这道题的对局。'

async function openFixture(page, mode) {
  const dist = resolve(process.env.ROOM_OFFLINE_WEB_DIST || fileURLToPath(new URL('../web/dist', import.meta.url)))
  await access(resolve(dist, 'index.html'))
  const revealed = []
  const responses = new Map([
    ['/api/me', user],
    ['/api/soups', [soup]],
    [`/api/soups/${soup.id}`, soup],
    [`/api/soups/${soup.id}/comments`, []],
    [`/api/rooms/${roomId}`, {
      id: roomId, code: 'AX0077', mode: 'casual', status: 'active', myRole: 'player', maxPlayers: 4,
      title: soup.title, surface: soup.surface, genre: soup.genre, difficulty: soup.difficulty, horror: soup.horror,
      members: [{ id: user.id, username: user.username, role: 'player', online: true }],
      events: [], eventCursor: 0, historyBefore: 0, hasMoreNew: false,
    }],
  ])
  await page.route(`${origin}/**`, async route => {
    const { pathname } = new URL(route.request().url())
    if (pathname === '/api/me' && mode === 'guest') return route.fulfill({ status: 401, json: { error: '请先登录' } })
    if (pathname === answerPath && route.request().method() === 'POST') {
      revealed.push(pathname)
      return route.fulfill({ json: { answer } })
    }
    if (responses.has(pathname)) return route.fulfill({ json: responses.get(pathname) })
    if (pathname.startsWith('/api/')) return route.fulfill({ status: 404, json: { error: '测试接口未声明' } })
    const file = resolve(dist, pathname === '/' ? 'index.html' : pathname.slice(1))
    if (!file.startsWith(dist + sep)) return route.fulfill({ status: 404 })
    return route.fulfill({ path: file })
  })
  if (mode === 'room') {
    await page.addInitScript(({ user, roomId }) => sessionStorage.setItem(`turtle_room_view_${user.id}`, String(roomId)), { user, roomId })
    // 此处需要稳定的房间快照，不向外连接 WebSocket 服务。
    await page.routeWebSocket(`ws://127.0.0.1:14285/api/rooms/${roomId}/ws`, () => {})
  }
  await page.goto(origin)
  return revealed
}

async function openLogin(page) {
  await openFixture(page, 'guest')
  await page.getByRole('button', { name: '登录 / 注册', exact: true }).click()
  await expect(page.getByRole('heading', { name: '欢迎回来' })).toBeVisible()
}

test('登录与注册的字段有精确可访问名称和注册密码说明', async ({ page }) => {
  await openLogin(page)
  await expect(page.getByRole('textbox', { name: '用户名', exact: true })).toMatchAriaSnapshot('- textbox "用户名"')
  const loginPassword = page.locator('input[autocomplete="current-password"]')
  await expect(loginPassword).toHaveAccessibleName('密码')
  await expect(loginPassword).toHaveAccessibleDescription('')
  await expect(page.getByRole('button', { name: '登录', exact: true })).toMatchAriaSnapshot('- button "登录"')

  await page.getByRole('button', { name: '创建账号', exact: true }).click()
  await expect(page.getByRole('heading', { name: '加入海龟汤' })).toBeVisible()
  const registerPassword = page.locator('input[autocomplete="new-password"]')
  // 独立报告旧版的两个语义缺口，便于回归对照。
  await expect.soft(registerPassword, '注册密码保留精确名称').toHaveAccessibleName('密码', { timeout: 1000 })
  await expect.soft(registerPassword, '密码提示单独成为可访问描述').toHaveAccessibleDescription(passwordHint, { timeout: 1000 })
})

test('从键盘切换账号模式后顺序进入用户名和密码', async ({ page }) => {
  await openLogin(page)
  const username = page.getByRole('textbox', { name: '用户名', exact: true })
  await page.getByRole('button', { name: '创建账号', exact: true }).press('Enter')
  await expect(page.getByRole('heading', { name: '加入海龟汤' })).toBeVisible()
  await expect.soft(username, '进入注册后聚焦用户名').toBeFocused({ timeout: 1000 })
  // 字段顺序独立于模式切换焦点，需在旧版也继续检查。
  await username.focus()
  await page.keyboard.press('Tab')
  await expect(page.locator('input[autocomplete="new-password"]')).toBeFocused()

  await page.getByRole('button', { name: '去登录', exact: true }).press('Enter')
  await expect(page.getByRole('heading', { name: '欢迎回来' })).toBeVisible()
  await expect.soft(username, '回到登录后聚焦用户名').toBeFocused({ timeout: 1000 })
  await username.focus()
  await page.keyboard.press('Tab')
  await expect(page.locator('input[autocomplete="current-password"]')).toBeFocused()
})

async function openSpoiler(page) {
  const revealed = await openFixture(page, 'player')
  await expect(page.getByRole('button', { name: '我的主页', exact: true })).toBeVisible()
  await page.getByRole('button', { name: /^题目社区/ }).click()
  await page.getByRole('button', { name: soup.title, exact: true }).click()
  const trigger = page.getByRole('button', { name: '查看汤底', exact: true })
  await expect(trigger).toBeVisible()
  await trigger.press('Enter')
  return { trigger, revealed, dialog: page.getByRole('dialog', { name: '确定查看汤底？', exact: true }) }
}

test('剧透弹窗用名称和可访问描述提供失去本题玩家资格的提醒', async ({ page }) => {
  const { dialog } = await openSpoiler(page)
  await expect(dialog).toMatchAriaSnapshot(`
    - dialog "确定查看汤底？":
      - heading "确定查看汤底？" [level=2]
      - button "继续推理"
      - button "确认查看"
  `)
  await expect(dialog, '剧透后果应作为弹窗的可访问描述').toHaveAccessibleDescription(spoilerWarning, { timeout: 1000 })
})

test('剧透弹窗双向循环焦点，键盘取消不请求或展示汤底', async ({ page }) => {
  const { trigger, revealed, dialog } = await openSpoiler(page)
  const cancel = dialog.getByRole('button', { name: '继续推理', exact: true })
  const confirm = dialog.getByRole('button', { name: '确认查看', exact: true })
  await expect(cancel).toBeFocused()
  await page.keyboard.press('Shift+Tab')
  await expect(confirm).toBeFocused()
  await page.keyboard.press('Tab')
  await expect(cancel).toBeFocused()
  await page.keyboard.press('Tab')
  await expect(confirm).toBeFocused()
  await page.keyboard.press('Escape')
  await expect(dialog).toHaveCount(0)
  await expect(trigger).toBeFocused()

  await page.keyboard.press('Enter')
  await expect(cancel).toBeFocused()
  await page.keyboard.press('Enter')
  await expect(dialog).toHaveCount(0)
  await expect(trigger).toBeFocused()
  await expect(page.getByText(answer, { exact: true })).toHaveCount(0)
  expect(revealed).toEqual([])

  // 主动确认作为对照，证明测试确实能够识别请求和揭露。
  await page.keyboard.press('Enter')
  await expect(cancel).toBeFocused()
  await page.keyboard.press('Tab')
  await expect(confirm).toBeFocused()
  await page.keyboard.press('Enter')
  await expect(page.getByText(answer, { exact: true })).toBeVisible()
  expect(revealed).toEqual([answerPath])
})

test('共探房间的输入和按钮在可访问树中按玩家操作命名', async ({ page }) => {
  await openFixture(page, 'room')
  await expect(page.getByRole('heading', { name: soup.title, exact: true })).toBeVisible()
  await expect(page.getByRole('textbox', { name: '提出问题', exact: true })).toMatchAriaSnapshot('- textbox "提出问题"')
  await expect(page.getByRole('button', { name: '提问', exact: true })).toMatchAriaSnapshot('- button "提问" [disabled]')
  await expect(page.getByRole('textbox', { name: '还原真相', exact: true })).toMatchAriaSnapshot('- textbox "还原真相"')
  await expect(page.getByRole('button', { name: '提交还原', exact: true })).toMatchAriaSnapshot('- button "提交还原" [disabled]')
  await expect(page.getByRole('button', { name: '申请看答案', exact: true })).toMatchAriaSnapshot('- button "申请看答案"')
})
