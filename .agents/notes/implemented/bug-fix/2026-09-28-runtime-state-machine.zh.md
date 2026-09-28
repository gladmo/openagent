# Agent Note: runtime 状态机正确性

Status: implemented

[English](2026-09-28-runtime-state-machine.md) | 中文

## Problem

lane 状态机的七项已确认缺陷：`AcceptRun` 把排队的 steer/followUp/nextRun id 捕获进 run intent，却只物化 prompts，任何带排队项的 accept→start run 在 `StartRun` 必死（"Run prompt entry is missing its message"），pending 载荷成为孤儿；持久 `op.state` 编解码只序列化 ~25 个 `OperationState` 字段中的 8 个，崩溃恢复静默丢失操作位置（continuation、trigger、生成上下文、重试调度）；包级 `laneNameHolder` 使跨 lane 提交竞态，可能把 A lane 的 tip 写到 B lane 名下；`QueueMessage`/`CancelQueued`/`AcceptRun` 以 `lastOperationId: nil` 写持久 lane.state，抹掉上一结果链接；accept 从不推进持久 `branch.tip`，snapshot 与恢复丢失 run 的开篇转录；`emitEvents` 不加 lane 互斥锁就向 sink 追加；`StreamHarnessAssistant` 在 provider 产生响应元数据之前捕获（外加数据竞态），`AfterResponse` 钩子永远看到零元数据。重试退避的 sleep 也不感知 abort。

## Decision

accept 物化整个捕获：prompts 与选中的 inbox 项按同一条链从 tip 链成条目（message 载荷与投影 write 一律经 `PendingEntryWrite`），消费掉的 pending 载荷删除，内存 tip 与持久 `branch.tip` 一并推进到末条。`StartRun` 按"条目存在性"解析 intent id——message 条目加入 prompt，物化的 write 条目作为转录上下文但不产出 prompt 消息。op.state 编解码对全部 `OperationState` 字段对称（jsonx 值保持 jsonx 模型）。`PlanBoundaryInbox` 以参数接收 lane 名，全局变量删除。队列写入把 `state.LastOperationID` 透传，queue_update 事件携带提交后的队列（暂存项因其载荷在计划期尚未持久化而直接并入快照）。`emitEvents` 取 lane 互斥锁；响应元数据加互斥锁并在钩子调用时读取；退避等待观察 drive 的 abort 信号。

## Alternatives considered

**在 StartRun 里解析 pending 载荷而非在 accept 物化。** 否决：载荷在 accept 时已被移出 inbox，边界规划器同样永远不会物化它们；在 accept 物化使 pending 载荷恰好只有一个消费者，accept 提交成为"排队对话变为转录"的原子点。

**用 encoding/json 按 json tag 序列化 OperationState。** 否决：`*jsonx.Obj` 字段无法经结构体解码往返（未导出状态）——与 pico3 重放记录关闭的是同一类"静默变空"；手写编解码保留。

**保留 laneNameHolder 但加互斥锁。** 否决：串行化能止住数据竞态，却止不住跨 lane 的逻辑损坏——tip 写入必须命名产生该提交的 lane，只有按调用传递才能保证。

## Consequences

带排队对话接受的 run 正常启动并正确重放；崩溃恢复找回完整操作位置；并发驱动的两个 lane 不会互相破坏 tip（-race 钉住）；队列写入保留 last-result 链接，事件反映 watcher 实际可见的状态。`PlanBoundaryInbox`/`durableStateJSON` 签名有变（预稳定面）。捕获项从未被物化的旧会话在 StartRun 仍会失败——修复前同样失败，不存在可回归的存活调用方。
