// 失败后逐步延长重连间隔，抖动用于错开同时断线的客户端。
export function reconnectDelay(failures, random = Math.random) {
  const step = Math.min(30000, 2000 * 2 ** Math.min(Math.max(failures - 1, 0), 4))
  return Math.round(Math.min(30000, step * (0.75 + random() * 0.5)))
}

// 实时连接不可用时，降低补查频率并错开各客户端请求。
export function offlinePollDelay(random = Math.random) {
  return Math.round(30000 + random() * 30000)
}

// 实时连接尚在但快照读取失败时，也要避免固定间隔重试形成同步请求。
export function snapshotRetryDelay(failures, random = Math.random) {
  const step = Math.min(60000, 10000 * 2 ** Math.min(Math.max(failures - 1, 0), 3))
  return Math.round(Math.min(60000, step * (0.75 + random() * 0.5)))
}

export function needsRefresh(connected, failures, lastLoadedAt, now) {
  return !connected || failures > 0 || now - lastLoadedAt >= 60000
}
