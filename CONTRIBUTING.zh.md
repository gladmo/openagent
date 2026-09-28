# 贡献指南

[English](CONTRIBUTING.md) | 中文

感谢你有兴趣为 openagent 做贡献！

移植正在进行中：动手前先在 https://github.com/gladmo/openagent/issues 开一个 issue 说明目标区域，并查看 [PORTING.md](PORTING.md) 的阶段状态；针对已标记完成区域的 pull request 欢迎提交。

## 参与方式

- 在 issue 跟踪器中报告缺陷并为已有报告点赞：https://github.com/gladmo/openagent/issues。
- 改进文档与示例；标准由 [docs/AGENTS.md](docs/AGENTS.md) 规定。
- 解答问题，帮助社区其他成员。

## 提交 pull request 之前

- 阅读 [AGENTS.md](AGENTS.md) 的常驻约定；它们适用于每一次变更。
- 按 [pre-push-checks](.agents/skills/pre-push-checks/SKILL.md) 为你的 diff 选择证据并运行，报告你执行过的命令；不要推送后寄希望于 CI 结果不同。
- 变更中持久的设计决策需附带 [Agent Note](.agents/notes/README.md)，且每个新 note 都要触发[取代检查](.agents/notes/AGENTS.md)。
- 双语文档同步更新：在同一个提交中同时修改 `.md` 与 `.zh.md`。

## 开发环境搭建

见[开发指南](docs/development.md)；测试策略见 [docs/testing.md](docs/testing.md)。
