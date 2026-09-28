# Agent Note: pico3 JSONL 重放全程保持 jsonx 模型

Status: implemented

[English](2026-09-28-pico3-replay-jsonx-codec.md) | 中文

## Problem

pico3 的 JSONL 重放路径曾经 `encoding/json` 解码持久化记录（[jsonl.go](../../../../agent/harness/pico3/jsonl.go) 的 `jsonDecodeInto`），而 jsonx 对象的状态全部在未导出字段里，于是每个 jsonx 类型的载体字段都解码成非 nil 的*空*对象：重启后 `Entry.Data` 为空、`Entry.Model` 元素变成 `map[string]any` 而非 `*jsonx.Obj`（下游所有角色断言失灵）、`Task.Checkpoint`/`Outcome` 被清空。除此之外：`patchFromJSON` 断言了错误类型（载体存的是 `*jsonx.Obj` 却断言 `map[string]any`），补丁的 checkpoint 与 outcome 被直接丢弃；缺失的 checkpoint 键重放成显式 null（无关补丁清掉了 checkpoint）；显式 null 又因编码器读取生产端从未设置的私有标志而从不落盘；`Conversation` 的 sections `Data` 在写入侧因同一 `encoding/json` 原因丢失；畸形但可解析的记录（doc 写缺 `ref`、patch 写缺 `patch`）让重放 nil 解引用而非报错。

## Decision

记录编解码对称，且端到端保持在 jsonx 模型内。写入与读取都走手写的逐字段载体——`entryCarrierToJSON`/`FromJSON`、`taskCarrierToJSON`/`FromJSON`、`conversationCarrierToJSON`/`FromJSON`、`patchCarrierToJSON`/`FromJSON`——jsonx 值以 jsonx 值移动；`encoding/json` 仅准入字段全为普通 Go 标量的载体（`Input`）。`TaskPatchJSON` 显式携带 checkpoint 三态（存在且为 null 记 `HasCheckpointNull`，对象则设置 `Checkpoint`，键缺失则什么都不记），wire 对象仅在显式清除时写出 `"checkpoint": null`，缺失经往返后仍表示"未触碰"。`writesFromJSON` 与 `recordFromJSONString` 把缺失载荷与非对象写入项以 "malformed record" 错误拒绝，取代 panic。

## Alternatives considered

**给 jsonx 对象导出字段或实现 `UnmarshalJSON`，让 `encoding/json` 往返可用。** 否决：jsonx 刻意在内部表示中保序并保持 JS 数值语义；向结构体解码开放会让 `encoding/json` 的浮点/键序行为静默改写持久化值——正是本记录要关闭的损坏类别。

**解码成 `map[string]any` 镜像，在使用点逐字段转换。** 否决：编解码面翻倍，且未来每个载体字段都距离同样的"静默变空"只差一次遗忘的转换；逐字段载体把每个字段的两个方向收进同一文件。

**重放时把缺失 checkpoint 当作 null（旧行为）。** 否决：存活应用语义是三态（未触碰/清除/设置）；把缺失重放为清除，会让同一条日志的重放状态偏离存活状态。

## Consequences

重启恢复保留 entry 数据与 model 角色、task checkpoint 与 outcome、conversation section 数据；补丁 checkpoint 三态在 close/reopen 周期中与存活应用一致。以上全部由 [jsonl_replay_test.go](../../../../agent/harness/pico3/jsonl_replay_test.go) 钉住，含畸形记录的错误路径。`TaskPatchJSON` 字段形状有变（三态标志取代了 `any` 类型字段加一个不可设置的私有标志），属预稳定面。旧编码器写下的记录无法区分显式 null 与缺失——可接受，因为编码器与读取器同树发布，不存在外部文件。
