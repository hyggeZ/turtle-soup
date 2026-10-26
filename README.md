# 多人海龟汤

用 Go、Vue 和 PostgreSQL 构建的海龟汤社区。注册后可发布汤面和汤底，选择本格/变格、难度与恐怖程度；其他用户可关注作者、点赞、收藏、评论。阅读汤题时可主动查看汤底。

## 文件树

```text
cmd/server/         Go 服务入口
internal/account/   注册、登录与会话
internal/forum/     汤题、互动与用户资料
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

打开 `http://127.0.0.1:5173`。运行后端测试：`go test ./...`。

已有本地数据库时，启动后端会自动补建论坛表；首次启动数据库会创建账号表。
