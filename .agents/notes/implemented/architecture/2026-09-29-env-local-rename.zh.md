# Agent Note: harness ExecutionEnv 包由 env/nodejs 改名为 env/local

Status: implemented

[English](2026-09-29-env-local-rename.md) | 中文

## Problem

1:1 移植把参考实现 `harness/env/nodejs.ts` 的文件名保留成了 Go 包 `agent/harness/env/nodejs`。在 TypeScript 参考里，这个名字按宿主运行时区分同一 `ExecutionEnv` 接口的不同实现（Node.js 版与潜在的 Deno/浏览器版）。Go 移植只有一种宿主实现，且完全构建在 `os`、`os/exec`、`syscall` 之上——不涉及任何 Node.js 运行时——这个名字因此宣告了一个并不存在的依赖，误导纯 Go 代码库的读者。

## Decision

包现位于 `agent/harness/env/local`，包名 `local`，类型 `LocalExecutionEnv`，构造函数 `local.New(cwd, ...)`（`WithShellPath`/`WithShellEnv` 不变）。行为与参考的钉定关系不变：包注释与类型注释保留对 `nodejs.ts` / `NodeExecutionEnv` 的溯源，PARITY.md 记录映射，自有测试为搬迁（改名）而非重写。这是纯改名——零行为变更。未来的其他执行环境（in-memory、sandbox）以 `env/<name>` 平行命名。

## Alternatives considered

**保留 `nodejs` 名字，仅在注释中澄清。** 放弃：每个新读者都要为误解付一次成本；注释能纠正意图，但 grep、import 列表与 godoc 会永远展示误导性的公共标识符。

**命名为 `env/native`。** 放弃："native" 语义模糊（相对于什么原生——CPU？操作系统？cgo？）；"local" 说出的才是真实区分——真实本机文件系统环境，与 in-memory 或 sandbox 环境相对。

**命名为 `env/osexec`。** 放弃：把实现细节（`os/exec` 支撑）固化进公共 API 名；该包的契约是 `ExecutionEnv` 接口，不是实现机制。

**在旧 import 路径保留废弃别名包。** 放弃：项目处于 pre-stable（AGENTS.md：公共 API 跟随参考、可变），唯一使用方是 coding_agent 示例与包自身测试，且别名违反 one-home-per-fact。

## Consequences

import 路径 `agent/harness/env/nodejs` 已消失；使用方（examples/coding_agent）与自有测试、PARITY.md、PORTING.md 的提及按仓库 API 变更规则在同一提交内更新。目录与文件重命名经 `git mv` 保留历史。验证：`gofmt -l .`、`go vet ./...`、`go build ./...`、`go test ./agent/harness/env/... ./examples/coding_agent/...`、`node scripts/run-gates.mjs`。创建时的 supersession 检查：无既有 note 覆盖 env 包命名。
