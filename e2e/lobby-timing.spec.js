// 用真正的首页点击与延迟独立的模拟接口测大厅首屏。
import { expect, test } from '@playwright/test'
import { resolve, sep } from 'node:path'
import { setTimeout as delay } from 'node:timers/promises'
import { fileURLToPath } from 'node:url'

const origin = 'http://127.0.0.1:14288'
const publicPath = '/api/rooms'
const ownPath = '/api/rooms/mine'
const apiDelay = 120
const user = { id: 23, username: '测试玩家', role: 'user' }
const publicRoom = { id: 1, code: 'PUBLIC', title: '车站共探房', public: true, status: 'waiting', mode: 'casual', maxPlayers: 4, players: 1, genre: '本格', difficulty: 2, horror: 1 }
const ownRoom = { ...publicRoom, id: 2, code: 'OWN', title: '我的测试房', public: false, myRole: 'host' }

async function enterLobby(page, signedIn) {
  const dist = resolve(process.env.ROOM_OFFLINE_WEB_DIST || fileURLToPath(new URL('../web/dist', import.meta.url)))
  const received = []
  await page.route(`${origin}/**`, async route => {
    const req = route.request()
    const { pathname } = new URL(req.url())
    if (pathname === '/api/me') {
      return route.fulfill(signedIn ? { json: user } : { status: 401, json: { error: '请先登录' } })
    }
    if ([publicPath, ownPath].includes(pathname) && req.method() === 'GET') {
      const request = { path: pathname, start: performance.now(), response: null }
      received.push(request)
      await delay(apiDelay)
      request.response = performance.now()
      return route.fulfill({ json: [pathname === publicPath ? publicRoom : ownRoom] })
    }
    if (pathname.startsWith('/api/')) return route.fulfill({ status: 404, json: { error: '测试接口未声明' } })
    const file = resolve(dist, pathname === '/' ? 'index.html' : pathname.slice(1))
    if (!file.startsWith(dist + sep)) return route.fulfill({ status: 404 })
    return route.fulfill({ path: file })
  })
  await page.addInitScript(({ paths, title }) => {
    const probe = window.lobbyProbe = { inputAt: null, clickAt: null, domAt: null, requests: [], events: {}, eventSupported: false }
    const isLobbyClick = event => event.isTrusted && event.target instanceof Element &&
      event.target.closest('button')?.textContent.includes('多人房间')
    document.addEventListener('pointerdown', event => {
      if (isLobbyClick(event)) probe.inputAt = event.timeStamp
    }, true)
    document.addEventListener('click', event => {
      if (isLobbyClick(event)) probe.clickAt = event.timeStamp
    }, true)

    new MutationObserver(() => {
      if (probe.clickAt === null || probe.domAt !== null) return
      const shown = [...document.querySelectorAll('.play-room-list h3')].some(heading => heading.textContent === title)
      if (shown) probe.domAt = performance.now()
    }).observe(document, { childList: true, subtree: true })

    const fetch = window.fetch.bind(window)
    window.fetch = (path, options) => {
      const pathname = new URL(path, location.href).pathname
      if (paths.includes(pathname)) probe.requests.push({ path: pathname, start: performance.now() })
      return fetch(path, options)
    }

    if (PerformanceObserver.supportedEntryTypes.includes('event')) {
      probe.eventSupported = true
      new PerformanceObserver(list => {
        for (const entry of list.getEntries()) {
          if (!entry.interactionId || probe.inputAt === null || probe.clickAt === null) continue
          if (entry.startTime < probe.inputAt - 2 || entry.startTime > probe.clickAt + 2) continue
          const prior = probe.events[entry.interactionId]
          if (!prior || entry.duration > prior.duration) {
            probe.events[entry.interactionId] = { name: entry.name, duration: entry.duration }
          }
        }
      }).observe({ type: 'event', buffered: true, durationThreshold: 16 })
    }
  }, { paths: [publicPath, ownPath], title: publicRoom.title })

  const me = page.waitForResponse(`${origin}/api/me`)
  await page.goto(origin)
  await me
  const account = signedIn ? '我的主页' : '登录 / 注册'
  await expect(page.getByRole('button', { name: account, exact: true })).toBeVisible()
  await page.getByRole('button', { name: /^多人房间/ }).click()
  await expect(page.getByRole('heading', { name: '房间大厅', exact: true })).toBeVisible()
  await expect(page.getByRole('heading', { name: publicRoom.title, exact: true })).toBeVisible()
  const measured = await page.evaluate(() => window.lobbyProbe)
  expect(measured.clickAt).not.toBeNull()
  expect(measured.domAt).not.toBeNull()
  return { received, measured }
}

function reportTiming(kind, { received, measured }) {
  const parallel = received.length === 2 && received.every(item => item.start < Math.min(...received.map(entry => entry.response)))
  const gap = measured.requests.length === 2 ? (measured.requests[1].start - measured.requests[0].start).toFixed(2) : 'n/a'
  const elapsed = (measured.domAt - measured.clickAt).toFixed(2)
  console.log(`${kind}: requests=${received.length} before_first_response=${parallel} start_gap_ms=${gap} click_to_public_dom_ms=${elapsed} event_supported=${measured.eventSupported} interactions=${JSON.stringify(measured.events)}`)
  return parallel
}

test('登录后大厅并行发出公开列表和我的列表，两份结果都可见', async ({ page }) => {
  const fixture = await enterLobby(page, true)
  expect(fixture.received.map(item => item.path).sort()).toEqual([publicPath, ownPath].sort())
  expect(fixture.measured.requests.map(item => item.path).sort()).toEqual([publicPath, ownPath].sort())
  const parallel = reportTiming('signed_in', fixture)
  await page.getByRole('button', { name: '我的房间', exact: true }).click()
  await expect(page.getByRole('heading', { name: ownRoom.title, exact: true })).toBeVisible()
  expect(parallel, '两个列表请求应在首个响应前到达模拟接口').toBe(true)
})

test('访客进入大厅只发出公开列表请求', async ({ page }) => {
  const fixture = await enterLobby(page, false)
  reportTiming('guest', fixture)
  expect(fixture.received.map(item => item.path)).toEqual([publicPath])
  expect(fixture.measured.requests.map(item => item.path)).toEqual([publicPath])
  await expect(page.getByRole('button', { name: '我的房间', exact: true })).toHaveCount(0)
})
