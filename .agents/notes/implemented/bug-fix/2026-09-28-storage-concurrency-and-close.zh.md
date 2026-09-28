# Agent Note: 存储层并发与关闭协议

Status: implemented

[English](2026-09-28-storage-concurrency-and-close.md) | 中文

## Problem

会话存储层按单线程 TypeScript 1:1 移植，在任何并发下都会崩溃：[InMemoryStorageState](../../../../agent/harness/session/memory_state.go) 声明了互斥锁却从不获取，读与 commit 并发时触发 Go 不可恢复的 `concurrent map read and map write` fatal；[memory.go](../../../../agent/harness/session/memory.go) 与 [jsonl/storage.go](../../../../agent/harness/session/jsonl/storage.go) 的 `enqueue`/`Close` 在锁内检查状态、解锁后才向队列发送，与 `close(queue)` 竞态；任何与 `Close` 交叠的 `Commit` 直接 panic（`JsonlStorage is closed`）。同族缺陷还有三个：`JsonlStorage.backing` 由工作协程在 v3→v4 升级期间写入、调用方无锁读取；`MemorySessionRepo.Open` 在全新空状态上重开会话（close→reopen 丢全部数据）；`SelectBranchFork` 把根条目的 nil parent 当作损坏——JS 的 `undefined`/`null` 之别在移植中塌缩成了一个 `nil`。

## Decision

存储层可安全并发使用，并以错误形式闭合失败。`InMemoryStorageState` 的每个方法都获取自己的 RWMutex——`ApplyValidated`/`AdvanceNextSeq` 取写锁，全部读方法取读锁——嵌套调用路径（`ScanBranchStructure`、`CreateFork`→`selectForkPlan`）改走非导出的 `*Locked` 辅助方法。队列协议把发送本身置于互斥锁内，`close(queue)` 也在锁内进行：通过 open 状态检查的发送者必然在 `Close` 关闭通道之前完成发送；job 永不获取存储互斥锁，缓冲区满时工作协程仍可持续排水，排除死锁。`Close` 之后（或竞态中）的 `Commit` 返回 "storage is closed" 错误——这是参考实现 throw 在签名已返回 `error` 的 Go API 上的忠实映射，取代原先杀死进程的 panic。`JsonlStorage.backing` 的读写置于 `s.mu` 之下。`MemorySessionRepo.Open` 在已记录的状态对象上重开，该对象作为纯数据在所属存储 `Close` 后仍存活。`SelectBranchFork` 的 `GetParent` 回调返回 `(*string, bool)`：`(nil, true)` 表示根条目，`(_, false)` 表示条目缺失。

## Alternatives considered

**把全部读操作路由进 commit 队列。** 否决：读会被 commit 的文件 I/O 串行化，读延迟特征改变，而相对 RWMutex 并无正确性收益；队列的存在意义是串行化 commit，不是读。

**用 `closed` 标志保护发送，替代锁内发送。** 否决：不把锁同时覆盖检查与发送，check-then-send 仍然存在 send-after-close 窗口；锁内缓冲通道发送在此处成立，正因为 job 永不重新获取该锁。

**保留 Commit-after-Close 的 panic（响亮失败）。** 否决：该 panic 在普通关停竞态的 commit 路径上触发，而 `Commit` 本就返回 `error`；描述性错误同样响亮且可恢复，与 TS 参考将同一状况呈现为 rejected promise 而非进程崩溃一致。

## Consequences

commit 期间的并发读、与 `Close` 竞态的 `Commit`、close→reopen 周期、branch 范围 fork，均由 [concurrency_test.go](../../../../agent/harness/session/concurrency_test.go) 在 `-race` 下钉住。调用方须将 closed-storage 错误视为该存储实例的终态（此前是直接崩溃，不存在会回归的存活调用方）。`GetParent` 签名变更属预稳定 API 面，是本记录唯一的外部形状变化。
