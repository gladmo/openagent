# Agent Note: 为 codingagent 之外的复用提取压缩核心

Status: implemented

English | [中文](2026-10-14-compaction-core-extraction.zh.md)

## Problem

压缩机制（阈值判定、切点搜索、割裂回合处理、重试包裹的 LLM 摘要）只存在于 `codingagent`，且焊接在它的 `SessionManager`/`SessionEntry` 投影模型上。agent 技术栈的其它所有消费方——会话服务、web 后端、自定义 harness——需要持久上下文管理时，只能在导入被其策略禁止的 coding agent（一个带 TUI 耦合的完整 CLI）与从零重写算法之间二选一。触发判定（`agent/harness/runtime.ShouldCompact`）已经可复用，这让缺失的执行侧成为不对称缺口。

## Decision

把 provider 无关的压缩核心提取到 `agent/harness/compaction`（模块页：[docs/modules/compaction.zh.md](../../../../docs/modules/compaction.zh.md)）。该包拥有：token 估算（`EstimateTokens`、`EstimateContextTokens`）、线性 `PathEntry` 路径上的切点规划（`FindCutPoint`、`PrepareCompaction`）、参考摘要提示词，以及注入式 `SummarizationStreamFn` 之后的重试包裹摘要执行器（`GenerateSummary`、`GenerateTurnPrefixSummary`、`Compact`）。`codingagent` 保持其现有实现不变；新包是非 codingagent 消费方的缝合点，作为 openagent 原生提取（同 `trajectory/`）文档化，而非 pi 1:1 移植。

## Adaptations carried by the extraction

- 线性 `PathEntry` 取代 codingagent 的 `SessionEntry` 投影；context-edit 恢复逻辑没有线性等价物，予以省略。
- coding-agent 自定义消息角色物化为框架化 user 消息（`FramedSummary`），切点判定因此只覆盖四个 ai 角色。
- 传输凭据（`ApiKey`/`Headers`/`Env`）留在被注入的流函数里。
- 分支摘要（交互式 UI 功能）不移植。

## Alternatives considered

**让消费方导入 `codingagent`。** 否决：`codingagent` 是产品面（CLI + TUI），不是库；下游项目（如 yian-agent）按策略禁止该导入，且依赖会把会话文件、扩展运行器与 UI 代码拖进每个服务。

**把算法复制进每个消费方。** 否决：切点行走与割裂回合语义微妙、由参考钉住；各自分叉的副本会漂移，golden 测试保护不到它们。

**改用 harness `runtime` 的压缩管线。** 暂时否决：runtime 的 `PrepareCompactionThreshold` 执行器缝合点期待 lane/drive 集成，而目前尚无生产消费者驱动它；提取纯核心同时服务直接路径（基于 `Agent.SetMessages` 的 agent）与未来的 runtime 注入。

## Consequences

- 非 codingagent 会话获得与参考一致的压缩：相同提示词、相同重试汇聚点、相同的失败拒绝（length stop 永不持久化、工具调用摘要被拒绝）。
- 树中存在两个压缩集成（codingagent 的与本包的）；它们同源但不同码。把 codingagent 整合到提取核心上是未来工作，前提是迁移 `SessionManager` 投影。
- `PathEntry` 契约要求调用方把上下文贡献物化为 ai 消息；其它一切按零 token 估算（已文档化的限制）。
