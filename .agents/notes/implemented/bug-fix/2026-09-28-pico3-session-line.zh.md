# Agent Note: pico3 会话线并发

Status: implemented

[English](2026-09-28-pico3-session-line.md) | 中文

## Problem

pico3 Session 把单线程 TS 语义原样搬上 goroutine 而无保护：包级 `sessionOwners` map、由 tail 协程写而 `Subtree`/`Ancestors` 无锁读的 `LiveTasks`/`ConversationRecords`/`OwnerTaskCache`、无锁遍历的监听器列表，全部是数据竞态（map 类是 fatal）。从 commit 回调或监听器发起的同步 `Commit` 会排到它自己正阻塞的 tail job 之后，双方永久死锁。`Close` 从不关闭 tail 队列，每会话泄漏一个 goroutine 加 1024 槽缓冲。legacy tracker 对 base 与 working 浅克隆，嵌套就地变更同时别名两侧，`Flush` 产出零 ops。

## Decision

全部共享状态加互斥锁（`s.mu`），`sessionOwners` 经包级助手守护；监听器在锁内快照、在所有会话锁之外分发。重入调用内联执行：tail worker 恰在执行 job 期间置位 `onTail` 原子标志，观察到它的 `Commit` 内联执行 job 而非入队。job 主体把事务段（invoker 校验、storage 提交、applyChanges——以 `txnMu` 串行）与监听器分发（永不在 `txnMu` 下）分离，内联路径既不可能与 worker 死锁，即便非 tail 调用方在 `onTail` 短暂为真的窗口内误判，事务也绝不交叠。Close 以哨兵排空 tail 并按与 storage 队列相同的互斥协议关闭。tracker 对 base 与 working 深拷贝（chord CopyJson），嵌套变更对 diff 可见。

## Alternatives considered

**精确检测 tail goroutine，非该协程的重入报错。** 否决：Go 没有可靠的 goroutine 身份原语；原子观测设计让误判无害（该 job 与其他一样在 `txnMu` 上串行），而不是把死锁变成插件代码无法处理的错误。

**job 后异步分发监听器，使其提交正常入队。** 否决：破坏同步监听器契约（测试与插件等待监听器效果），且监听器与下一次提交的顺序变得不确定；TS 在 commit 的 continuation 内 await 监听器。

**保留浅克隆并改为递归 diff。** 否决：递归 diff 能找回 ops，但别名 base 在变更应用后呈现的就是 working 状态，空 flush 会产出幻影 ops；深拷贝恢复真实的前后语义。

## Consequences

commit 期间的并发 `Subtree`/`Ancestors` 与监听器发起的提交在 `-race` 下钉住；Close 不遗留 goroutine；tracker 的嵌套变更 flush 为 Set ops 且下一次 flush 安静。顺序说明：内联重入提交在外层 `Commit` 返回之前完成（嵌套），而 TS promise 链先 resolve 外层——最终状态一致，仅中间事件交错不同。
