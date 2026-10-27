# Agent Note: 面向宿主运行时的管线执行器缝（agent/harness/tasks）

Status: implemented

## Problem

`Executor` 缝发布时只有两个适配器——测试 fake 与 `AgentExecutor`——都由同步的 agent 循环塑形：构造/找到 `agent.Agent`，发 prompt，等待。拥有自身执行管线的宿主运行时（队列准入的回合、任务运行器）要驱动 Fire，只能在每个宿主里重写同一副骨架：异步提交、等待终态 channel、把 context 过期传导进自家取消 API、再区分超时与取消的结局。这副骨架是框架知识而非宿主知识；每个宿主重建它正是框架要消除的重复。另一问题：已有关系库的宿主问 openagent 是否该提供可直接使用的存储，省去手写适配器。

## Decision

`agent/harness/tasks/executor_pipeline.go` 在更窄的缝上新增第三个适配器：`PipelineSubmitter.Submit(fire)` 把一条 Fire 落到宿主管线并返回 `PipelineSubmission`（`Done` 恰好投递一次终态 `RunResult`；`Cancel` 中止排队中或运行中的提交）。`NewPipelineExecutor` 承担其余全部——Submit 出错让运行响亮失败；context 超时报告 `failed: timeout after …`、普通取消报告 `interrupted`，两者都在 `CancelGrace`（默认 10s）排空之后——排空让宿主的终态写入先行落定；context 过期时该分类优先于宿主对被中止运行报告的任何结果。`ExecutorFunc` 把普通函数适配上缝。会话策略解析留在 `Submit` 内：`Fire` 已携带策略，而会话域归宿主所有。`AgentExecutor` 不动。存储维持原决策：`Store` 仍是唯一抽象，适配器契约改由接口注释言明（单调用方串行化、全量快照原子替换、加载顺序无关、逐记录降级、schema 归宿主）——openagent 不发布关系型实现。`examples/scheduled_pipeline` 以队列化的假宿主钉死该模式。

## Alternatives considered

### Why not generalize AgentExecutor behind agent-shaped interfaces?

宿主并不驱动裸 agent；其真实形状是「入队一个工作单元、等它的终态事件、按 id 取消它」。进一步抽象 `PromptText`/`Abort` 仍会逼每个宿主把自家管线包装成 agent 的样子——适配器照样要拥有这条缝本该共享的等待/超时代码。`PipelineSubmitter` 直接命名宿主实际拥有的形状。

### Why not let the host's terminal result win after context expiry?

无论宿主如何映射自己的中止（yian 把用户取消与超时映射为同一行状态），两个执行器都必须给出一致的分类。按 context 错误分类是确定性规则；宿主结果只在 context 存活期间获胜。宽限排空仍保证报告排在宿主终态写入之后。

### Why no busy policy on PipelineExecutor?

宿主管线自身串行化运行（每会话队列），调度器也已拒绝每任务重叠触发与全局并发帽之外的 Fire。忙策略会重复实现宿主已有的串行化。

### Why not ship a relational Store implementation?

`go.mod` 通过 vendor `yamlv3` 刚做到零依赖；GORM 或 `database/sql` 存储会为一个包推翻它。schema 与迁移属于拥有数据库的进程，而全量快照 `Save` 契约让事务内 upsert 加删除缺席行的适配器小到可以放进每个宿主。

### Why not an incremental per-record save interface?

`SaveRecord`/`DeleteRecord` 能力探测会给调度器增加第二条持久化路径，弱化多记录原子性（启动恢复要重写每条记录），而省下的写量在任务集规模下微不足道。快照契约保持单一路径。

## Consequences

- `Executor` 现有三个适配器（测试 fake、`AgentExecutor`、`PipelineExecutor`）；调度器与其余各缝不变。
- 经 `PipelineSubmitter` 接入的宿主只写领域代码：策略解析、提交与取消——等待/超时/取消骨架来自本包。卡死的宿主管线由 `CancelGrace` 界定报告时限；此后运行按 context 错误分类，宿主可能仍在收尾。
- 面向关系库的 Store 适配器仍归宿主所有；接口注释承载契约，`TaskRecord`/`TaskSpec` 的 JSON 往返可直接支撑 JSON 列。
- 2026-10-25 任务笔记的「两个真实适配器」后果被本笔记扩展而非取代；对方已链接至此。
