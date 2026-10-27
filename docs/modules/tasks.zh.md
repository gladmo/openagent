# Tasks（自动化任务）

[English](tasks.md) | 中文

`agent/harness/tasks/` 包（openagent 原生，非 pi 端口）是自动化任务调度器：按 cron / 固定间隔 / 一次性时间规则触发已注册的任务定义，并通过 `Executor` 缝驱动每条 Fire 完成一轮完整 Agent 执行。Agent 循环本身不做任何修改；默认执行器把任务 prompt 作为用户消息注入并等待运行收敛。

## Composition

- **Schedule**（`schedule.go`、`schedule_cron.go`）—— `Next(after)` 返回严格晚于 `after` 的下次触发时间（耗尽时 `ok=false`）。适配器：`ParseSchedule`（5 段 cron 或 `@描述符`，本地时区；`ParseScheduleIn` 可传 `*time.Location`）、`Every`（锚点 + k·间隔，无漂移）、`At`（一次性）。cron 语法是 robfig/cron v3.0.0 的 5 段子集——名称、列表、区间、步进、日字段的 `?`、`@every <duration>`——并沿用 robfig 的 dom 与 dow 同时受限按 OR 的规则与 4 年匹配窗口；秒字段与 Quartz 的 `L`/`W`/`#` 响亮失败。黄金向量在 `schedule_cron_test.go` 钉死语义。
- **TaskSpec**（`spec.go`）—— 持久化的任务定义：`ID`、`Name`（注册时缺省取 ID）、`Schedule`、`SessionStrategy`（默认 `new` | `fixed`）、`FixedSessionID`、`Prompt`、`Timeout`、`Enabled`、`MaxRuns`（0 = 不限）、`Metadata`。JSON 以判别式 union 编码调度（`{"type":"cron"|"every"|"at", ...}`）；未知类型解码失败。`Validate` 拒绝缺失 id/schedule/prompt、未知策略、fixed 无会话 id、负 timeout/maxRuns。
- **Store**（`store.go`）—— `FileStore` 每任务一行 JSONL（`TaskRecord`：spec、`RunCount`、按时间升序的受限 `History`），整文件原子替换（临时文件 + rename）。`Load` 按行降级：损坏或非法记录变成 `StoreIssue`，不阻断启动；文件不存在视为空库。`MemoryStore` 供测试与临时进程。
- **Executor**（`executor.go`）—— `Run(ctx, Fire) RunResult`；`Fire` 携带 prompt、`Trigger`（`schedule` | `manual`）、触发时间与会话策略。`AgentExecutor` 是 `agent.Agent` 之上的默认适配器：`NewAgent` 为 `new` 策略构造全新会话（分配新的 UUIDv7 会话 id）；`LoadAgent` 跨进程恢复 fixed 会话（结果按进程缓存）。fixed 会话忙策略：`wait`（默认——运行等待会话空闲，受运行 context 约束）或 `skip`。超时/取消调用 `Abort()` 并在 `CancelGrace`（默认 10s）内等待收尾，随后返回 `failed`/`interrupted`；提供方失败经由合成错误助手消息浮出为 `failed` 并带该消息。`ExecutorFunc` 把普通函数适配成这条缝。
- **宿主管线**（`executor_pipeline.go`）—— `PipelineSubmitter` 把一条 Fire 作为异步、可取消的工作单元落到宿主自身的执行管线（队列准入的回合、任务运行器）：`Submit` 解析会话策略（会话域归宿主所有）并返回 `PipelineSubmission`——`Done` 恰好投递一次终态 `RunResult`，`Cancel` 尽力中止排队中或运行中的提交。`NewPipelineExecutor` 承担公共的等待、取消与分类：Submit 出错让该次运行响亮失败；context 超时在 `CancelGrace`（默认 10s）排空后报告 `failed: timeout after …`、普通取消报告 `interrupted`——context 过期时该分类优先于宿主结果。
- **Scheduler**（`scheduler.go`）—— `NewScheduler(SchedulerOptions)` 校验 `Store`/`Executor` 缝并集中解析缺省：`MaxConcurrentRuns` 4、`HistoryLimit` 20、`DefaultTimeout` 5m（0 关闭）、`MinEvery` 10s、`Now` 时钟缝。`Start` 加载 store、把遗留 `running` 运行标记 `interrupted`、启动循环 goroutine；`Stop` 取消循环，先按自身 context 排空在途运行，超时再取消它们。CRUD：`Register`、`Update`（保留历史、从 now 重新排程）、`Unregister`（在途结果丢弃）、`Pause`/`Resume`（单一 `Enabled` 开关；恢复从 now 重算）、`RunNow`（同步手动触发，不影响排程）。所有变更先持久化再改内存；触发路径的持久化为尽力而为并以 `store_degraded` 事件浮出。
- **Events**（`events.go`）—— `OnEvent(listener)` 返回注销函数；监听器在调度器锁释放后内联执行（可回调调度器）、串行分发，监听器 panic 被隔离。事件集：`task_registered/updated/deleted/paused/resumed`、`fired`、`run_started`、`run_skipped`、`run_finished`、`store_degraded`。
- **task_manage**（`tool.go`）—— `TaskManageTool(scheduler) *agent.AgentTool`，自然语言入口：动作 `create/list/get/update/pause/resume/run/delete`；task 参数携带调度表达式字符串。非法请求以工具错误响亮失败。

