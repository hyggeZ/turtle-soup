// 所有页面通过同一个入口请求后端，并统一显示后端返回的错误。
function retryDeadline(value) {
  if (!value) return 0
  const now = Date.now()
  const header = value.trim()
  if (/^\d+$/.test(header)) {
    const until = now + Number(header) * 1000
    return Number.isSafeInteger(until) && until > now ? until : 0
  }
  // HTTP 日期采用 GMT 格式；回写校验会排除不完整或被自动修正的日期。
  const date = new Date(header)
  return date.getTime() > now && date.toUTCString() === header ? date.getTime() : 0
}

export async function request(path, options = {}) {
  const response = await fetch(path, { credentials: 'same-origin', ...options })
  if (response.status === 204 || (response.status === 201 && path.endsWith('/comments'))) return null
  const retryAt = response.status === 429 ? retryDeadline(response.headers.get('Retry-After')) : 0
  const json = response.json()
  const data = await (retryAt ? json.catch(() => null) : json)
  if (!response.ok) {
    const error = new Error(data?.error || '请求失败')
    error.status = response.status
    if (typeof data?.code === 'string') error.code = data.code
    if (retryAt) error.retryAt = retryAt
    throw error
  }
  return data
}

export function send(path, method, body) {
  return request(path, {
    method,
    ...(body ? { headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) } : {}),
  })
}
