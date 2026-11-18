# 第 122 条 Prometheus 运行指标

后台链路日志适合查看一次请求的具体阶段。Prometheus 拉取按操作累计的请求数和耗时分布，适合查看错误率、P95 和应用重启前后的趋势。两者共用请求计时与 AI 判定阶段，指标抓取不读取数据库或日志文件。

- `room_join` 记录 `/api/rooms/join` 的请求耗时，2xx 计成功、4xx 计拒绝、其余状态计错误。
- `ai_decision` 使用已有 `ai.decision` 阶段，包含在该阶段内的历史读取、模型调用和输出校验，按阶段是否失败计数。重复操作回放、揭底、本阶段之前被拒绝的请求均没有这个阶段。
- 直方图只有 5 种固定的 `operation` 和 `outcome` 组合。计数来自直方图的 `_count`。预先绑定标签，避免在请求热路径中按标签查找序列。指标中没有房间标识、账号、题目内容或错误正文。[Prometheus 指标命名与标签](https://prometheus.io/docs/practices/naming/)

## 抓取配置

在应用启动环境中设置 `METRICS_ADDR=127.0.0.1:18085`，在同一主机运行 Prometheus。默认关闭。监听地址只接受回环 IP，指标仅在独立端口的 `/metrics` 上开放。

```yaml
global:
  scrape_interval: 15s
scrape_configs:
  - job_name: turtle-soup
    static_configs:
      - targets: ["127.0.0.1:18085"]
```

```promql
histogram_quantile(0.95, sum by (le) (rate(turtle_operation_duration_seconds_bucket{operation="ai_decision"}[5m])))
sum(rate(turtle_operation_duration_seconds_count{operation="ai_decision",outcome="error"}[5m])) / sum(rate(turtle_operation_duration_seconds_count{operation="ai_decision"}[5m]))
```

P95 由直方图的桶估算。应用进程重启后计数归零，Prometheus 保留此前已抓取的样本。两次抓取间若应用退出，尚未抓取的增量无法恢复。[Prometheus 直方图说明](https://prometheus.io/docs/practices/histograms/)

## 真实抓取回归

使用 Prometheus 3.13.5 LTS、1 秒抓取间隔与本地测试服务，业务响应由固定夹具生成，计时和指标复用生产代码。切换测试服务进程期间保持 Prometheus 和临时 TSDB 运行。

- 重启前抓到 9 次加入成功、3 次加入拒绝、3 次 AI 判定失败。PromQL 查得 AI 判定失败占比 25%，直方图 P95 为 24.25 ms。
- 重启后，同一条加入成功序列先降到 0，随后收到 2 次新请求。`max_over_time` 仍查得到重启前的 9 次，`resets` 识别出 1 次重置。
- 自动测试覆盖指标隔离、固定标签、并发更新计数、非回环地址拒绝、业务端口访问 `/metrics` 返回 404，以及没有判定阶段的重放不会计为 AI 调用。

## 观测开销

Go 1.26.5，Windows/amd64，`GOMAXPROCS=4`。每组 250 ms，重复 7 轮。对照只计时观测分支与直方图写入。

| 操作 | 关闭（ns/op） | 开启（ns/op） | 分配变化 |
| --- | ---: | ---: | ---: |
| 单线程加入房间 | 1.03 | 25.13 | 0 B、0 次 |
| 单线程 AI 判定 | 1.07 | 26.07 | 0 B、0 次 |
| 4 个 goroutine 更新同一房间加入序列 | 0.23 | 112.30 | 0 B、0 次 |

开启指标会增加少量请求内开销，收益是可抓取的趋势和故障比例。微基准中的并行数据按总操作数计算，用于比较争用成本。

另用真实 Gin 中间件和 JSONL 写入做完整请求对照。单线程，每组 300 ms，重复 5 轮。房间和 AI 业务结果由夹具生成，未在这组基准中访问数据库、模型或抓取端点。

| 操作 | 关闭中位数（范围，µs/op） | 开启中位数（范围，µs/op） | 分配次数 |
| --- | ---: | ---: | ---: |
| 加入房间被拒绝 | 15.15（14.65–15.35） | 14.28（14.20–15.52） | 25 → 25 |
| 含判定阶段的 AI 请求 | 16.76（15.87–17.45） | 16.70（16.46–18.78） | 27 → 27 |

两组测量范围交叠，未测出完整请求耗时有稳定变化。
