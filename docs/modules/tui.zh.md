# tui

[English](tui.md) | 中文

`tui` 是 pi-tui v0.87.1 的 Go 移植：终端 UI 基元——颜色、分段、按键、组件、差分渲染器与全屏管理。移植清单见 [PARITY.md](../../PARITY.md)；逐轮记录见 [PORTING.md](../../PORTING.md)。

## 颜色

- `colors.go` 在 OKLCH ↔ sRGB 间转换并按 `TerminalColorMode`（256color、truecolor）渲染 ANSI 序列（`ForegroundAnsi`/`BackgroundAnsi`）。
- `terminal_colors.go` 经 OSC 10/11 读写终端默认色。
- `ParseColor` 接受十六进制、`rgb()`、`oklch()` 与索引色规格；每种颜色都可转换为 sRGB，颜色运算不会失败。

## 文本分段

- `grapheme.go` 实现 UAX#29 字素簇（GB9c/GB11/RI 对齐）。`unicode_data.go` 持有生成的 UCD 16.0.0 表（GCB/WB/InCB/Extended_Pictographic/RGI emoji/EAW）；用 `scripts/gen-unicode-tables.mjs` 再生成。
- `word_segmenter.go` 实现 UAX#29 词分段。CJK 词典分段（ICU CLDR）为文档化偏差：默认逐字步进。
- `utils.go` 提供 `VisibleWidth`、`TruncateToWidth`、`WrapTextWithAnsi`、`SliceByColumn` 与 OSC-8 超链接跟踪。

## 按键与输入

- `keys.go` 解码 Kitty CSI-u、xterm modifyOtherKeys 与传统序列；`keybindings.go` 解析命名动作；`stdin_buffer.go` 分块读取 stdin。

## 组件

单行 `input` 编辑器、多行 `editor`（粘贴标记、kill 环、undo、跳字符、粘滞列光标）、基于 marked 兼容词法器的 `markdown` 渲染器、`latex` 渲染器、`select_list`/`settings_list`、`image` 处理（kitty/sixel/iTerm）、`stack`/`box`/`text` 布局基元、`autocomplete` 提供者集成。

## 屏幕

- `tui_main_screen.go` 从组件树差分渲染；`tui_alt_screen.go` 增加选区、搜索、滚动条与闪烁管理。
- 焦点引擎跟踪覆盖层恢复；16ms 调度器批量渲染。

## 已知限制

- `native-modifiers`/`native-platform`/`native-module-path`（NAPI）无 Go 等价物；剪贴板回退到 OSC52 + 系统命令。
- CJK 词典分段有偏差（默认逐字 UAX#29）。
