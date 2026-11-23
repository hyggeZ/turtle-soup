import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { request } from '../../api.js'
import { validHistoryBefore, validNewPage } from './eventHistory.js'
import { roomSnapshotPath, validTaskPage } from './taskView.js'

// 由 Go 房间接口测试验证同一份文件，前端用真实请求与校验函数消费契约。
const file = new URL('../../../../internal/room/testdata/snapshot-contract-v1.json', import.meta.url)
const contract = JSON.parse(readFileSync(file, 'utf8'))
const roomId = 7

test('房间快照使用第 1 版契约', () => {
  assert.equal(contract.version, 1)
  assert.ok(contract.cases.length > 0)
})

for (const scenario of contract.cases) {
  test(`房间快照契约 · ${scenario.name}`, async t => {
    const { query, status, response } = scenario
    const path = `/api/rooms/${roomId}?${query}`
    const params = new URLSearchParams(query)
    const after = Number(params.get('after'))
    const taskCursor = Number(params.get('tasksAfter'))

    if (status !== 200) {
      t.mock.method(globalThis, 'fetch', async () => Response.json(response, { status }))
      await assert.rejects(request(path), { status, message: response.error })
      return
    }

    if (params.get('tasks') === '1') {
      assert.equal(roomSnapshotPath(roomId, params.has('after'), after, taskCursor), path)
      assert.equal(validTaskPage(response.tasks, taskCursor), true, '应能读取待办游标与列表')
    }

    if (params.get('history') === '2' && params.has('after')) {
      assert.equal(validNewPage(response, after), true, '应能按原始事件游标继续追赶')
    } else if (params.get('history') === '2') {
      const before = params.has('before') ? Number(params.get('before')) : undefined
      assert.equal(validHistoryBefore(response.historyBefore, before), true, '应能继续向前翻页或到达末尾')
    } else {
      assert.ok(Array.isArray(response.events))
      assert.ok(Number.isSafeInteger(response.eventCursor) && response.eventCursor >= after)
    }
  })
}
