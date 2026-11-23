// 在首次绘制之后返回身份，检查首页入口位置及权限。
import { expect, test } from '@playwright/test'
import { resolve, sep } from 'node:path'
import { fileURLToPath } from 'node:url'

const origin = 'http://127.0.0.1:14289'

async function openHome(page, role) {
  const dist = resolve(process.env.ROOM_OFFLINE_WEB_DIST || fileURLToPath(new URL('../web/dist', import.meta.url)))
  let releaseMe
  const holdMe = new Promise(done => { releaseMe = done })
  await page.route('**/*', async route => {
    const { origin: requestedOrigin, pathname } = new URL(route.request().url())
    if (requestedOrigin !== origin) return route.abort()
    if (pathname === '/api/me') {
      await holdMe
      return route.fulfill(role === 'guest'
        ? { status: 401, json: { error: '请先登录' } }
        : { json: { id: 23, username: '测试账号', role, isAdmin: role === 'super_admin' } })
    }
    if (pathname.startsWith('/api/')) return route.fulfill({ status: 404, json: { error: '测试接口未声明' } })
    const file = resolve(dist, ['/', '/admin'].includes(pathname) ? 'index.html' : pathname.slice(1))
    if (!file.startsWith(dist + sep)) return route.fulfill({ status: 404 })
    return route.fulfill({ path: file })
  })

  const account = page.locator('.game-home-account')
  const publish = page.getByRole('button', { name: '发布题目', exact: true })
  const admin = page.getByRole('link', { name: '管理后台', exact: true })
  const me = page.waitForResponse(`${origin}/api/me`)
  let before
  try {
    await page.goto(origin, { waitUntil: 'domcontentloaded' })
    await expect(publish).toBeVisible()
    await page.evaluate(() => new Promise(done => requestAnimationFrame(() => requestAnimationFrame(done))))
    before = { account: await account.boundingBox(), publish: await publish.boundingBox() }
  } finally {
    releaseMe()
  }
  const response = await me
  expect(response.status()).toBe(role === 'guest' ? 401 : 200)
  const label = role === 'guest' ? '登录 / 注册' : '我的主页'
  await expect(account.getByRole('button', { name: label, exact: true })).toBeVisible()
  await expect(admin).toHaveCount(role === 'super_admin' ? 1 : 0)
  return { account, publish, admin, before }
}

function checkStable(before, after, label) {
  expect.soft(Math.abs(after.x - before.x), `${label}的水平位移`).toBeLessThanOrEqual(1)
  expect.soft(Math.abs(after.y - before.y), `${label}的垂直位移`).toBeLessThanOrEqual(1)
}

function checkDisjoint(first, second) {
  const overlapX = Math.min(first.x + first.width, second.x + second.width) - Math.max(first.x, second.x)
  const overlapY = Math.min(first.y + first.height, second.y + second.height) - Math.max(first.y, second.y)
  expect(Math.min(overlapX, overlapY), '两个入口应可独立点击').toBeLessThanOrEqual(0)
}

for (const width of [1440, 921, 320]) {
  test(`管理员首页入口稳定且可用（${width}px）`, async ({ page }) => {
    await page.setViewportSize({ width, height: 844 })
    const { account, publish, admin, before } = await openHome(page, 'super_admin')
    checkStable(before.account, await account.boundingBox(), '账号入口')
    const publishBox = await publish.boundingBox()
    checkStable(before.publish, publishBox, '发布入口')
    checkDisjoint(publishBox, await admin.boundingBox())
    checkDisjoint(await account.boundingBox(), await page.getByRole('heading', { name: '海龟汤', exact: true }).boundingBox())

    await publish.focus()
    await page.keyboard.press('Tab')
    await expect(admin).toBeFocused()
    await page.keyboard.press('Shift+Tab')
    await expect(publish).toBeFocused()
    await admin.press('Enter')
    await expect(page).toHaveURL(`${origin}/admin`)
    await expect(page.getByRole('link', { name: '进入操作审计', exact: true })).toBeVisible()
  })
}

for (const role of ['guest', 'user']) {
  test(`窄屏首页保留对应账号入口（${role}）`, async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    const { account, publish, before } = await openHome(page, role)
    checkStable(before.account, await account.boundingBox(), '账号入口')
    checkStable(before.publish, await publish.boundingBox(), '发布入口')
    checkDisjoint(await account.boundingBox(), await page.getByRole('heading', { name: '海龟汤', exact: true }).boundingBox())
  })
}
