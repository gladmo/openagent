# Agent Note: ai 传输层加固

Status: implemented

[English](2026-09-28-ai-transport-hardening.md) | 中文

## Problem

六项已确认的传输层缺陷：anthropic-messages 传输从未接入 Copilot 动态头（`X-Initiator`/`Openai-Intent`/vision），而 catalog 恰在该传输上发布 Copilot Claude 模型；fetch 层失败（连接拒绝、DNS、重置）以裸 `*url.Error` 到达 `IsRetryableProviderError`，从不重试——`Status == 0` 的 SDK 对齐分支不可达；`content_block_delta` 类型与所引块 kind 不匹配时空引用击穿流；`blockFromJSON` 使用未检查的类型断言，一条被截断的持久化块就能让崩溃恢复路径 panic；frame 编码器以 rune 计数 covered 边界、delta 计账与切片却用字节，产出非法 UTF-8；codex 重试循环每 attempt 派生 `abort.Any` 而不处置，在会话级信号上泄漏监听器。

## Decision

分类发生在 PostJSON/PostJSONStream seam：任何非 `*ProviderHTTPError`、非 abort 的 fetch 错误包装为 `&ProviderHTTPError{Status: 0}`（APIConnectionError 的角色），既有的重试策略与指数退避随之生效。anthropic 传输与两个 OpenAI 传输完全同型地构建 Copilot 动态头。不匹配的 delta 按块状态的 kind（text/thinking/toolCall）门控后跳过，对齐 TS 容忍语义。`blockFromJSON` 改用安全的 `stringField`/`strPtr` 访问器——缺失或类型错误的字段解码为零值（顺带修复了 `thoughtSignature`/`namespace` 因变量遮蔽而从 arguments 对象读取的 bug）。frame 编码器以字节计数 covered 边界，与 delta 计账和切片一致。codex 循环改经 `abort.AnyWithDispose` 派生并在每 attempt 后立即处置。

## Alternatives considered

**在 DefaultFetch 中包装传输错误。** 否决：经 options 注入的自定义 `Fetch` 返回自己的错误；PostJSON 是所有传输都跨越的唯一 seam，在此分类可同时覆盖两者。

**用专用标志重试传输错误而非 Status 0。** 否决：status-0 分支已编码所钉 SDK 的连接错误策略（可重试、指数退避、无 retry-after 头）；平行机制会与它漂移。

**为不匹配 delta 发错误事件。** 否决：TS 参考能追加则追加、不能则忽略；因可容忍的网关怪癖而失败整轮正是本次移除的行为。

## Consequences

瞬时网络故障现在按 SDK 策略重试（conn-refused + MaxRetries=2 得到三次尝试，由 [transport_defects_test.go](../../../../ai/transport_defects_test.go) 钉住）；Copilot Claude 请求在 wire 上携带网关路由头；畸形的持久化 JSON 解码为零值而非让恢复路径 panic；多字节 covered 边界切出合法 UTF-8；codex 重试循环不留监听器残留。abort 保持不可重试（经 `transportError` 原样通过）。
