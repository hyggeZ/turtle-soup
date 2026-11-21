# 第 193 条 GitHub 自动回归

`main` 每次推送触发 CI，支持在 GitHub Actions 手动发起同一工作流。两个作业并行执行，GitHub 令牌只读，官方 Actions 固定到具体提交。工作流承担测试与构建验证。

- Go 作业启动临时 PostgreSQL 16，配置 `TEST_DATABASE_URL` 后执行 Go 单元测试、数据库集成测试和 `go vet`。测试包串行执行，避免共用测试库时相互干扰迁移锁。包内的并发用例继续运行。[GitHub PostgreSQL 服务容器](https://docs.github.com/en/actions/tutorials/use-containerized-services/create-postgresql-service-containers)
- Vue 作业使用 Node.js 22 和 `web/package-lock.json`。依次执行 `npm ci`、14 项单元测试、现有 TypeScript 检查和 Vite 构建。

## 本地预验收

冻结 `8c85ba6` 的独立副本，用 Go 1.26.5、Node.js 22.14.0 和临时 PostgreSQL 16.15 跑完两组命令。Go 测试和校验分别为 153.83 秒、7.64 秒。Vue 空目录首次安装 35.66 秒、测试 2.16 秒、构建 8.34 秒。两组命令在同一主机并行运行，耗时作为本机记录。

在副本分别加入一个会失败的 Go 用例和一个受类型检查的赋值错误，两者均返回非零退出码。错误夹具退出后从副本清理。工作流通过 `actionlint` 1.7.12 检查。

## GitHub 托管验收

提交 `14bf577` 的 `push` 触发首次运行，两个作业和所有检查均成功。运行记录从创建到完成约 99 秒。[GitHub Actions 运行记录](https://github.com/hyggeZ/turtle-soup/actions/runs/44851267298)

| 阶段 | 托管环境耗时 |
| --- | ---: |
| Go 作业总耗时 | 94 秒 |
| PostgreSQL 容器初始化 | 12 秒 |
| Go 测试与集成验证 | 67 秒 |
| `go vet` | 3 秒 |
| Vue 作业总耗时 | 11 秒 |

首次运行的成功样本为 1/1，本地故障夹具识别 2/2。后续可按同一工作流的历史运行统计长期通过率。
