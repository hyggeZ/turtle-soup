# 玩法页面静态图

打开 [gallery.html](gallery.html) 查看大厅、建房、等待、共探、竞答、主持、看汤底申请和单人模式的静态图。`index.html?view=casual` 等页面仅用于设计验收，不连接后端。运行 `render.ps1` 可重新导出 `screens/` 中的 PNG。

## 线索插画来源

`assets/evidence-cup-watch.png` 由内置 ImageGen 工具生成，参考 `../references/room-concept.png` 的雨夜漫画画风。生成提示词：

> Use case: illustration-story. Asset type: in-game mystery evidence photograph for a Turtle Soup deduction game's room detail card. The room concept is a style reference only, not an edit target. Match its rain-soaked noir comic illustration, warm coral and cyan against dark plum, inked outlines, subtle halftone texture. Create a close-up landscape 4:3 evidence scene with one overturned metal cup, a small pocket watch and a thin puddle of dark red liquid on wet stone floor. Warm lamplight against cold rainy reflections. No text, UI, frame, people, logos or watermark. Original art, not a copy of the reference.

生成后从 Codex 的 `generated_images` 目录复制到本目录，原始生成文件未删除。界面文字、票券按钮、纸张卡片和表单由 HTML/CSS 绘制。
