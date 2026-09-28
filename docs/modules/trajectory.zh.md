# 轨迹（Trajectory）

[English](trajectory.md) | 中文

`trajectory/` 包把 agent 运行记录为工程对象：带"提交后发布"语义的只追加类型化记录日志，服务于调试、审计、回放、评估与回归测试。它是 openagent 原生能力，设计对标 DeepSeek Harness 的 session-event 模型——不属于 pi 移植。

## 组成

- **Record**（`record.go`）——信封 `{type, seq, time, turn, step, data, ignorable}`。`seq` 从 1 起稠密分配于提交时；`time` 为 Unix 毫秒、提交时盖戳。`turn` 计循环回合周期（每个回合含一次模型调用及其工具执行）；`step` 为回合内保留层（当前循环下每回合恰一步）。`data` 为 jsonx 值；`ignorable=true` 标记外来读者可跳过的记录。
- **Trajectory**（`trajectory.go`）——日志对象：`Append`（校验→提交→发布）、`Subscribe`/`SubscribeAfter`、`Snapshot`、`Query`、`Close`、`RegisterKind`。
- **Recorder**（`recorder.go`）——`Attach(agent, options)` 采集：一个 `Agent.Subscribe` 监听者 + 一个 `StreamFn` 包装；`Dispose` 全部还原。
- **流累积器**（`stream.go`）——带计时的增量运行 `{type:"text-chunks"|"thinking-chunks"|"toolcall-chunks", time0, index, dt[], texts[]|args[]}` 与裸块 `{type:"chunk", time, event}`，按到达序排列。
- **JSONL 存储**（`jsonl.go`）——首行 header + 每记录一行；`ReadFile` 容忍末行撕裂，拒绝未知非 ignorable 种类与断裂序列。
- **查询 / 组装**（`query.go`、`assemble.go`）——记录过滤；`Transcript`、`Outline`、`UsageSummaryFrom`、`Combine` 折叠视图。
- **Sink / 导出**（`export.go`）——`StdoutSink`（限幅 NDJSON）、`FileSink`、`MultiSink`、`PipeSubscribe(After)`；`ExportJSONL`、`ExportJSON`、`ExportMarkdown`。
- **回放**（`replay.go`）——`DeriveScript` → `BuildReplayAgent`（faux 提供方）、`LoadReplayOverride`/`ApplyOverride` 边车、`CompareRecords` 回归断言。

## 记录种类

- 会话级（`turn=0`）：`session/start`、`session/end`、`error`。
- 回合级：`turn/start`、`turn/end`（reason 为 `completed|aborted|error|length|deferred`）、`user/message`（`source: prompt|injection`）、`system/message`、`agent/message`（自定义角色，ignorable）。
- 步级：`step/start`、`request/header`（逻辑头：模型、思考级别、工具声明、消息数、系统提示词长度）、`model/input`（保真度 `full|summary|off`）、`assistant/message`（消息 JSON、计时流、用量、`durationMs`、`ttftMs`、`interrupted`）、`assistant/attempt`（最终结果为错误的模型调用）、`tool/call`（原始参数串）、`tool/update`（可选）、`tool/result`（消息 JSON、`isError`、`durationMs`）、`llm/retry` + `llm/retry-started`（经 `Recorder.RetryCallbacks()`）、`step/end`。
- 预留：`subagent/start`、`subagent/end`——仅经扩展 API 发出；嵌套 agent 各自记录，经 `parentTrajectoryId` 关联。

## Subscribe 契约

- **可重复调用：** 任意数量独立订阅并存；每次调用返回各自的退订函数；退订幂等。`Agent.Subscribe` 语义不动——recorder 只是又一个监听者。
- **只发增量：** `Subscribe` 只投递注册之后提交的记录。`SubscribeAfter(afterSeq, …)` 在提交锁下先投递积压（`seq > afterSeq`；`0` = 全量），再接实时——衔接原子，无缺失、无重排。首条记录之后才挂的 sink 用 `PipeSubscribeAfter`。
- **顺序与隔离：** 监听者按订阅序、提交后同步调用。监听者 panic 被隔离并经 `Options.OnListenerError` 上报，绝不中断发布者、不饿死其他监听者（与 `Agent.Subscribe` 的失败传播相反——观测面不得破坏被观测的 agent）。
- **重入：** 监听者内调用 `Append` 以 `ErrReentrantAppend` 失败；不得在监听者内调用 `SubscribeAfter`。

## 配置

`RecorderOptions` 零值（加 `Trajectory`）采集完整模型输入、计时流、内联图片（上限 256 KiB，超限保留 `mimeType`、`dataBytes`、`dataOmitted:true`）。字段：`ModelInput`（`InputFull|InputSummary|InputOff`）、`DisableStreamDeltas`、`IncludeToolUpdates`、`MaxInlineImageBytes`、`Now`（可注入时钟）。`StdoutSink` 限幅：每串 8 KiB、每行 32 KiB，截断处标 `truncated:true`。

## 不记录

- API 密钥与凭证——永不过采集面。
- 提供方原始 HTTP wire 报文与响应头——`request/header` 是逻辑头；`model/input` 是变换后的规范化输入。
- 提供方内部 HTTP 重试（`ai.RetryProviderRequest` 无观测钩子）与调用方重试包装内部隐藏的尝试期内容——重试仅经 `Recorder.RetryCallbacks()` 以元数据露面。扩展缝：把重试放到 agent 之外，让每次尝试各自成一条被完整记录的运行。
- steer 与 followUp 之别——两者都以普通用户消息进入循环；记为 `source:"injection"`。
- 工具的文件系统副作用（只记返回内容）、交互 stdin/UI 状态、进程指标、harness 会话/压缩内部状态（harness 会话存储自有日志）、子 Agent 内部（预留种类加父链关联）。
- 图片上限裁剪的内容带标记删除，绝不静默省略。

## 已知限制

- 单写者文件、无压缩、无跨进程锁、无格式迁移链（仅格式 v1）。
- 查询为内存/线性扫描；无 SQL/FTS 索引。
- harness 事件总线（`agent/harness/events.go`）尚未接入为采集源。

完整流水线（stdout 记录 + 文件 sink + 查询 + Markdown 导出 + 回放断言）见 `examples/trajectory_demo`。
