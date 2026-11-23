# 第 671 条 HTTPS 会话 Cookie 回归

现有 Cookie 由 Go 会话接口签发，通过 `COOKIE_SECURE` 控制传输限制。新增回归使用真实 Gin 注册、登录、退出和隔离 PostgreSQL 会话表。测试的 HTTPS 服务校验本地测试证书，HTTP 与 HTTPS 两个客户端共享 Cookie Jar，同一测试域名只会被映射到本机。[Go Cookie Jar](https://pkg.go.dev/net/http/cookiejar)

| 操作 | 开发设置 Secure=false | 安全设置 Secure=true |
| --- | ---: | ---: |
| HTTPS 注册与登录 | 201、200 | 201、200 |
| HTTPS 访问当前会话 | 200 | 200 |
| 同名主机的明文 HTTP 访问 | 200 | 401 |
| HTTPS 退出 | 204 | 204 |
| 退出后 HTTPS 访问当前会话 | 401 | 401 |

两种设置都检查 `HttpOnly`、`SameSite=Strict`、当前主机和 `Path=/`。退出使用同名空 Cookie 和负的年龄值清除浏览器状态。第 335 条另有过期、旧令牌与改密后会话撤销的测试。[会话生命周期](49-服务端会话生命周期.md)

本次对照覆盖应用的 Cookie 属性和本机 HTTP/TLS 发送行为。跨站策略通过已有来源校验测试补足，公开域名证书及生产反代配置需在其部署条件具备时单独验收。

```powershell
go test ./internal/admin -run '^TestSessionCookieTransportWithPostgres$' -v
```

运行前设置独立 `TEST_DATABASE_URL`。两种设置连续三轮共 6/6 通过，整轮用时为 3.51、3.50、3.62 秒，含临时表迁移、密码散列及本地 HTTP/TLS 请求。业务实现沿用原代码，无新增请求耗时。
