/**
 * 历史页和实时增量都可能补发旧问题，按数字事件 ID 合并并保留调用方的字段。
 * @template {{ id: number }} Event
 * @param {Event[]} previous
 * @param {Event[]} incoming
 */
export function mergeEvents(previous, incoming) {
  const byId = new Map(previous.map(event => [event.id, event]))
  for (const event of incoming) byId.set(event.id, event)
  return [...byId.values()].sort((a, b) => a.id - b.id)
}

/**
 * 校验用于向前翻页的游标。
 * @param {number} cursor
 * @param {number} [previous]
 */
export function validHistoryBefore(cursor, previous = Infinity) {
  return Number.isSafeInteger(cursor) && cursor >= 0 && (cursor === 0 || cursor < previous)
}

/**
 * 增量游标跟随原始事件。本页因权限过滤而不可见时，游标仍须正确前进。
 * @param {{ events: unknown, eventCursor: number, hasMoreNew: boolean }} page
 * @param {number} after
 */
export function validNewPage(page, after) {
  return Array.isArray(page.events) && Number.isSafeInteger(page.eventCursor) &&
    page.eventCursor >= after && typeof page.hasMoreNew === 'boolean' &&
    (!page.hasMoreNew || page.eventCursor > after)
}
