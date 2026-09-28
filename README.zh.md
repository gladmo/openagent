# openagent

[English](README.md) | 中文

pi agent 技术栈的 Go 1:1 重写——`agent`、`ai`、`chord`、`telemetry` 各包及其依赖闭包（TypeBox、jsdiff、ignore、partial-json 均重写为纯 Go）——保持模块命名、行为与 JavaScript JSON 语义一致。[PORTING.md](PORTING.md) 跟踪移植阶段；[PARITY.md](PARITY.md) 逐文件审计与 TypeScript 基准的对齐状态。

## 从这里开始

- [架构](docs/architecture.zh.md)：模块与流程的有序地图。
- [AGENTS.md](AGENTS.md)：适用于每次变更的常驻指令。
- [开发指南](docs/development.zh.md)：环境搭建与日常工作流。
- [Agent Notes](.agents/notes/README.md)：决策记录；可复用工作流位于 [.agents/skills/](.agents/skills/)。

## 验证

```sh
go build ./... && go vet ./... && go test ./...
node scripts/run-gates.mjs
```

Go 负责编译、静态分析与行为测试；零依赖的 Node 门禁在 Node ≥ 18 上负责提交卫生、双语文档配对与决策记录格式。
