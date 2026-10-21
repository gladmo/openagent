# Agent Note: 把 harness→agent 循环的工具适配器放进独立的 toolset 包

Status: implemented

[English](2026-10-21-toolset-adapter-seam.md) | 中文

## Problem

把 harness 工具装进裸 `agent.NewAgent` 循环，每个调用方都要手工拼装五件事：创建执行环境（并记住清理）、创建闸门（两个视图，加 `Close`）、挑选工具工厂、手写管线适配器（Prepare → Before → Execute → Finalize、abort→chord 上下文织入、更新回调适配、错误文本提取）、再循环塞进 `AgentOptions`。仓库里唯一的实现在 `examples/coding_agent`，新消费者只能复制——而复制品恰是微妙契约所在：agent 循环的 `Execute` 必须以错误返回，管线却以解栈信号表达准入拒绝。

## Decision

把适配器抽到 `agent/harness/toolset`（模块页：[docs/modules/toolset.zh.md](../../../../docs/modules/toolset.zh.md)），两级接口：`Adapt(toolset, env, gate)` 是纯适配层，服务已拥有 env 与 gate 的调用方；`NewLocal(cwd, toolset).AgentTools()/Close()` 把一个本地环境加一个闸门捆在单一幂等 disposer 之后（`Close` 顺序：gate → shell 子进程 → 环境清理 → 变更队列释放）。`Coding()` 提供 read/write/edit/bash 四件套。该包是 openagent-native（与 `agent/harness/compaction` 同出处类），非 1:1 移植。

在适配 seam 上，准入 panic（`*harness.AbortRequested`、闸门关闭）转为错误返回：这是循环 `Execute` 的契约。管线自身的 panic 转换只覆盖工具体；准入 panic 逸出适配器会击穿没有 recover 的循环。

## Alternatives considered

**放进 `agent/harness` 根包。** 因导入图否决：适配器需要 `tools.ExecutionToolContext`，而 `tools` 导入 `harness`，根包会成环。

**放进 `agent/harness/tools`。** 为保持该 1:1 移植包镜像参考的 `harness/tools/*` 而否决；适配器没有 TS 对应物（参考经 coding-agent 会话层加载工具）。

**只做 `Local` 捆绑层。** 否决：藏起 env/gate 的创建就失去了纯适配层（跨工具集共享 gate、远程 env 的调用方），捆绑层也无法脱离文件系统生命周期做测试。

**只做纯 `Adapt`。** 否决：捆绑层才是本次诉求的要点——三行从无到 `AgentOptions.Tools`——且 disposer 约定（"registrations are effects"）要求 env + gate + 队列状态有唯一 owner。

## Consequences

- `tools.ReleaseMutationQueue(env)` 成为导出 API：移植用普通 map 替换了参考的 WeakMap，队列状态按 env 泄漏；捆绑层的 `Close` 现在补全注册-清理约定。
- `Local.Close` 同时执行 `CleanupShell`（杀死已跟踪子进程），这是原示例遗漏的。
- `Adapt` 为所有工具构造 `tools.ExecutionToolContext{Env: env}`；需要其它上下文类型的工具须自行适配（文档化限制）。
- 示例降为捆绑层的展示；其管线级测试继续跑在 `toolset.Adapt` 上。
