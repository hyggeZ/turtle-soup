import { randomBytes } from 'node:crypto'
import { expect, test as base } from '@playwright/test'
import { modelQuestion } from './qwen.js'

const playerPassword = 'only-local-e2e-2026'

// 每个角色使用独立的 Cookie 和会话存储，避免身份和房间恢复状态串用。
const test = base.extend({
  actors: async ({ page, browser, baseURL, viewport }, use) => {
    const pages = []
    try {
      for (let i = 0; i < 3; i++) {
        pages.push(await browser.newPage({ baseURL, viewport }))
      }
      const [player, solo, admin] = pages
      await use({ author: page, player, solo, admin })
    } finally {
      await Promise.all(pages.map(page => page.close()))
    }
  },
})

async function registerAndLogin(page, username) {
  await page.goto('/')
  await page.getByRole('button', { name: '登录 / 注册' }).click()
  await page.getByRole('button', { name: '创建账号' }).click()
  await expect(page.getByRole('heading', { name: '加入海龟汤' })).toBeVisible()
  await page.getByLabel('用户名').fill(username)
  await page.getByLabel('密码').fill(playerPassword)
  await page.locator('form').getByRole('button', { name: '创建账号' }).click()
  await expect(page.getByRole('status')).toHaveText('注册成功，请登录')

  await page.getByLabel('密码').fill(playerPassword)
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page.getByRole('button', { name: '我的主页' })).toBeVisible()
}

function chooseSoup(page, title) {
  return page.getByRole('group', { name: '选择题目' })
    .getByRole('button', { name: new RegExp(title) }).click()
}

async function openCommunity(page) {
  const [list] = await Promise.all([
    page.waitForResponse(res => res.request().method() === 'GET' && new URL(res.url()).pathname === '/api/soups'),
    page.getByRole('button', { name: /^题目社区/ }).click(),
  ])
  expect(list.ok(), '社区题目请求应成功').toBe(true)
  await expect(page.getByRole('heading', { name: '题目社区' })).toBeVisible()
  return list.json()
}

async function askInRoom(page, question) {
  await page.getByLabel('提出问题').fill(question)
  await page.getByRole('button', { name: '提问', exact: true }).click()
}

