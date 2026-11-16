import assert from 'node:assert/strict'
import test from 'node:test'
import { needsRefresh, offlinePollDelay, reconnectDelay, snapshotRetryDelay } from './retryTiming.js'

test('重连间隔逐级增加，抖动后不超过 30 秒', () => {
  assert.equal(reconnectDelay(1, () => 0), 1500)
  assert.equal(reconnectDelay(1, () => 1), 2500)
  assert.equal(reconnectDelay(2, () => 0.5), 4000)
  assert.equal(reconnectDelay(3, () => 0.5), 8000)
  assert.equal(reconnectDelay(4, () => 0.5), 16000)
  assert.equal(reconnectDelay(5, () => 1), 30000)
  assert.equal(reconnectDelay(100, () => 0), 22500)
})

test('离线补查分散在 30 至 60 秒', () => {
  assert.equal(offlinePollDelay(() => 0), 30000)
  assert.equal(offlinePollDelay(() => 0.5), 45000)
  assert.equal(offlinePollDelay(() => 1), 60000)
})

test('快照持续失败时逐级退避，成功后可从第一档重试', () => {
  assert.equal(snapshotRetryDelay(1, () => 0), 7500)
  assert.equal(snapshotRetryDelay(1, () => 0.5), 10000)
  assert.equal(snapshotRetryDelay(2, () => 0.5), 20000)
  assert.equal(snapshotRetryDelay(3, () => 0.5), 40000)
  assert.equal(snapshotRetryDelay(4, () => 1), 60000)
  assert.equal(snapshotRetryDelay(100, () => 0), 45000)
})

test('最近成功读取过快照，也会在新失败的退避到期后重试', () => {
  assert.equal(needsRefresh(true, 1, 1000, 2000), true)
  assert.equal(needsRefresh(true, 0, 1000, 2000), false)
  assert.equal(needsRefresh(true, 0, 1000, 61000), true)
  assert.equal(needsRefresh(false, 0, 1000, 2000), true)
})

function seededRandom(seed) {
  let state = seed
  return () => {
    state = (Math.imul(state, 1664525) + 1013904223) >>> 0
    return state / 2 ** 32
  }
}

function simulate(delay, clients = 1000, seconds = 60) {
  const random = seededRandom(20261116)
  const buckets = new Map()
  let requests = 0
  for (let client = 0; client < clients; client++) {
    let elapsed = 0
    let failures = 0
    while (true) {
      elapsed += delay(++failures, random)
      if (elapsed > seconds * 1000) break
      requests++
      const bucket = Math.floor(elapsed / 100)
      buckets.set(bucket, (buckets.get(bucket) || 0) + 1)
    }
  }
  return { requests, peak100ms: Math.max(0, ...buckets.values()) }
}

test('千名客户端持续断线时，定时模拟错开重试请求', () => {
  const oldWs = simulate(() => 2000)
  const nextWs = simulate((failures, random) => reconnectDelay(failures, random))
  const oldHttp = simulate(() => 10000)
  const nextHttp = simulate((_, random) => offlinePollDelay(random))
  const nextFailedHttp = simulate((failures, random) => snapshotRetryDelay(failures, random))
  assert.ok(nextWs.requests < oldWs.requests)
  assert.ok(nextWs.peak100ms < oldWs.peak100ms)
  assert.ok(nextHttp.requests < oldHttp.requests)
  assert.ok(nextHttp.peak100ms < oldHttp.peak100ms)
  assert.ok(nextFailedHttp.requests < oldHttp.requests)
  assert.ok(nextFailedHttp.peak100ms < oldHttp.peak100ms)
  console.log('断线模拟（60 秒、1000 客户端、100 毫秒峰值）：', { oldWs, nextWs, oldHttp, nextHttp, nextFailedHttp })
})
