// 仅在回环地址模拟实时服务不可用，浏览器照常走 HTTP 获取快照。
import { expect, test } from '@playwright/test'
import { once } from 'node:events'
import { readFile } from 'node:fs/promises'
import { createServer } from 'node:http'
import { extname, resolve, sep } from 'node:path'
import { fileURLToPath } from 'node:url'

const roomId = 7
const roomPath = `/api/rooms/${roomId}`
const user = { id: 23, username: '小林', role: 'user' }
const question = '停电前，车站的钟走得正常吗？'
const contentTypes = { '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css', '.webp': 'image/webp' }

function sendJSON(res, data) {
  res.writeHead(200, { 'content-type': 'application/json; charset=utf-8' }).end(JSON.stringify(data))
}

async function staticFile(res, dist, path) {
  const file = resolve(dist, path === '/' ? 'index.html' : path.slice(1))
  if (!file.startsWith(dist + sep)) return res.writeHead(404).end()
  try {
    const data = await readFile(file)
    return res.writeHead(200, { 'content-type': contentTypes[extname(file)] || 'application/octet-stream' }).end(data)
  } catch {
    return res.writeHead(404).end()
  }
}

async function startFixture() {
  const dist = resolve(process.env.ROOM_OFFLINE_WEB_DIST || fileURLToPath(new URL('../web/dist', import.meta.url)))
  await readFile(resolve(dist, 'index.html'))
  const reads = []
  let upgrades = 0
  let event
  const server = createServer((req, res) => {
    const { pathname, searchParams } = new URL(req.url, 'http://127.0.0.1')
    if (pathname === '/api/me') return sendJSON(res, user)
    if (pathname === roomPath) {
      const after = searchParams.get('after')
      reads.push(after)
      return sendJSON(res, {
        id: roomId, code: 'OFF707', mode: 'casual', status: 'active', myRole: 'player', maxPlayers: 4,
        title: `快照 ${reads.length}`, genre: '本格', difficulty: 2, horror: 1, surface: '车站停电了。',
        members: [{ id: user.id, username: user.username, role: 'player', online: false }],
        events: event && event.id > Number(after) ? [event] : [],
        eventCursor: event?.id || 0, historyBefore: 0, hasMoreNew: false,
      })
    }
    if (pathname.startsWith('/api/')) return sendJSON(res, [])
    return staticFile(res, dist, pathname)
  })
  server.on('upgrade', (req, socket) => {
    if (req.url !== `${roomPath}/ws`) return socket.destroy()
    upgrades++
    socket.end('HTTP/1.1 503 Service Unavailable\r\nConnection: close\r\nContent-Length: 0\r\n\r\n')
  })
  server.listen(0, '127.0.0.1')
  await once(server, 'listening')
  return {
    origin: `http://127.0.0.1:${server.address().port}`,
    reads,
    get upgrades() { return upgrades },
    publishQuestion() { event = { id: 1, userId: user.id, kind: 'question', body: question, createdAt: '2026-01-01T00:00:00Z' } },
    async close() {
      server.closeAllConnections()
      await new Promise((done, fail) => server.close(error => error ? fail(error) : done()))
    },
  }
}

test('WebSocket 持续握手失败时仍按游标补查，离开房间后停止', async ({ page }) => {
  const fixture = await startFixture()
  try {
    const start = Date.UTC(2026, 0, 1)
    await page.clock.install({ time: start - 60_000 })
    await page.clock.pauseAt(start)
    await page.addInitScript(({ roomId, roomPath, userId }) => {
      sessionStorage.setItem(`turtle_room_view_${userId}`, String(roomId))
      // 固定抖动并观测时间；WebSocket 和 fetch 请求仍由浏览器真实发送。
      Math.random = () => 0.5
      const probe = window.roomOfflineProbe = { opened: 0, closed: 0, reads: [] }
      const NativeSocket = window.WebSocket
      window.WebSocket = class extends NativeSocket {
        constructor(...args) {
          super(...args)
          this.addEventListener('open', () => { probe.opened++ })
          this.addEventListener('close', () => { probe.closed++ })
        }
      }
      const fetch = window.fetch.bind(window)
      window.fetch = (path, options) => {
        if (new URL(path, location.href).pathname === roomPath) probe.reads.push(Date.now())
        return fetch(path, options)
      }
    }, { roomId, roomPath, userId: user.id })
    await page.goto(fixture.origin)
    await expect(page.getByRole('heading', { name: '快照 1' })).toBeVisible()
    await expect(page.getByText('正在重连')).toBeVisible()
    await expect(page.getByText(question, { exact: true })).toHaveCount(0)
    fixture.publishQuestion()

    const retries = new Set([0, 2_000, 6_000, 14_000, 30_000, 60_000, 90_000, 120_000, 150_000])
    const checkpoints = [...retries, 45_000, 135_000, 170_000].sort((a, b) => a - b)
    const probe = () => page.evaluate(() => window.roomOfflineProbe)
    let elapsed = 0
    let sockets = 0
    let reads = 1
    for (const time of checkpoints) {
      if (time > elapsed) await page.clock.runFor(time - elapsed)
      elapsed = time
      // 网络事件不随虚拟时间同步完成，先等待浏览器处理关闭和快照再推进。
      if (retries.has(time)) {
        sockets++
        await expect.poll(async () => (await probe()).closed).toBe(sockets)
      }
      const count = (await probe()).reads.length
      if (count > reads) await expect(page.getByRole('heading', { name: `快照 ${count}`, exact: true })).toBeVisible()
      reads = count
    }

    const stats = await probe()
    const pollTimes = stats.reads.slice(1).map(time => time - stats.reads[0])
    const latestQuestion = page.getByRole('region', { name: '对局记录' }).getByText(question, { exact: true })
    const delivered = await latestQuestion.count()
    console.log('170 秒虚拟时间内的断线补查：', { pollTimes, upgrades: fixture.upgrades, delivered, cursors: fixture.reads })
    await page.getByRole('button', { name: '返回' }).click()
    await expect(page.getByRole('heading', { name: '房间大厅' })).toBeVisible()
    await page.clock.runFor(120_000)
    expect(await probe()).toEqual(stats)
    expect(fixture.upgrades).toBe(retries.size)
    expect(stats.opened).toBe(0)
    expect(pollTimes).toEqual([45_000, 90_000, 135_000])
    expect(delivered).toBe(1)
    expect(fixture.reads).toEqual([null, '0', '1', '1'])
  } finally {
    await fixture.close()
  }
})