test('注册、发题、多人重连、AI 主持和后台审核', async ({ actors }) => {
  test.setTimeout(60_000)
  const { author, player, solo, admin } = actors
  const id = randomBytes(5).toString('hex')
  const names = { author: `e2e_host_${id}`, player: `e2e_guest_${id}`, solo: `e2e_solo_${id}` }
  const title = `上锁的房间 ${id}`
  const surface = '门从里面锁着。倒在房内的人没有把钥匙交给任何人。'
  const answer = `汤底 ${id}：死者自己锁了门，随后因旧病发作倒地。`
  const firstQuestion = '除了死者，当时屋里还有别人吗？'
  const nextQuestion = '钥匙还在屋里吗？'
  const adminPassword = process.env.E2E_ADMIN_PASSWORD
  expect(adminPassword, '应由本地测试夹具设置管理员密码').toBeTruthy()

  await test.step('分别注册并登录房主、多人玩家与单人玩家', async () => {
    await registerAndLogin(author, names.author)
    await registerAndLogin(player, names.player)
    await registerAndLogin(solo, names.solo)
  })

  await test.step('发布题目并验证其他玩家读不到汤底', async () => {
    await author.getByRole('button', { name: '发布题目' }).click()
    await author.getByLabel('标题').fill(title)
    await author.getByLabel('汤面').fill(surface)
    await author.getByLabel('汤底').fill(answer)
    await author.getByRole('button', { name: '发布题目' }).click()
    await expect(author.getByRole('heading', { name: title })).toBeVisible()
    await expect(author.getByText('题目已发布', { exact: true })).toBeVisible()

    await openCommunity(player)
    const [detail] = await Promise.all([
      player.waitForResponse(res => res.request().method() === 'GET' && /^\/api\/soups\/\d+$/.test(new URL(res.url()).pathname)),
      player.getByRole('button', { name: title, exact: true }).click(),
    ])
    expect(detail.ok(), '普通玩家应能读取汤面').toBe(true)
    expect(JSON.stringify(await detail.json())).not.toContain(answer)
    await expect(player.getByRole('heading', { name: title })).toBeVisible()
    await expect(player.getByText(surface, { exact: true })).toBeVisible()
    await expect(player.getByText(answer, { exact: true })).toHaveCount(0)
  })

  await test.step('创建共探房，玩家用房间码加入，开局后发送提问', async () => {
    await author.getByRole('button', { name: '返回', exact: true }).click()
    await author.getByRole('button', { name: /^多人房间/ }).click()
    await author.getByRole('button', { name: /^创建房间/ }).click()
    await chooseSoup(author, title)
    await author.getByRole('button', { name: /^共探/ }).click()
    await author.getByRole('checkbox', { name: '我来主持' }).check()
    await author.getByRole('checkbox', { name: '在大厅公开' }).uncheck()
    await author.getByRole('button', { name: /^确认创建/ }).click()
    const code = author.getByText('房间码', { exact: true }).locator('..').locator('strong')
    await expect(code).toHaveText(/\S+/)
    await expect(author.getByText('实时连接正常')).toBeVisible()

    await player.goto('/')
    await expect(player.getByRole('button', { name: '我的主页' })).toBeVisible()
    await player.getByRole('button', { name: /^多人房间/ }).click()
    await player.getByRole('textbox', { name: '房间码' }).fill(await code.innerText())
    await player.getByRole('button', { name: /^加入房间/ }).click()
    await expect(player.getByRole('heading', { name: '共探房间' })).toBeVisible()
    await expect(player.getByText('实时连接正常')).toBeVisible()
    await author.getByRole('button', { name: /^开始游戏/ }).click()

    await askInRoom(player, firstQuestion)
    await expect(author.getByRole('region', { name: '对局记录' }).getByText(firstQuestion)).toBeVisible()
    await expect(player.getByText(answer, { exact: true })).toHaveCount(0)
  })

  await test.step('主持人断开 WebSocket 后恢复，记录不重复并可继续提问', async () => {
    // 导航离开会关闭真实连接，同一个页面回到站点时仍保留会话。
    await author.goto('about:blank')
    const paused = player.getByRole('heading', { name: '对局已暂停' })
    await expect(paused).toBeVisible({ timeout: 15_000 })
    await expect(player.getByRole('button', { name: '提问', exact: true })).toHaveCount(0)

    await author.goto('/')
    await expect(author.getByRole('heading', { name: '共探房间' })).toBeVisible()
    await expect(author.getByText('实时连接正常')).toBeVisible()
    await expect(paused).toHaveCount(0)
    const records = player.getByRole('region', { name: '对局记录' })
    await expect(records.getByText(firstQuestion)).toHaveCount(1)
    await askInRoom(player, nextQuestion)
    await expect(author.getByRole('region', { name: '对局记录' }).getByText(nextQuestion)).toBeVisible()
  })

  await test.step('通过独立账号开启 AI 对局并收到约定的判定', async () => {
    await solo.getByRole('button', { name: /^单人模式/ }).click()
    await chooseSoup(solo, title)
    await solo.getByRole('button', { name: /^开始推理/ }).click()
    await solo.getByLabel('输入一个可以回答是、否或无关的问题').fill(modelQuestion)
    await solo.getByRole('button', { name: /^提问/ }).click()
    await expect(solo.getByText(/^本次判定：是 ·/)).toBeVisible()
    await expect(solo.getByText(modelQuestion, { exact: true })).toBeVisible()
    await expect(solo.getByText(answer, { exact: true })).toHaveCount(0)
  })

  await test.step('后台拒绝普通用户，管理员隐藏题目后社区不再展示', async () => {
    await solo.goto('/admin')
    await solo.getByLabel('用户名').fill(names.solo)
    await solo.getByLabel('密码').fill(playerPassword)
    await solo.getByRole('button', { name: '登录后台' }).click()
    await expect(solo.getByRole('alert')).toHaveText('无后台登录权限')

    await admin.goto('/admin')
    await admin.getByLabel('用户名').fill('king')
    await admin.getByLabel('密码').fill(adminPassword)
    await admin.getByRole('button', { name: '登录后台' }).click()
    await admin.getByRole('link', { name: /进入题目管理/ }).click()
    const row = admin.getByRole('table', { name: '题目列表' }).getByRole('row').filter({ hasText: title })
    await row.getByRole('button', { name: '隐藏', exact: true }).click()
    const dialog = admin.getByRole('dialog', { name: '隐藏题目' })
    await dialog.getByRole('button', { name: '确认隐藏' }).click()
    await expect(admin.getByRole('status')).toContainText('题目已隐藏')
    await expect(row.getByRole('button', { name: '恢复公开' })).toBeVisible()

    await solo.goto('/')
    await expect(solo.getByRole('button', { name: '我的主页' })).toBeVisible()
    const soups = await openCommunity(solo)
    expect(soups.map(soup => soup.title)).not.toContain(title)
    await expect(solo.getByText('正在加载题目…', { exact: true })).toHaveCount(0)
    await expect(solo.getByRole('button', { name: title, exact: true })).toHaveCount(0)
  })
})
