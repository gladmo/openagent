# Agent Note: Automated task scheduling as a composed module (agent/harness/tasks)

Status: implemented

## Problem

移植后的框架是请求—响应式的：调用方发起 prompt，agent 循环跑完一轮，进程等待。仓库中没有任何机制能按时间规则（cron、固定间隔、一次性时间点）唤醒 agent、安全持久化任务定义、或对运行加以限流与观测。该能力不能放进 `codingagent`（它是钉在参考实现上的 CLI 端口），也不能分叉 agent 循环。

## Decision

`agent/harness/tasks/` 是 compaction 同源类的 openagent 原生包（新能力，无 TypeScript 对应物可移植）。它在小接口后面持有四个部件：`Schedule` 缝（cron/every/at）、带整文件原子替换与按行降级的 JSONL `Store`、`Executor` 缝（默认适配器 `AgentExecutor` 仅经 `PromptText`/`Abort`/`WaitForIdle`/`IsStreaming` 驱动 `agent.Agent`），以及单循环 goroutine 睡到最早下次触发、随 CRUD 唤醒的 `Scheduler`。任务从外侧组合：宿主代码提供 agent 工厂（`NewAgent`/`LoadAgent`）；codingagent 会话经既有公开缝 `AgentSessionConfig.Agent` 与 `CustomTools`（`CreateToolDefinitionFromAgentTool`）绑定，codingagent 零改动。

调度语义沿用 DeepSeek Harness cron 插件模型：错过不补跑（下次触发一律从 now 计算，迟到唤醒每任务至多补一次）、任务忙时新触发记为 `skipped` 运行、全局并发上限超额即拒绝。`every` 调度在注册时锚定并持久化锚点，触发网格永不漂移。fixed 会话的注入在会话层串行化（默认忙策略 `wait`），因为 agent 本就拒绝并发 prompt。

cron 解析器自研，语义对齐 robfig/cron v3.0.0 的 5 段子集（名称、列表、区间、步进、日字段 `?`、`@every`、dom 与 dow 同时受限的 OR 规则、4 年窗口），由移植自 robfig 测试预期的黄金向量钉死。源标记（调度注入 vs 用户输入）落在 `Fire.Trigger` 与 `TaskRun` 记录上；`ai.UserMessage` 保持不动。本包 `Scheduler` 与 `agent/harness/pico3` 的 scheduler（任务相位状态机）无关；`doc.go` 与模块页写明区分。

## Alternatives considered

### Why not implement it in codingagent?

需求明确排除，且端口把 codingagent 钉在参考实现上；放在那里的调度子系统会变成不可移植的偏差，纯 agent 消费方（`examples/data_query_agent`、`examples/coding_agent` 形态的宿主）也无法使用。

### Why not vendor robfig/cron v3 source under the package?

所需子集约 300 行；vendor 会拖进 6 段秒字段机制、我们未暴露的描述符管线，以及 MIT 声明义务，而行为仍需自建测试钉死。黄金向量钉住参考语义；`Schedule` 接口仍是日后换入 robfig 的缝。

### Why not per-task timers or a robfig Cron runner?

每任务定时链要把暂停/恢复/更新的簿记复制 N 份；robfig runner 自带 goroutine 与存储模型。单循环 + 每任务 `next` + wake 通道让错过收敛、暂停、更新都成为单一持锁变更，并让 `Now` 可注入供测试使用。

### Why not queue fires when the task is busy?

排队会掩盖过载并搅乱触发网格；带原因的 skipped 记录可观测，调用方可基于运行历史自建补偿。会话层串行化（busy-wait）只覆盖等待才是正确行为的场景。

### Why not one JSON document instead of JSONL?

损坏的 JSON 文档会丢失全部任务；逐行解码只降级受损记录（NFR-3），且 JSONL 与会话/轨迹存储约定一致。写入仍是整文件原子替换。

### Why not add a source field to ai.UserMessage?

`ai` 是钉死的端口；加字段是必须由黄金测试兜底的偏差。触发事实已经持久化在 `TaskRun`（与 `Fire`）里，可观测性所需即在此。

## Consequences

- 宿主进程拥有接线权：调度器随构造它的进程启停；重启把遗留 `running` 运行标记 `interrupted`，且从不补跑。
- 单进程假设：无 leader 选举或跨实例锁；每进程一个 store 文件。
- cron 粒度为分钟；秒级工作使用 `every`（受 `MinEvery` 下限约束，默认 10s）。
- `Executor` 已有两个真实适配器（测试 fake、`AgentExecutor`）；harness lane 驱动适配器日后可加入该缝而不改调度器。
- `TaskRun.RunID`（UUIDv7 字符串、无 kind）的形态可直接注册进未来的通用后台任务注册表。
- 自然语言入口（`task_manage`）是普通 `agent.AgentTool`，任何循环级消费方都能获得；codingagent 经其既有工具定义适配器包装它。
