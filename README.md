# 多人海龟汤

用 Go、Vue 和 PostgreSQL 构建的海龟汤社区。支持注册登录、发布与互动、创建房间、真人主持的共探和竞答玩法。

## 文件树

```text
cmd/server/         Go 服务入口
cmd/admin/          本地管理员设置命令
internal/account/   注册、登录与会话
internal/forum/     汤题、互动与用户资料
internal/room/      房间与对局规则
internal/admin/     账号管理接口
internal/telemetry/ 请求追踪与结构化日志
migrations/         PostgreSQL 建表 SQL
web/src/components/ Vue 页面组件
compose.yaml        本地数据库
```

## 快速运行

需要 Go、Node.js 和 Docker Desktop。在项目根目录启动本地数据库：

```sh
docker compose up -d
```

启动后端（PowerShell）：

```powershell
$env:DATABASE_URL = "postgres://turtle:turtle_dev@127.0.0.1:5434/turtle?sslmode=disable"
go run ./cmd/server
```

另开终端启动网页：

```sh
cd web
npm install
npm run dev
```

游戏入口为 `http://127.0.0.1:5173/`，后台入口为 `http://127.0.0.1:5173/admin`。运行后端测试：`go test ./...`。

已有本地数据库时，启动后端会自动补建业务表；首次启动数据库会创建账号表。用本地命令初始化或重设超级管理员 `king`：

```powershell
$env:SUPER_ADMIN_PASSWORD = "设置你的密码"
go run ./cmd/admin
Remove-Item Env:SUPER_ADMIN_PASSWORD
```

后台可创建普通账号；超级管理员还可创建管理员。管理员只能管理普通账号，超级管理员可以管理管理员和普通账号。

后台的“运行日志”展示最近请求、各阶段耗时和日志文件占用。日志默认写入 `.local/logs/`，可通过 `LOG_DIR` 修改目录；请求与数据库查询不会记录正文、Cookie 和 SQL 参数。
