import assert from 'node:assert/strict'
import test from 'node:test'
import { roomSnapshotPath, validTaskPage } from './taskView.js'

test('待办游标与历史首页或事件增量分别组合', () => {
  assert.equal(roomSnapshotPath(7, false, 0, 0), '/api/rooms/7?history=2&tasks=1')
  assert.equal(roomSnapshotPath(7, true, 0, 0), '/api/rooms/7?history=2&after=0&tasks=1')
  assert.equal(roomSnapshotPath(7, true, 120, 0), '/api/rooms/7?history=2&after=120&tasks=1')
  assert.equal(roomSnapshotPath(7, true, 120, 35), '/api/rooms/7?history=2&after=120&tasks=1&tasksAfter=35')
  assert.equal(roomSnapshotPath(7, false, 0, 35), '/api/rooms/7?history=2&tasks=1&tasksAfter=35')
})

test('待办下一页游标必须前进，空页仍可安全返回第一页', () => {
  const page = { pendingRestore: false, pendingAnswer: true, items: [], nextAfter: 0 }
  assert.equal(validTaskPage(page, 50), true)
  assert.equal(validTaskPage({ ...page, nextAfter: 80 }, 50), true)
  assert.equal(validTaskPage({ ...page, nextAfter: 50 }, 50), false)
  assert.equal(validTaskPage({ ...page, nextAfter: -1 }, 50), false)
  assert.equal(validTaskPage({ ...page, pendingAnswer: 'true' }, 0), false)
})
