# 多人海龟汤

用 Go 和 Vue 构建的多人海龟汤。玩家可通过房间码加入，由主持人回答「是」「否」「无关」。看过汤底的玩家不能在同一局继续猜题。

## 文件树

```text
cmd/server/         Go 服务入口
internal/account/   注册、登录与会话
migrations/         PostgreSQL 建表 SQL
web/                Vue 网页
compose.yaml        本地数据库
```

## 快速运行

需要 Go、Node.js 和 Docker Desktop。在项目根目录启动本地数据库：

```sh
docker compose up -d
```

启动后端（PowerShell）：

```powershell
$env:DATABASE_URL = "postgres://turtle:turtle_dev@127.0.0.1:5433/turtle?sslmode=disable"
go run ./cmd/server
```

另开终端启动网页：

```sh
cd web
npm install
npm run dev
```

打开 `http://127.0.0.1:5173`。运行后端测试：`go test ./...`。
