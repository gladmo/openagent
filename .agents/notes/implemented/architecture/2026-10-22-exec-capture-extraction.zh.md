# Agent Note: 抽取 ExecCapture 使自定义环境复用 spill 语义

Status: implemented

[English](2026-10-22-exec-capture-extraction.md) | 中文

## Problem

spill 落盘此前是 `agent/harness/env/local` 的私有 `execCapture` 管线：feed 算法（缓冲输出块直到视图首次截断、冲刷缓冲前缀使文件包含完整输出、追加、发布路径、写失败时以 `spill_error` 失败整个 exec）只有 local env 能用。参考实现在其唯一的 `env/nodejs.ts` 里内联同款逻辑——单环境产品合理，但对作为通用 agent 框架的 openagent 不合理：每个自定义 `ExecutionEnv`（远程执行、容器、沙箱）都得重移植一遍算法，而这恰是副本最容易写错的部分（复用读缓冲、漏掉前缀冲刷）。

## Decision

把组合件上提为导出的 `harness.ExecCapture`（`utils_exec_capture.go`，openagent-native）：`NewExecCapture(env, options, onUpdate, ctx)` 以注入依赖的方式接收 env——spill 临时文件经该 env 的 `CreateTempFile` 落在其临时空间，对应参考的 `this.createTempFile`。接口就是读循环的形状：`Push`（界视图 + spill feed）、`Finish`（关文件）、`SpillError`（作为 exec 结果的闸门）、`Snapshot`（终视图，发布 onUpdate）。`env/local` 只保留进程编排并消费该组件；行为零变化——既有 spill 测试经 local env 照常通过，新增 harness 层测试用非 local 的 fake env 驱动组件（正是抽取所求的复用性证明）。

## Alternatives considered

**只抽 SpillWriter（capture 分开）。** 否决：调用方仍要编排 capture/spill 时序（push 与截断判断之间的 wasTruncated 握手）——接口变浅，把微妙部分推回给每个 env 作者。

**把 spill 折进 OutputCapture 本体。** 否决：会让目前纯内存的视图累加器耦合文件 IO，且放弃参考"env 从外部驱动 setSpillPath"的形状。

**保持 local 私有。** 否决：框架前提——`ShellOutputCaptureOptions.Spill` 是导出的 `ShellExecOptions` 契约一部分，实现 `Exec` 的每个 env 都会面对这个开关，却没有兑现它的机制。

## Consequences

- 自定义环境四个调用（NewExecCapture / Push / Finish / Snapshot）即得界视图 + 全量落盘；feed 算法只有一个家。
- `OutputCapture` 保持参考形状（`SetSpillPath` 外部驱动）；`ExecCapture` 是唯一的 openagent-native 增量，出处记于 PARITY。
- 同步写假设调用方的管道约束生产者；从无界来源 push 的环境需自带背压（类型上已注明）。
