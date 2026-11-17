import assert from 'node:assert/strict'
import test from 'node:test'
import { mergeEvents, validHistoryBefore } from './eventHistory.js'

test('较早历史与实时补发按 ID 合并，不重复、不回退已有记录', () => {
  const current = [{ id: 100, body: '问题' }, { id: 102, body: '回答' }]
  const older = [{ id: 20, body: '旧问题' }, { id: 100, body: '更新后的问题' }]
  assert.deepEqual(mergeEvents(current, older), [
    { id: 20, body: '旧问题' }, { id: 100, body: '更新后的问题' }, { id: 102, body: '回答' },
  ])
})

test('历史游标必须向更早的原始事件移动或明确结束', () => {
  assert.equal(validHistoryBefore(60), true)
  assert.equal(validHistoryBefore(20, 60), true)
  assert.equal(validHistoryBefore(0, 20), true)
  assert.equal(validHistoryBefore(60, 60), false)
  assert.equal(validHistoryBefore(80, 60), false)
  assert.equal(validHistoryBefore(-1, 60), false)
  assert.equal(validHistoryBefore('20', 60), false)
})
