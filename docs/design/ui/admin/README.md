# 后台界面静态稿

统一使用雨夜背景、纸质档案、切角按钮和红青双色状态。后台首页提供账号、题目、运行日志三个入口；内页只保留返回后台按钮。

| 页面 | 桌面图 | 手机图 |
| --- | --- | --- |
| 未登录 | `12-login.png` | `13-login-mobile.png` |
| 后台首页 | `01-home.png` | `08-home-mobile.png` |
| 账号列表 | `02-accounts.png` | `10-accounts-mobile.png` |
| 创建账号弹窗 | `03-create-account.png` | `09-create-mobile.png` |
| 修改密码弹窗 | `04-change-password.png` | — |
| 题目管理 | `05-soups.png` | — |
| 运行日志 | `06-logs.png`、`11-logs-full.png` | — |
| 请求链路弹窗 | `07-trace.png` | — |
| AI 链路弹窗 | `14-ai-trace.png` | — |

`index.html?page=home` 可浏览所有页面。`render.ps1` 从 HTML/CSS 重新截取 PNG。静态稿中的账号、题目和指标数字均为布局样例；AI 链路的 `ai.history`、`ai.model`、`ai.persist` 等阶段需要后端真实记录后才能在产品中显示。
