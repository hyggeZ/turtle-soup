// 用真实预览响应发送部署策略，保留同源 API 和 WebSocket 的实际网络连接。
import { readFile } from 'node:fs/promises'
import config from '../web/vite.config.js'

const site = await readFile(new URL('../deploy/turtle-soup.nginx.conf', import.meta.url), 'utf8')
const policy = site.match(/^\s*add_header Content-Security-Policy "([^"\r\n]+)" always;/m)?.[1]
if (!policy) throw new Error('Nginx 站点配置缺少 CSP')

export default {
  ...config,
  preview: { ...config.preview, headers: { ...config.preview?.headers, 'Content-Security-Policy': policy } },
}
