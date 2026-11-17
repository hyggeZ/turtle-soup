// 待办页与事件历史各用自己的游标，查询时不要把两者混在一起。
export function roomSnapshotPath(id, eventCursor, taskCursor) {
  const events = eventCursor ? `after=${eventCursor}` : 'history=1'
  const tasks = taskCursor ? `&tasksAfter=${taskCursor}` : ''
  return `/api/rooms/${id}?${events}&tasks=1${tasks}`
}

export function validTaskPage(tasks, cursor) {
  return tasks && typeof tasks.pendingRestore === 'boolean' && typeof tasks.pendingAnswer === 'boolean' &&
    Array.isArray(tasks.items) && tasks.items.length <= 50 &&
    Number.isSafeInteger(tasks.nextAfter) && tasks.nextAfter >= 0 &&
    (tasks.nextAfter === 0 || tasks.nextAfter > cursor)
}
