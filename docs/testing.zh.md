# 测试策略

[English](testing.md) | 中文

证据与面相匹配：能对该回归失败的最窄检查拥有这次变更。本地绝不默认跑全量；穷举覆盖与平台矩阵归 CI 负责。证据选择流程见 [pre-push-checks](../.agents/skills/pre-push-checks/SKILL.md)。

## 测试层级

- **单元：** 单包独立行为；快速且确定。`*_test.go` 与被测代码同目录。
- **对齐 / golden：** 被移植的库（`jsonx`、`typebox`、`partialjson`、`diff`、`ignore`、`telemetry`、`chord`）由对照 npm 原件与 pi TypeScript 源码的 golden 及一致性测试钉住；基准行为即契约。
- **端到端：** 真实提供方冒烟测试（`ai/providers_smoke_test.go`）调用真实 API，未启用时自动跳过。

## 密钥

真实提供方冒烟测试仅在 `PI_SMOKE_TESTS=1` 且提供目标提供方密钥时运行——`ANTHROPIC_API_KEY`、`OPENAI_API_KEY`、`DEEPSEEK_API_KEY`、`OPENROUTER_API_KEY`、`ZAI_CODING_CN_API_KEY` 及 `ai/env_api_keys.go` 表中的其余变量。绝不打印密钥；绝不提交包含密钥的凭据或录制数据。

## 测试质量规则

- 测试描述行为而非正确性：断言在目标回归上失败，并验证可观察状态而非复述实现。
- 只有单独运行才能通过的测试是测试的缺陷：把每个端口、临时路径和子进程的所有权管到清理为止。
- 过时行为与其测试一起修改，并在 PR 中说明原因。
- 用户或模型可见输出的非平凡变更，在同一个提交中更新其归属的快照或期望输出数据。
