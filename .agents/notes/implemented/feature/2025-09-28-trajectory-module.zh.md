# Agent Note: 轨迹作为一等模块

Status: implemented

## Problem

调试、审计、回放、评估与回归测试 agent 运行都需要同一件东西：一份忠实、结构化的运行记录——模型输入输出、思维链、工具往来、重试、错误、耗时、Token 用量、图片。pi 基准技术栈没有这种能力（其 harness session 持久化的是会话状态，不是观测数据），而 openagent 的移植契约把 `agent/` 与 `ai/` 钉在基准上，任何移植面都无法在不"发明行为"的前提下长出它。问题变成：对标 DeepSeek Harness 的轨迹能力应该落在哪里、对象模型长什么样。

## Decision

新增顶层包 `trajectory/`，openagent 原生（非 pi 移植；PORTING.md 与 PARITY.md 有意不记条目），导入 `agent`/`ai`/`jsonx` 且无反向依赖。对象模型沿用 DSH session-event 设计：只追加的类型化记录日志 `{type, seq, time, turn, step, data, ignorable}`，提交后发布语义；`Trajectory.Subscribe` 可重复调用、返回退订函数、只发增量、按订阅序、panic 隔离；`SubscribeAfter` 补投原语在提交锁下投递积压，晚挂的 sink 仍能写出合法文件；`Recorder` 仅经两条缝采集——一个 `Agent.Subscribe` 监听者加一个 `StreamFn` 包装——循环行为零改动；JSONL 存储容忍末行撕裂、读取端拒绝未知非 ignorable 种类；Transcript/Outline/Usage 折叠视图；默认 stdout NDJSON 记录（限幅）；回放以脚本派生落到既有 `ai` faux 提供方之上，并兑现此前无消费者的 `AgentTool.Replay` 标记。完整种类目录、Subscribe 契约与显式的"不记录"清单归[模块页](../../../../docs/modules/trajectory.zh.md)。

## Alternatives considered

**扩展 harness 会话存储（`agent/harness/session`）而非新建包。** 放弃：该存储是被钉住的 pi 移植，形态是持久会话树；把观测数据（失败尝试、重试链、逐块流时序、完整模型输入）折进去既扰动移植面，又混淆两种生命周期——会话状态会被压缩重写，观测数据绝不能。

**让 `Subscribe` 向晚到的订阅者重放历史。** 放弃：重放即迫使每个监听者在"漏历史"与"双投递"间抉择，也破坏了让发布者不受观察者成本影响的提交后 fire-and-forget 契约。历史读取保持显式（`Snapshot`/`Query`）；唯一正当的重放消费者——存储 sink——改用 `SubscribeAfter`，在提交锁下原子衔接积压。

**给 `ai.RetryProviderRequest` 加观测钩子来采集提供方内部重试。** v1 放弃：`ai` 受奇偶校验钉定，钩子是无基准的新面；重试改经 `Recorder.RetryCallbacks()` 露面（在调用方接 `ai.RetryAssistantCall` 处接线），模块页写明扩展缝（把重试放到 agent 之外，每次尝试各自成一条被完整记录的运行）。

**turn/step 坐标放各记录载荷内（DSH 原样）而非信封。** 放弃：Go 消费者高频按 turn/step 过滤分组；信封放置让载荷模式只管载荷，作用域校验（`session/turn/step`）成为一条信封规则而非逐种类不变量。

## Consequences

轨迹文件是平行产物，不是 harness 会话日志；两者按轨迹 id 关联、分别读取。Recorder 不复制循环已持久化的内容、也绝不阻塞它：观察者失败被隔离并经 `Options.OnListenerError` 上报。回放回归（`CompareRecords`）以归一化忽略 seq/time/用量/流时序——确定性工具逐字比对，真实文件系统工具需预归一化 fixture。已知缺口写在模块页：无压缩、锁、迁移链与 SQL 索引；harness 事件总线尚未接入采集；steer 与 followUp 在循环事件层不可区分，记为 `injection`。验证：`go test ./trajectory/ ./examples/trajectory_demo/`（单元、全链路、race）加 `gofmt -l .` 与 `go vet ./...`。
