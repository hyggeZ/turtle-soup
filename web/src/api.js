// 所有页面通过同一个入口请求后端，并统一显示后端返回的错误。
export async function request(path, options = {}) {
  const response = await fetch(path, { credentials: 'same-origin', ...options })
  if (response.status === 204 || (response.status === 201 && path.endsWith('/comments'))) return null
  const data = await response.json()
  if (!response.ok) throw new Error(data.error || '请求失败')
  return data
}

export function send(path, method, body) {
  return request(path, {
    method,
    ...(body ? { headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) } : {}),
  })
}
