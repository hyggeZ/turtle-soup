// 只模拟百炼的外部响应，浏览器和 Go 仍使用真实的应用接口。
import { once } from 'node:events'
import { createServer } from 'node:http'

export const qwenPort = 18081
export const qwenKey = 'fixture-only'
export const qwenModel = 'e2e-qwen'
export const modelQuestion = '门是死者自己锁上的吗？'

export async function startQwen() {
  const server = createServer(async (req, res) => {
    res.setHeader('Content-Type', 'application/json')
    if (req.method !== 'POST' || req.url !== '/chat/completions') {
      res.writeHead(404).end('{}')
      return
    }
    if (req.headers.authorization !== `Bearer ${qwenKey}`) {
      res.writeHead(401).end('{}')
      return
    }

    try {
      const parts = []
      for await (const part of req) parts.push(part)
      const body = JSON.parse(Buffer.concat(parts).toString('utf8'))
      const input = JSON.parse(body.messages.find(message => message.role === 'user').content)
      if (body.model !== qwenModel || input.input !== modelQuestion) {
        res.writeHead(422).end(JSON.stringify({ error: '模型或提问与测试夹具不符' }))
        return
      }
      res.end(JSON.stringify({
        model: qwenModel,
        choices: [{ finish_reason: 'stop', message: { content: JSON.stringify({ result: '是' }) } }],
        usage: { prompt_tokens: 20, completion_tokens: 4 },
      }))
    } catch {
      res.writeHead(400).end(JSON.stringify({ error: '无法解析模型请求' }))
    }
  })
  server.listen(qwenPort, '127.0.0.1')
  await once(server, 'listening')
  return server
}
