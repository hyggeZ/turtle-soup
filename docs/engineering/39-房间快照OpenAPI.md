# 第 260 条 房间快照 OpenAPI

将 `GET /api/rooms/{id}` 写成 OpenAPI 3.1 文档，为网页端和后续 App 提供同一份可读、可校验的 HTTP 接口说明。接口文档和校验脚本集中在 `docs/api`。规范覆盖 Cookie 会话、路径参数、历史与待办游标、按角色可见的字段，以及相应 HTTP 状态码。 [OpenAPI 3.1.1 规范](https://spec.openapis.org/oas/v3.1.1.html)

Vue 与 Go 在第 245 条已共用 9 个房间快照场景。Go 集成测试验证原契约后，可以导出这些请求的完整响应。文档测试读取导出文件，对照 OpenAPI 核对 HTTP 状态、查询参数和完整 JSON 的类型与字段。两个检查接在同一个 GitHub 作业里，导出数据来自一次性 PostgreSQL 中的合成房间。

## 独立验收与耗时

Go 使用隔离 PostgreSQL 16 生成了 9 份实际响应，导出文件共 6,036 字节。其中 6 份为 200，另外 3 份分别为 400、401 和 404。规范还声明了 409 和 500。

- 文档由 OpenAPI Initiative 的 2026-11-05 版 3.1 元 Schema 验证。Ajv 以文档定义为准，校验查询参数与真实响应。 [官方 Schema 索引](https://spec.openapis.org/oas/) · [Ajv 的 JSON Schema 支持](https://ajv.js.org/json-schema.html)
- 9 份真实响应全部通过。数字游标改为字符串、丢失 `events`、使用未声明的状态码、传入非整数查询游标和删除规范中的 API 版本共 5 种变更，都被对应校验发现。
- Windows Node 22.14.0 暖机后独立执行 9 轮完整文档测试，每轮 16 项均通过。含 Node 启动的墙钟中位数为 1.557 秒，范围为 1.250–1.903 秒。增加 3 个直接开发依赖，含传递依赖共 9 个，集中在 `docs/api`。

在 Windows 上用同一个 Go 测试二进制，交替运行 5 组关闭和启用导出的测试。每次都新建隔离 schema，通过本地 SSH 转发访问 PostgreSQL，计时包括启动、迁移、造数、请求与清理。

| 测量项 | 关闭导出 | 启用导出 |
| --- | ---: | ---: |
| 全组耗时中位数 | 2.691 s | 2.579 s |
| 五次样本范围 | 2.557–4.538 s | 2.457–2.759 s |

启用导出后会在测试结束时额外写入一份 JSON；这组计时包含数据库与连接波动，适合复核回归的运行成本。规范和校验依赖进入文档与 CI，线上请求不执行这些检查。

未设置 `SNAPSHOT_CONTRACT_OUTPUT` 时，`npm test` 会单独校验 OpenAPI 文件。设置该变量后，导出文件不存在或场景缺失会使测试失败。

```powershell
$env:TEST_DATABASE_URL = 'postgres://测试用户:测试密码@127.0.0.1:15432/独立测试库?sslmode=disable'
New-Item -ItemType Directory -Force .local | Out-Null
$env:SNAPSHOT_CONTRACT_OUTPUT = Join-Path (Get-Location) '.local/snapshot-contract-results.json'
go test -mod=readonly -count=1 ./internal/room -run '^TestRoomSnapshotContractV1$'
Push-Location docs/api
npm ci
npm test
Pop-Location
```
