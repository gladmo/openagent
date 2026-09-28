# 架构

[English](architecture.md) | 中文

openagent 如何组合、行为在哪里扩展。决策依据存放在 [Agent Note](../.agents/notes/README.md)；模块细节在[模块页](modules/README.md)；逐文件移植清单在 [PORTING.md](../PORTING.md) 与 [PARITY.md](../PARITY.md)。

## 总览

openagent 是 pi 单仓库 TypeScript agent 技术栈（`agent` v0.87.1 及其 `ai`、`chord`、`telemetry` 闭包；提交 `ff72faba2`）的 Go 1:1 重写。各包在命名与行为上镜像源码；npm 依赖变成独立的 Go 包；分层与基准一致：基础库、chord、ai 提供方栈、agent 循环。

## 模块地图

```
abort/        AbortSignal / AbortController 垫片，nil 安全
jsonx/        JS 语义 JSON 模型
typebox/      TypeBox 1.3.27 子集
partialjson/  partial-json 0.1.7 移植
diff/         jsdiff 8.0.4 移植
ignore/       npm ignore 7.0.8 语义
telemetry/    Span 契约，NOOP / 内存上下文
chord/        JSON 契约、context/、delta/、services/
ai/           类型、流式、重试、目录、提供方
agent/        Agent 循环、代理、搜索、harness/ 与工具
examples/     可运行的示例 agent
```

## 核心流程

- **提供方流：** 一次回合经 HTTP/SSE 接缝（`http_sse.go`）调用 `ai` 提供方；codec、重试与 transcript 把可中止的事件流整理为消息。
- **Agent 循环：** 回合 → 模型事件 → 工具调用 → 工具结果 → 下一回合，直到最终消息或中止。
- **状态增量：** `chord/delta` 操作应用在 `jsonx` 模型之上，顺序与数字格式和 JavaScript 完全一致。

## 扩展点

- **提供方：** 在 `ai/` 实现提供方核心，随其目录条目注册。
- **工具：** 在 `agent/harness/tools` 定义工具，由循环调度调用。
- **遥测：** 经 `telemetry/` 上下文定义 span 模式；内置 NOOP 与内存实现。

## 不变量

- 跨包边界的 JSON 一律走 `jsonx` / `chord`，保持 JS 语义：插入序、数字格式。
- 取消从控制器传播到提供方流；被中止的路径不遗留工作。
- 被移植行为由对照 npm 与 pi 基准的 golden 测试钉住；偏离即缺陷。
