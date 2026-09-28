# Agent Note: 智能体协作脚手架落地

Status: implemented

[English](2026-09-28-agent-scaffold-adoption.md) | 中文

## Problem

仓库里只有 Go 源码、测试与两份移植跟踪文档（[PORTING.md](../../../../PORTING.md)、[PARITY.md](../../../../PARITY.md)），没有面向智能体的常驻指令、文档标准、决策记录，也没有确定性门禁：没有任何机制在机械层面保证文档成对、预算合规、结构一致，持久决策也没有持久之地可落。

## Decision

本仓库采用 meta-project 脚手架（自 deepseek-harness 提炼）：根 AGENTS.md 常驻指令并以 `CLAUDE.md` 符号链接指向它；[docs/](../../../../docs/AGENTS.md) 下的分层双语文档标准；RFC 风格的 [Agent Notes](../../README.md)；pre-push-checks 与 code-review 两个技能；由 [run-gates.mjs](../../../../scripts/run-gates.mjs) 聚合的零依赖 Node 门禁，接入 lefthook 的 pre-commit。模板的可选性能通道与网站层级暂不采用：其预算与构建在源机器和依赖树上校准，而本项目尚无 Go 测量面与文档站；两者日后均可从模板复制。[PARITY.md](../../../../PARITY.md) 与 [PORTING.md](../../../../PORTING.md) 这两份单语言移植工作底稿在移植完成后退役，因此加入 [scripts/lib/md.mjs](../../../../scripts/lib/md.mjs) 的免配对名称集合，而不是翻译它们。

## Alternatives considered

**把 PARITY/PORTING 翻译成双语配对。** 否决：它们是移植进行时的临时跟踪文档；为注定收敛为审计结论的文件在每次编辑上支付配对税毫无收益。

**整体照搬 deepseek-harness 布局。** 否决：它是 TypeScript 专属的（翻译目录、TS 专属门禁）；模板已经承载了语言无关的提炼版本。

**不用脚手架。** 否决：移植是智能体驱动的工作；没有常驻指令与门禁，文档会漂移，决策依据会蒸发。

**保留基准通道。** 暂时否决：它唯一的用例在模板机器上校准的预算下测量门禁脚本；待存在 Go 测量面后再作为独立决策重新录制。

## Consequences

钩子安装后，每次提交本地都会运行 run-gates（亚秒级、离线）；人读文档以 `.md`/`.zh.md` 成对存在并同步更新；持久决策以带取代检查的 Agent Note 落地。日后补加 benchmarks 或 website 层级时，从模板复制并凭全新校准运行录制其期望。