## Scheduling semantics

- 错过不补跑：重启、恢复、注册、更新一律从 now 计算下次触发；一次迟到的唤醒每任务至多补一次，随后收敛到 `Schedule.Next(now)`。
- 同任务重叠被拒绝：上一运行未结束时的 Fire 记为 `skipped` 运行并附原因。全局信号量（`MaxConcurrentRuns`）超额时同样记 skipped。fixed 会话的注入还会在会话层串行化（忙策略）。
- `every` 调度在注册时锚定；锚点跨重启持久化，触发网格永不漂移。
- 任务完成态（派生、不落盘）：调度耗尽（`at` 已触发）或已启动 `MaxRuns` 次运行；完成任务保留历史但不再触发。
- 注册对非法表达式、低于 `MinEvery` 的 every、已耗尽的调度、重复 id 及 `Validate` 违规响亮失败。

## Integration

调度器从外侧组合进来；agent 各包从不引用它。

- **纯 agent**：把你的构造代码作为 `AgentExecutorOptions.NewAgent`/`LoadAgent` 传入；把 `TaskManageTool(scheduler)` 追加进 agent 工具列表。见 `examples/scheduled_agent`。
- **codingagent 会话**：`AgentSessionConfig.Agent`（调用方构造的 agent）把 fixed 策略绑到交互会话；`AgentSessionConfig.CustomTools` 配合 `codingagent.CreateToolDefinitionFromAgentTool(tasks.TaskManageTool(...))` 暴露工具，不改 codingagent 一行。见 `examples/scheduled_codingagent`。
- **宿主管线**（自有回合队列或任务运行器）：实现 `PipelineSubmitter`——在 `Submit` 内解析会话策略、交回 `Done`/`Cancel`——再用 `NewPipelineExecutor` 包装；等待/超时/取消骨架由本包承担。见 `examples/scheduled_pipeline`。
- **宿主自有存储**：针对既有数据库实现 `Store`。适配器契约写在接口注释上：调度器串行化全部调用、`Save` 为全量快照原子替换（事务内 upsert 加删除缺席行即可满足）、加载顺序无关、降级记录以 `StoreIssue` 浮出、schema 与迁移留在宿主；`TaskRecord`/`TaskSpec` 的 JSON 往返可直接支撑 JSON 列。
- **其它情形**：直接实现 `Executor`——测试 fake、`AgentExecutor` 与 `PipelineExecutor` 已共用这条缝。

## Known limitations

- 单进程：无 leader 选举或跨实例锁；一个 store 归属一个进程。
- 无补跑机制：需要错失恢复的消费方基于运行历史在其上构建，而非内置进调度器。
- 源标记位于 `Fire.Trigger`/`TaskRun` 记录与任务 prompt；`ai.UserMessage`（钉死的端口类型）不携带 metadata 字段。
- cron 粒度为分钟；秒级调度使用 `every`。
