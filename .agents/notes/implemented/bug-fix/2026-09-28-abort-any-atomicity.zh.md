# Agent Note: abort 派生信号的原子性与处置

Status: implemented

[English](2026-09-28-abort-any-atomicity.md) | 中文

## Problem

`abort.Any()` 先用 `Aborted()` 扫描源信号、然后才注册 `OnAbort` 监听器——两个独立步骤。源在这之间 abort 时派生信号永不触发：20 万次压测复现丢失 123 次 abort；而 codex 传输每次 attempt 都从调用方的长生命周期信号派生，用户取消落在窗口内时 fetch 将继续运行而不被取消。同根的还有两处：`OnAbort`（按 JS 契约）在已 abort 的信号上永不触发；`Any` 不保存 disposer，每次派生都在源信号上泄漏监听器直至其生命结束。

## Decision

注册与 abort 判定原子化：`Signal.register(fn, fireIfAborted)` 在单次持锁内完成判定与监听器追加；当源已 abort 且 `fireIfAborted` 置位时，`fn` 在解锁后同步执行（abort 状态是终态，推迟到锁外不损失任何东西）。`Any` 建立其上，新增导出的 `AnyWithDispose` 返回聚合 disposer，移除在源上注册的全部监听器（派生信号自身状态不受影响）。`ToGoContext` 以 `fireIfAborted` 注册，已 abort 的信号产出立即取消的 context。

## Alternatives considered

**轮询源的 Done() channel 代替监听器。** 否决：只观察 Done 的派生信号仍需每个源一个 goroutine 来翻译 reason，且"首个 abort 的源的 reason"会与轮询竞态。

**仅让 OnAbort 返回 disposer、由 Any 的调用方处置。** 否决为不足：原子性窗口在检查与注册之间而非移除处；处置只解决泄漏，解决不了丢 abort。

## Consequences

`Any` 不再丢 abort（由 [any_race_test.go](../../../../abort/any_race_test.go) 的 -race 压测钉住）；按 attempt 派生（codex 重试循环）改经 `AnyWithDispose` 处置，不再累积。已 abort 信号上的 `OnAbort` 现在返回 no-op remove，不再保留一个永不运行的监听器。`FromGoContext` 对永不完成的 context 的 goroutine 泄漏是同族剩余缺口（需要持有 disposer 的 API），留在 backlog。
