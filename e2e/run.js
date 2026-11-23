// 在临时目录构建并启动测试服务，退出时回收本次运行创建的进程。
import { spawn } from 'node:child_process'
import { once } from 'node:events'
import { mkdtemp, rm } from 'node:fs/promises'
import { createServer } from 'node:net'
import { tmpdir } from 'node:os'
import { join, sep } from 'node:path'
import { setTimeout as delay } from 'node:timers/promises'
import { fileURLToPath } from 'node:url'
import { qwenKey, qwenModel, qwenPort, startQwen } from './qwen.js'

const here = fileURLToPath(new URL('.', import.meta.url))
const root = fileURLToPath(new URL('..', import.meta.url))
const web = join(root, 'web')
const vite = join(web, 'node_modules', 'vite', 'bin', 'vite.js')
const playwright = join(here, 'node_modules', '@playwright', 'test', 'cli.js')
const host = '127.0.0.1'
const apiPort = 18080
const webPort = 4173
const adminPassword = 'turtle-only-fixture-admin-26'
const programs = []
let scratch
let qwen
let stopping = false

function databaseURL() {
  let url
  try { url = new URL(process.env.E2E_DATABASE_URL) } catch { /* 统一提示连接要求。 */ }
  if (!url || !['postgres:', 'postgresql:'].includes(url.protocol) ||
      !['127.0.0.1', '[::1]'].includes(url.hostname) || url.pathname !== '/turtle_e2e' ||
      !url.username || !url.password || !url.port || url.hash || url.search !== '?sslmode=disable') {
    throw new Error('E2E_DATABASE_URL 必须指定本地回环地址、端口、专用 turtle_e2e 数据库及 ?sslmode=disable。')
  }
  return url.href
}

function start(name, file, args, options = {}) {
  if (stopping) throw new Error('测试正在退出。')
  const child = spawn(file, args, { cwd: root, stdio: 'inherit', windowsHide: true, ...options })
  const exit = new Promise(resolve => {
    child.once('error', error => resolve({ error }))
    child.once('exit', (code, signal) => resolve({ code, signal }))
  })
  const program = { name, child, exit }
  programs.push(program)
  return program
}

function exitError(name, { code, signal, error }) {
  return new Error(`${name}退出（${error?.message || signal || code}）。`)
}

async function run(name, file, args, options) {
  const { exit } = start(name, file, args, options)
  const result = await exit
  if (result.code !== 0) throw exitError(name, result)
}

async function requireFreePort(port) {
  const probe = createServer()
  probe.listen(port, host)
  await once(probe, 'listening')
  await new Promise(resolve => probe.close(resolve))
}

async function ready(program, url) {
  async function poll() {
    for (let attempt = 0; attempt < 100 && !stopping; attempt++) {
      try {
        const response = await fetch(url, { signal: AbortSignal.timeout(500) })
        await response.body?.cancel()
        if (response.ok) return
      } catch { /* 正在启动，继续等到就绪或进程退出。 */ }
      await delay(150)
    }
    throw new Error(`${program.name}没有在限定时间内启动。`)
  }
  await Promise.race([poll(), program.exit.then(result => { throw exitError(program.name, result) })])
}

async function cleanup() {
  stopping = true
  if (qwen) {
    qwen.close()
    qwen.closeAllConnections()
  }
  const live = programs.filter(({ child }) => child.pid && child.exitCode === null && child.signalCode === null)
  for (const { child } of live) child.kill()
  const force = setTimeout(() => {
    for (const { child } of live) child.kill('SIGKILL')
  }, 1500)
  await Promise.all(live.map(({ exit }) => exit))
  clearTimeout(force)
  if (scratch) await rm(scratch, { recursive: true, force: true, maxRetries: 3 })
}

async function main() {
  const database = databaseURL()
  await requireFreePort(apiPort)
  await requireFreePort(webPort)
  scratch = await mkdtemp(join(tmpdir(), 'turtle-e2e-'))
  qwen = await startQwen()

  // 覆盖应用配置并清除 PG 环境变量，防止本机服务配置改变数据库目的地址。
  const config = {
    DATABASE_URL: database,
    SUPER_ADMIN_PASSWORD: adminPassword,
    SERVER_ADDR: `${host}:${apiPort}`,
    COOKIE_SECURE: 'false',
    LOG_DIR: join(scratch, 'logs'),
    METRICS_ADDR: '',
    TRUSTED_PROXIES: '',
    DASHSCOPE_API_KEY: qwenKey,
    QWEN_BASE_URL: `http://${host}:${qwenPort}`,
    QWEN_MODEL: qwenModel,
  }
  const env = Object.fromEntries(Object.entries(process.env).filter(([name]) =>
    !/^PG/i.test(name) && !(name.toUpperCase() in config)))
  Object.assign(env, config)

  const dist = join(scratch, 'web')
  await run('构建网页', process.execPath, [vite, 'build', '--outDir', dist], { cwd: web })
  await run('构建 Go 服务', 'go', ['build', '-mod=readonly', '-o', scratch + sep, './cmd/admin', './cmd/server'])
  const exe = process.platform === 'win32' ? '.exe' : ''
  await run('准备测试管理员', join(scratch, `admin${exe}`), [], { env, timeout: 30_000 })
  const api = start('Go API', join(scratch, `server${exe}`), [], { env })
  await ready(api, `http://${host}:${apiPort}/health`)
  const preview = start('Vite 预览', process.execPath,
    [vite, 'preview', '--host', host, '--port', String(webPort), '--strictPort', '--outDir', dist], { cwd: web })
  await ready(preview, `http://${host}:${webPort}/`)

  const testEnv = { ...process.env, E2E_ADMIN_PASSWORD: adminPassword }
  await run('浏览器验收', process.execPath, [playwright, 'test', ...process.argv.slice(2)], { cwd: here, env: testEnv })
}

for (const [signal, code] of [['SIGINT', 130], ['SIGTERM', 143]]) {
  process.once(signal, () => {
    process.exitCode = code
    stopping = true
    for (const { child } of programs) child.kill()
  })
}

try {
  await main()
} catch (error) {
  if (!stopping) console.error(error.message)
  process.exitCode ||= 1
} finally {
  await cleanup()
}
