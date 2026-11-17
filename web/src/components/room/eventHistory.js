// 历史页和实时增量都可能补发旧问题，按事件 ID 合并并保持发生顺序。
export function mergeEvents(previous, incoming) {
  const byId = new Map(previous.map(event => [event.id, event]))
  for (const event of incoming) byId.set(event.id, event)
  return [...byId.values()].sort((a, b) => a.id - b.id)
}

export function validHistoryBefore(cursor, previous = Infinity) {
  return Number.isSafeInteger(cursor) && cursor >= 0 && (cursor === 0 || cursor < previous)
}
