# Agent Note: 严重评审发现待办清单

Status: proposed

[English](2026-09-28-severe-review-findings-backlog.md) | 中文

## Problem

对初始导入（commit `d4e0b58`）的全量评审确认了约二十项严重缺陷。其中十三项崩溃类、竞态类与数据丢失类缺陷已随归属测试立即修复（见[存储并发](../../implemented/bug-fix/2026-09-28-storage-concurrency-and-close.md)、[agent 事件串行化](../../implemented/bug-fix/2026-09-28-agent-emit-serialization.md)、[pico3 重放编解码](../../implemented/bug-fix/2026-09-28-pico3-replay-jsonx-codec.md)）；其余各项经过验证与复现但尚未修复，若不落盘，评审证据只存在于一个终将结束的对话里。

## Proposal

按下表的簇顺序推进待办，每个簇落地时升级为各自的 implemented 记录。每一项都经过临时探针或 race detector 对被评审 commit 的复现，无猜测成分。

### Inventory

| # | 级别 | 簇 | 缺陷与位置 |
|---|---|---|---|
| 1 | FIXED — [runtime state machine](../../implemented/bug-fix/2026-09-28-runtime-state-machine.md) — | 严重 | runtime accept | 捕获的 steer/followUp/nextRun 项从未物化为条目，任何带排队项的 accept→start run 在 `StartRun` 必死（"Run prompt entry is missing its message"）；载荷遗留在 `pi.pending.entry` — [accept.go](../../../../agent/harness/runtime/accept.go) |
| 2 | FIXED — [runtime state machine](../../implemented/bug-fix/2026-09-28-runtime-state-machine.md) — | 严重 | runtime 持久化 | 持久 `op.state` 只序列化 ~25 个 `OperationState` 字段中的 8 个（丢 continuation、triggerEntryId、generationContext、responseEntryId、attempt、notBefore、poll…）— [lane_json.go](../../../../agent/harness/runtime/lane_json.go) |
| 3 | FIXED — [pico3 session line](../../implemented/bug-fix/2026-09-28-pico3-session-line.md) — | 高 | pico3 会话 | 数据竞态：全局 `sessionOwners` map 无锁；`LiveTasks`/`ConversationRecords` 由 tail 协程写、`Subtree`/`Ancestors` 无锁读；`listeners` 无锁遍历 — [session.go](../../../../agent/harness/pico3/session.go) |
| 4 | FIXED — [pico3 session line](../../implemented/bug-fix/2026-09-28-pico3-session-line.md) — | 高 | pico3 会话 | 重入 `Session.Commit`（来自 listener 或 commit 回调）在单一 tail 协程上永久死锁 — [session.go](../../../../agent/harness/pico3/session.go) |
| 5 | FIXED — [pico3 session line](../../implemented/bug-fix/2026-09-28-pico3-session-line.md) — | 高 | pico3 会话 | `Close` 从不关闭 tail 队列：每会话泄漏一个 goroutine + 1024 槽缓冲 — [session.go](../../../../agent/harness/pico3/session.go) |
| 6 | FIXED — [pico3 session line](../../implemented/bug-fix/2026-09-28-pico3-session-line.md) — | 高 | pico3 tracker | `legacy_tracker` 浅克隆使嵌套就地变更同时别名 base 与 working；`Flush` 产出零 ops — [legacy_tracker.go](../../../../agent/harness/pico3/legacy_tracker.go) |
| 7 | FIXED — [ai transport hardening](../../implemented/bug-fix/2026-09-28-ai-transport-hardening.md) — | 高 | ai copilot | Copilot 动态头（`X-Initiator`/`Openai-Intent`/vision）从未接入 anthropic-messages 传输 — [api_anthropic_messages.go](../../../../ai/api_anthropic_messages.go) |
| 8 | FIXED — [abort atomicity](../../implemented/bug-fix/2026-09-28-abort-any-atomicity.md) — | 高 | abort shim | `abort.Any()` 检查-后-注册竞态静默丢失 abort（压测 20 万次丢 123 次）— [abort.go](../../../../abort/abort.go) |
| 9 | FIXED — [runtime state machine](../../implemented/bug-fix/2026-09-28-runtime-state-machine.md) — | 高 | runtime lane | 包级 `laneNameHolder` 竞态，可能把 A lane 的 tip 写到 B lane 名下 — [boundary.go](../../../../agent/harness/runtime/boundary.go) |
| 10 | FIXED — [runtime state machine](../../implemented/bug-fix/2026-09-28-runtime-state-machine.md) — | 高 | runtime lane | `QueueMessage`/`CancelQueued` 抹掉持久 `lastOperationId`；`AcceptRun` 从不推进持久 `branch.tip`；`emitEvents` 无 lane 互斥锁追加 — [queue.go](../../../../agent/harness/runtime/queue.go)、[accept.go](../../../../agent/harness/runtime/accept.go)、[lane.go](../../../../agent/harness/runtime/lane.go) |
| 11 | 未修（部分） | runtime 杂项 | 元数据捕获与退避 abort 感知：FIXED — [runtime state machine](../../implemented/bug-fix/2026-09-28-runtime-state-machine.md)。剩余：`PlanResponseWrites` 以 nil parent 构造响应条目（接线后转录孤儿化）— [response_settle.go](../../../../agent/harness/runtime/response_settle.go) |
| 12 | FIXED — [ai transport hardening](../../implemented/bug-fix/2026-09-28-ai-transport-hardening.md) — | 中 | ai 传输 | 传输层错误（连接拒绝/DNS/重置）从不重试——`Status == 0` 的 SDK 对齐分支不可达；anthropic delta 与块类型不匹配时空引用击穿流；codec/frame 解码对畸形持久 JSON panic（命中崩溃恢复路径）；frame 编码 rune/byte 混算（非法 UTF-8 delta）；codex 重试泄漏 `abort.Any` 监听器 — [provider_retry.go](../../../../ai/provider_retry.go) 等 |
| 13 | 中 | 移植库 | jsonx 孤立代理项与溢出指数偏离 JS；`Stringify` 对非 string 键 map panic；chord delta 空路径 op panic；typebox Integer ≥2^63 误拒与 UTF-16 长度计数；构建器约束选项只序列化不校验；ignore 指数回溯 — 见 ai/libs 评审记录 |

