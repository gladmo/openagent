# Agent Note: 迁移 pi a276dabe5..bd3c9b823 区间

Status: implemented

[English](2026-10-24-pi-range-migration.md) | 中文

## Problem

pi 参考仓库在移植钉定的面（树 `ff72faba2`、MCP `a276dabe5`）之后前进了 333 个提交。区间把已移植面上的修复与四个全新子系统（durable 约 27K 行、chord2 宿主运行时约 9.4K 行、pi-env SSH 远程环境、codemode 增强）混在一起。全部移植会把未完成的移植耦合到移动的参考上；完全不移植则会让 Go 项目错过其日常发布面上区间的修复。

## Decision

只迁移落在已移植面上的变更，按七个有序批次（telemetry 基座 → 身份/计时 → ai 修复 → mcp → tui → codingagent → 目录刷新），每批随移植的参考测试落地。四个新子系统与未移植 provider 面延后，并在 PARITY.md 写下缺口台账；它们在已移植面中没有消费者（coding-agent 在 HEAD 对 chord 零引用；没有任何包引用 durable 或 pi-env）。

## Alternatives considered

**移植整个区间。** 否决：durable 一个就比这里迁移的全部更大，且硬阻塞在 chord2 运行时上；移植会成为对一切的部分拷贝。

**只挑修复、跳过功能。** 否决：区间的修复与功能交织（工具支持重构两者都是），且移植纪律把行为钉在参考上，而不是手工挑选的子集上。

## Consequences

- 已移植面现在跟踪 `bd3c9b823`；基准按面钉定（树 `ff72faba2` + 区间迁移；MCP 栈 `a276dabe5` + 区间修复；目录快照 `bd3c9b823`）。PARITY.md 持有映射与延后台账。
- 区间引入并已记录的 Go 偏差：telemetry carrier 表为实例域（无 WeakMap）；RenderTrace 属性按键排序；span 时间戳毫秒取整；detailed User-Agent 报告 Go 运时段；退出恢复走 signal.Notify（直接 os.Exit 与异 goroutine panic 不可恢复）；RE2 无环视 → 边界字符消费类（状态码）与过程式扫描（终端回复）；CJK 友好强调以 punct+word closer 规则近似，该近似同时暴露并修复了 rune/字节宽度错位。
- 延后台账是未来 durable/chord2 决策的输入；今天的延后不阻塞已移植树里的任何内容。
- 每批以 owning 包测试验证，随后 `gofmt -l .`（空）、`go vet ./...`、`go build ./...`、`go test ./...`、`node scripts/run-gates.mjs`。创建时 supersession 检查：[MCP port-ahead note](2026-10-14-mcp-port-ahead-of-baseline.md) 保持 active（其 M2 延后不变，且现已列入区间台账）；没有其他 note 覆盖基准推进。
