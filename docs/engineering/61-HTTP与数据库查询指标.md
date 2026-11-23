# 第 486 条 HTTP 与数据库查询指标

在已有 Prometheus 抓取中，用 `turtle_http_requests_total{status_class=...}` 累计匹配到 API 路由的请求，用 `turtle_http_db_queries_total` 累计这些请求中已追踪的 SQL 次数。请求分类固定为 `2xx`、`3xx`、`4xx`、`5xx`，服务启动时即建立四条序列。数据库计数只有一条序列。[Prometheus 指标命名](https://prometheus.io/docs/practices/naming/)

请求完成时复用链路记录的 HTTP 状态和数据库次数。WebSocket `101`、健康检查、监控抓取和未匹配到路由的请求不进入计数。指标不读取 URL 参数或用户资料，也不执行额外 SQL。房间加入和 AI 判定的直方图照常记录。已有回环抓取配置启用后可读取这些指标。

截至第 500 条的全量本地 Chromium 回归 17/17 通过。它包含真实隔离 PostgreSQL 上的玩家和后台旅程，以及无障碍、限流、链路失败提示、大厅调度和断线补查的浏览器用例。

抓取专项使用真实 Gin 中间件和 Prometheus 处理器，SQL 次数通过 pgx 追踪回调注入。首次抓取有 5 条零值序列。执行 8 个夹具请求后，匹配 API 的四个状态类各计 1 次，SQL 共计 6 次。夹具同时检查排除的请求、抓取内容以及两类原有直方图。

固定旧版 `2c9005b` 做同一抓取，新增指标采样前后均为 0 条序列。Windows/amd64、Go 1.26.5、单 CPU 上，旧版与候选每组 400 ms，交替执行五对。以下为请求观测函数的纳秒级 CPU 开销，计时范围不含 HTTP 和数据库往返。

| 观测路径 | 旧版中位数 | 候选中位数 | 两版分配 |
| --- | ---: | ---: | ---: |
| `GET /api/soups` | 2.644 ns/op | 14.89 ns/op | 0 B/op，0 allocs/op |
| `POST /api/rooms/join` | 26.03 ns/op | 36.97 ns/op | 0 B/op，0 allocs/op |

```powershell
go test -v ./internal/telemetry -run '^TestHTTPMetricsMiddleware$'
go test ./internal/telemetry -run '^$' -bench '^BenchmarkHTTPMetricsObserve$' -benchmem -benchtime=400ms -cpu=1 -count=5
```
