# 游戏端静态设计

[打开画廊](index.html) 可查看首页、账号、题目社区、题目详情、发布页、个人主页及两个弹窗。PNG 与同名 HTML 对应，页面共用 [theme.css](theme.css)。首页没有顶栏或侧栏；内页仅保留返回按钮。

在 Windows 上使用 Edge 重截页面：

```powershell
.\render.ps1
```

## 插画来源

`assets/evidence-collage.png` 由内置 ImageGen 生成，`../references/home-concept.png` 只作为画风参考。生成时启用透明背景，图片用于题目详情的局部证物插画；所有中文、按钮、表单和弹窗均由 HTML/CSS 排版。

生成提示词：

> Original cutout for a Turtle Soup mystery game's story detail page. Richly inked urban mystery graphic novel in warm cream paper, midnight plum outlines, coral and teal accents, subtle print grain. A top-down arrangement of three overlapping detective evidence objects: a blank aged case photograph showing a rainy street and an overturned teacup, a small folded blank paper note, and a brass magnifying glass partly over the photo. No written words, letters, numbers, UI labels, buttons, watermark, scene background, or people. Centered with transparent margin; crisp enough at 260px. Use the supplied H4 home concept as style reference only; do not copy its UI or text.
