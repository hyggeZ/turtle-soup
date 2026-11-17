// 历史页和实时增量都可能补发旧问题，按事件 ID 合并并保持发生顺序。
export function mergeEvents(previous, incoming) {
  const byId = new Map(previous.map(event => [event.id, event]))
  for (const event of incoming) byId.set(event.id, event)
  return [...byId.values()].sort((a, b) => a.id - b.id)
}

export function validHistoryBefore(cursor, previous = Infinity) {
  return Number.isSafeInteger(cursor) && cursor >= 0 && (cursor === 0 || cursor < previous)
}

// 增量游标跟随原始事件，即使本页事件都因权限过滤而不可见也必须前进。
export function validNewPage(page, after) {
  return Array.isArray(page.events) && Number.isSafeInteger(page.eventCursor) &&
    page.eventCursor >= after && typeof page.hasMoreNew === 'boolean' &&
    (!page.hasMoreNew || page.eventCursor > after)
}
