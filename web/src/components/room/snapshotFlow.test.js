import assert from 'node:assert/strict'
import test from 'node:test'
import { createSingleFlight, sameRoomView } from './snapshotFlow.js'
import { validNewPage } from './eventHistory.js'

function deferred() {
  let resolve
  const promise = new Promise(done => { resolve = done })
  return { promise, resolve }
}

test('实时通知期间同一时刻只读一份快照，后续通知合成一次', async () => {
  const first = deferred()
  const second = deferred()
  let calls = 0
  const queue = createSingleFlight(() => ++calls === 1 ? first.promise : second.promise)
  const initial = queue.request()
  const later = Array.from({ length: 100 }, () => queue.request())
  await Promise.resolve()
  assert.equal(calls, 1)
  first.resolve('第一页')
  assert.equal(await initial, '第一页')
  await Promise.resolve()
  assert.equal(calls, 2)
  second.resolve('追赶页')
  assert.deepEqual(await Promise.all(later), Array(100).fill('追赶页'))
  assert.equal(calls, 2)
})

test('切换房间时清掉旧排队请求，旧响应不属于新视图', async () => {
  const first = deferred()
  let calls = 0
  const queue = createSingleFlight(() => { calls++; return first.promise })
  const initial = queue.request()
  const queued = queue.request()
  queue.cancelQueued()
  assert.equal(await queued, undefined)
  first.resolve('旧房间')
  await initial
  assert.equal(calls, 1)
  const expected = { id: 7, userId: 2, epoch: 3 }
  assert.equal(sameRoomView({ active: true, ...expected }, expected), true)
  assert.equal(sameRoomView({ active: true, id: 7, userId: 3, epoch: 3 }, expected), false)
  assert.equal(sameRoomView({ active: true, id: 8, userId: 2, epoch: 3 }, expected), false)
  assert.equal(sameRoomView({ active: true, id: 7, userId: 2, epoch: 4 }, expected), false)
})

test('权限过滤后的空增量页仍逐页推进，直到新事件追完', async () => {
  const pages = [
    { events: [], eventCursor: 50, hasMoreNew: true },
    { events: [], eventCursor: 100, hasMoreNew: true },
    { events: [{ id: 101 }], eventCursor: 101, hasMoreNew: false },
  ]
  const done = deferred()
  let cursor = 0
  let calls = 0
  const queue = createSingleFlight(() => {
    const page = pages.shift()
    calls++
    assert.equal(validNewPage(page, cursor), true)
    cursor = page.eventCursor
    if (page.hasMoreNew) queue.request()
    else done.resolve()
  })
  queue.request()
  await done.promise
  assert.equal(cursor, 101)
  assert.equal(calls, 3)
})

test('翻页目标在旧快照飞行中设置时，只由读取目标页的响应结算', async () => {
  const oldPage = deferred()
  const targetPage = deferred()
  let calls = 0
  let target = null
  let cursor = 0
  let loading = false
  const queue = createSingleFlight(async () => {
    const requested = target
    await (++calls === 1 ? oldPage.promise : targetPage.promise)
    if (target === requested) {
      cursor = requested ?? cursor
      target = null
      loading = false
    }
  })
  const first = queue.request()
  await Promise.resolve()
  target = 50
  loading = true
  const next = queue.request()
  oldPage.resolve()
  await first
  assert.equal(cursor, 0)
  assert.equal(loading, true)
  targetPage.resolve()
  await next
  assert.equal(cursor, 50)
  assert.equal(loading, false)
  assert.equal(calls, 2)
})
