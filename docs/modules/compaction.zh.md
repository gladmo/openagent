# 上下文压缩（Compaction）

[English](compaction.md) | 中文

`agent/harness/compaction/` 包是 provider 无关的压缩核心：token 估算、线性会话路径上的切点规划，以及把规划好的切点变成检查点摘要的重试包裹 LLM 摘要调用。它从 `codingagent` 的压缩移植（pi `core/compaction/compaction.ts`）中提取，使非 codingagent 消费方无需导入 coding agent 即可压缩会话。

## Composition

- **Settings**（`compaction.go`）—— `Enabled`、`ReserveTokens`、`KeepRecentTokens`；`DefaultSettings` 与参考一致（true、16384、20000）。`ShouldCompact(contextTokens, contextWindow, settings)` 是阈值判定。
- **PathEntry**（`compaction.go`）—— 线性分支路径上一个会话条目的上下文贡献：持久 `ID`、`IsCompaction` 标记、原始 `Summary` 文本与 `Details` 载荷（压缩条目）、以及 `Messages`（该条目上下文可见的 agent 消息；压缩检查点的消息承载框架化摘要加保留尾部，仅展示条目不承载消息）。
- **切点**（`compaction.go`）—— `FindCutPoint` 自后向前累积估算 token 直到满足 keep-recent 预算，吸附到最近的有效切点条目（user 或 assistant；绝不是工具结果），把相邻零贡献条目拉入保留后缀，并报告割裂回合（`CutPointResult.IsSplitTurn`、`TurnStartIndex`）。
- **Preparation**（`compaction.go`）—— `PrepareCompaction(path, settings)` 在无可压缩时返回 nil（路径已结束于一个有贡献的压缩条目，或切点之前没有消息）；否则返回 `Preparation`，携带待摘要消息、割裂回合前缀、上一个检查点摘要、token 总量与文件操作。
- **token 估算**（`tokens.go`）—— `EstimateTokens`（每消息 chars/4，assistant 按内容块）、`CalculateContextTokens`（原生 totalTokens，否则分量和）、`EstimateContextTokens`（最后一个有效 assistant usage 加尾部估算；中止/出错/零 usage 的 assistant 不提供可用 usage）。
- **摘要**（`summarization.go`）—— 结构化摘要提示词（初始、更新、回合前缀）、`GenerateSummary` / `GenerateTurnPrefixSummary`、`CompleteSummarization`（经 `ai.RetryAssistantCall` 的重试汇聚点；一次性摘要获得 `CacheRetention: "none"` 与全新 session id）、以及产出 `Outcome` 的 `Compact(preparation, model, options, streamFn)`。
- **序列化**（`utils.go`）—— `SerializeConversation`（带标签的文本，使模型做摘要而非续写；工具结果按 `ToolResultMaxChars` 2000 截断）、文件操作跟踪（带 `path` 参数的 `read`/`write`/`edit` 工具调用）、`FormatFileOperations`（`<read-files>`/`<modified-files>` 后缀）、以及 `SummarizationSystemPrompt`。

## Summarization seam

`SummarizationStreamFn` 是 provider 缝合点：`func(ctx, model, contextMessages []ai.Message, options *SummarizationOptions) (*ai.AssistantMessage, error)`。传输凭据属于被注入的函数；`SummarizationOptions` 只携带建议性调用参数（`MaxTokens`、`ThinkingLevel`、`SessionID`、`CacheRetention`）。`GenerateSummaryOptions.ReserveTokens` 约束摘要预算（`maxTokens = min(0.8 × reserve, model.MaxTokens)`；回合前缀摘要用 0.5 × reserve）。

## Failure behavior

- `GetSummarizationFailure`：error stop 透出 provider 消息；length stop 拒绝持久化（"摘要不完整"）——残缺文本永远不会成为检查点。
- 尝试工具调用的摘要被拒绝。
- 瞬时流错误经调用方提供的 `*ai.RetryPolicy` 重试；确定性错误（例如凭据无效）不重试。
- `Compact` 在 preparation 为 nil 或 first-kept 条目缺少 ID 时失败。

## Adaptations from the codingagent reference

- 条目模型是线性 `PathEntry` 路径（harness session 分支形状）而非 codingagent 的 `SessionEntry` 投影；context-edit 投影逻辑没有等价物。
- coding-agent 的自定义角色（`bashExecution`、`custom`、`branchSummary`、`compactionSummary`）在此不存在；检查点的上下文贡献物化为框架化的 user 消息（`FramedSummary`），因此判定只覆盖四个 ai 角色。
- 参考中的 `ApiKey`/`Headers`/`Env` 管道缺席（流缝合点拥有传输）。
- 分支摘要（交互式 UI 功能）未移植。

摘要提示词、重试包裹调用、割裂回合处理与切点行走都是参考行为。

## Known limitations

- 文件操作跟踪只识别参考的 `read`/`write`/`edit` 工具名加 `path` 参数；使用其它工具的 agent 不产生文件清单。
- `EstimateTokens` 对非 ai 消息类型返回 0；调用方须把每条 `PathEntry.Messages` 物化为 ai 消息。
