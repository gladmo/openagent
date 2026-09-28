# Agent Note: Agent 事件分发串行化

Status: implemented

[English](2026-09-28-agent-emit-serialization.md) | 中文

## Problem

`Agent.processEvents`（[agent.go](../../../../agent/agent.go)）在 `a.mu` 下归约状态、解锁后才调用 listener，于是默认的并行工具执行（[agent_loop.go](../../../../agent/agent_loop.go) 的 `executeToolCallsParallel`）在模型响应携带两个及以上工具调用时，让 listener 分发——以及 `AfterToolCall` 钩子——在多个 goroutine 上同时运行。这违背了文档契约（"按订阅顺序串行调用"），并使任何 listener 触碰共享状态的消费者产生数据竞态，而这正是常态。入口路径上还有两个相邻缺陷：`NewAgent` 直接赋值 `options.StreamFn` 而无回退，依赖文档化 `SetDefaultStreamFn` 默认值的调用者在运行中段 nil 函数 SIGSEGV 而非响亮失败；工具返回 `(nil, nil)` 会让循环的收尾阶段 nil 解引用。

## Decision

`Agent` 上的事件分发端到端串行：`processEvents` 在状态归约与 listener 调用全程持有专用 `emitMu`。锁序固定为 `emitMu` → `a.mu`，且 `emitMu` 不在任何其他位置获取，不存在逆序路径。两个循环入口在首次 emit 之前经 `GetDefaultStreamFn()` 解析 nil `streamFn`，原样返回其错误——错误配置在最早可解析点浮出。`executePreparedToolCall` 把 `(nil, nil)` 的工具返回转换为 isErrored 结果，与 TS 循环转换抛错工具的方式一致。

## Alternatives considered

**把并行工具事件经单一 channel 汇聚到循环 goroutine 消费。** 暂时否决：它以构造方式保序，但每次 emit 都多一跳，且改动了 TS 参考在顺序/并行执行间共享的代码路径；互斥锁以更小 diff 交付同一契约。若未来需要跨工具的确定性事件顺序（而非仅串行化），channel 设计仍是正确形态。

**仅串行化 listener 调用，状态归约留在 `a.mu` 下。** 否决：listener 必须能观察到由其自身事件产生的状态，归约与分发必须是同一临界区。

**在 `NewAgent` 内解析默认流函数。** 否决：`NewAgent` 不返回错误，能用注册表的报错信息响亮失败的最早点是循环入口、任何事件 emit 之前。

## Consequences

并行工具下 listener 与 `AfterToolCall` 钩子保持按订阅顺序串行的契约，无需自行加锁；[severe_defects_test.go](../../../../agent/severe_defects_test.go) 用刻意不加锁的 listener 在 `-race` 下钉住该行为。在同一 agent 上同步触发再次 emit 的 listener 现在会死锁而非竞态——此类重入从未受契约支持，此前只是以破坏顺序的方式"能用"。