## Alternatives considered

**在外部 tracker 按缺陷逐条建 issue。** 否决：本仓库持久决策的家是 Agent Notes；树外的 tracker 会与它描述的代码漂移。

**先一口气全修再记录。** 否决：runtime 各簇需要设计决策（重入语义、sidecar 退役），应在记录在案的清单上决策，而非凭对话记忆。

**只记录簇、不记录逐缺陷行号。** 否决：file:line 证据正是让每一项无需重新评审即可行动的关键；丢掉它等于重付评审成本。

## Acceptance criteria

- 第 1–2、9–11 项（runtime 簇）：行为由 `agent/harness/runtime/` 的归属测试钉住，每簇决策记入各自的 implemented 记录。
- 第 3–6 项（pico3 会话簇）：`go test -race` 覆盖并发的 `Subtree`/`Ancestors`/listener 使用；重入 commit 得到解决而非挂死；`Close` 不遗留 goroutine。
- 第 7–8、12–13 项：每个修复附带评审中的复现测试（传输重试分类、wire 上的 copilot 头、`abort.Any` 压测、解码器 panic）。
- 本记录随各簇落地迁移至 `implemented/`（或按簇拆分并交叉链接），遵循 supersession 规则。

## Risks

行号会随代码移动老化；把位置视作入口点而非精确坐标。runtime 的 accept/持久化两项（1–2）描述的是 P7 期语义，可能被有意重新规划——若如此，应将该两项的重新规划记录为 rejection，而非静默丢弃。修复 pico3 会话重入（第 4 项）可能改变 listener 顺序语义；该决策做出时需要独立记录。
