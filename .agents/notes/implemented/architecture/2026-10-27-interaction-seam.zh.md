# Agent Note: 把 ask-user-question 能力拆成三角色 interaction 接缝

Status: implemented

[English](2026-10-27-interaction-seam.md) | 中文

## Problem

harness 此前无法让运行中的 agent 获得人类的决策：循环是纯粹的 prompt 进/事件出，审批类拦截被留给嵌入方，用无名的 `before_tool` hook 加 gate 组合实现——没有问题词汇表、没有答案校验、没有 fail-closed 语义，模型也没有可调用的入口。每个需要"问人"的嵌入方都会自造一套机制，而复制件正是契约腐烂之处（与产出 `toolset` 的失败模式相同）。

## Decision

新增 `agent/harness/interaction`（模块页：[docs/modules/interaction.zh.md](../../../../docs/modules/interaction.zh.md)），openagent 原生（与 `toolset` 同源类别），按三角色拆分使接缝两侧皆可替换：

- **定义**——词汇表（`AskRequest`、`Question`、`Option`、`Answer`）、错误分类学（`InteractionError` 错误码跨接缝传递）与 `Service` 应答方链。只导入 `agent/harness`；不导入任何提供方或消费方。
- **提供方**——`Answerer` 实现经 `Service.Register` 注册，返回幂等 disposer（注册即效果）。Ask 按注册顺序遍历每次调用的快照；应答方以 `ErrDeclined` 哨兵表示拒绝。阻塞等待归属应答方（select 上下文的中止信号），服务不持有待答登记表。
- **消费方**——`CreateAskUserQuestionTool` 是模型面的 `ask_user_question` harness 工具；嵌入方也可直接消费 `Service.Ask`。该工具只是可能的消费方之一，且只依赖定义。

失败语义：空链或全部拒绝以 `NO_ANSWERER` fail closed；上下文中止以 `ASK_ABORTED` 失败，在链前与每个应答方返回后各检查一次，中止后到达的答案不生效；每个返回的答案都对照请求校验（`BAD_ANSWER`）。

## Alternatives considered

**把能力内联进 `agent/harness/tools`。** 拒绝：`tools` 1:1 镜像参考的 `harness/tools/*` 且绑定 env；没有 TS 对应物的原生能力应属独立源类别，且 ask 工具不需要 `ExecutionEnv`。

**用 `before_tool` hook 加回调表达提问。** 拒绝：hook 是变换点——可以否决或改写，但没有答案词汇表，也无法在工具调用内把值返回给模型。

**单个可注入的 `Ask` 函数、无链。** 拒绝：单一闭包取消了提供方组合（UI 应答方加自动化兜底），fail-closed 也需要显式的"拒绝"概念来区分"没有应答方受理"与"应答方失败"。

**服务持有的待答登记表加限时等待。** 延后：待答状态与截止时间在第二个消费方出现之前归属应答方；定义保持最小（能力接缝规则：第二个角色出现前不预加机器）。

## Consequences

- 错误码是稳定 API：工具折叠进错误文本，嵌入方按其分支。
- 该工具忽略执行工具上下文，因此经 `toolset.Adapt`/`NewLocal` 装配无需改动——首例不绑定 env 的树内 harness 工具。
- 限时等待、异步 continued 问题、RPC 暴露与独立审批接缝记录为模块页的已知限制；各自在其消费方出现时落在这条缝上。
- supersession 检查未发现覆盖人类交互的既有笔记；本笔记开题。
