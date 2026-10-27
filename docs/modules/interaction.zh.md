# 交互

[English](interaction.md) | 中文

`agent/harness/interaction/` 包是 ask-user-question 能力接缝：运行中的 agent 可以挂起一次工具调用，直到人类回答问题。本包为 openagent 原生（非 1:1 移植；与 `agent/harness/toolset` 同源类别）；接缝决策记录在 [Agent Note](../../.agents/notes/implemented/architecture/2026-10-27-interaction-seam.zh.md)。

## 角色

- **定义**：词汇表（`AskRequest`、`Question`、`Option`、`Answer`、`InteractionError` 错误码）与 `Service` 链。只依赖 `agent/harness`；不导入任何提供方或消费方。
- **提供方**：`Answerer` 实现。`Service.Register` 追加一个应答方并返回幂等 disposer（注册即效果）。应答方阻塞直到被回答、拒绝（`ErrDeclined`）或经 select 上下文的中止信号而中止；待答状态归属应答方，不归属服务。
- **消费方**：`CreateAskUserQuestionTool(service)` 暴露模型面的 `ask_user_question` harness 工具；嵌入方也可直接调用 `Service.Ask`。该工具忽略执行工具上下文，因此无需改动即可经 `toolset.Adapt`/`NewLocal` 装配。

## 语义

`Service.Ask` 先校验请求（`EMPTY_QUESTIONS`、`BAD_QUESTION`、`DUPLICATE_QUESTION_ID`），按注册顺序遍历链快照，并校验每个返回的答案（`BAD_ANSWER`：每个问题恰被命名一次、label 属于选项且不重复、仅 multiSelect 允许多 label、自由文本恰一个 label）。空链或全部拒绝时以 `NO_ANSWERER` fail closed。上下文中止时以 `ASK_ABORTED` 失败——在链前与每个应答方返回后各检查一次，中止后到达的答案不生效。阻塞中的 ask 期间的新注册应用于下一次 ask。

工具 schema 与词汇表同构（`questions[]` 含 `id`、`header`、`question`、可选 `multiSelect` 与 `options[]` 的 `{label, description?}`）；共享校验之外的输入——无选项的 `multiSelect`、空或重复的选项 label——在解析期拒绝。工具把答案按每问题一行 `id: labels` 写入 `Content`，机器可读形式写入 `StructuredContent`。

## 已知限制

- 无限时等待、无 RPC 暴露；应答方自管超时。
- 工具在工具调用内同步作答；异步 continued 问题与以用户消息形式 steer 回答案不在范围内。
- 无独立审批接缝；审批类拦截仍属 `before_tool` hook 加 gate。
