# Toolset

[English](toolset.md) | 中文

`agent/harness/toolset/` 包把 harness 工具组合进裸 agent 循环。它是 openagent-native（非 1:1 移植；与 `agent/harness/compaction` 同出处类）：参考实现没有等价捆绑，因为其工具经 coding-agent 会话层加载。

## 接口

- **`Coding()`** 返回编码四件套——`read`、`write`、`edit`、`bash`——每次调用生成新工具。
- **`Adapt(toolset, env, gate)`** 是纯适配层：把 `*harness.AgentHarnessTool` 包装成 `*agent.AgentTool`，不创建、不拥有任何资源。已持有执行环境与闸门的调用方直接使用。
- **`NewLocal(cwd, toolset)`** 是捆绑层：一个 `local` 环境 + 一个闸门收进 `Local` 值。`AgentTools()` 喂给 `AgentOptions`（初始状态或逐轮）；`Close()` 是唯一且幂等的收尾路径。cwd 必须已存在——`NewLocal` 从不创建目录。

## 执行语义

每次适配后的调用走 pi 执行管线：`PrepareToolCall`（typebox 校验）→ `ApplyBeforeToolDecision`（默认无钩子）→ `ExecuteToolCall`（闸门准入、工具体 panic 转错误）→ `FinalizeToolCall`。循环的 `abort.Signal` 织进 chord `Context`，取消语义传播到文件与 Shell 操作。准入拒绝（`AbortRequested`、闸门关闭）以错误返回呈现——agent 循环的 `Execute` 契约——而不是逸散进循环。

失败或被阻塞的调用返回错误，错误文本携带工具输出（bash 失败保留 stdout/stderr）；由循环标记进消息历史。

## Close 顺序

`Local.Close` 只执行一次：`gate.Close(nil)` 停止准入，`CleanupShell` 杀死已跟踪的 shell 子进程，`Cleanup` 执行环境清理，`tools.ReleaseMutationQueue(env)` 释放该环境的文件变更队列状态（参考用 WeakMap 回收；Go 需显式释放）。错误以 join 汇总。

## 已知限制

- `Adapt` 为所有工具构造 `tools.ExecutionToolContext{Env: env}`；需要其它上下文类型的 harness 工具需自行适配。
- 捆绑层固定使用本地环境；远程执行环境属纯适配层，生命周期由调用方持有。
