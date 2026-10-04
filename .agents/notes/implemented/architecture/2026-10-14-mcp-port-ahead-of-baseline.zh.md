# Agent Note: 在参考基准之前移植 MCP 栈

Status: implemented

[English](2026-10-14-mcp-port-ahead-of-baseline.md) | 中文

## Problem

pi 参考实现从 `ff72faba2`（本移植的基准）推进到 `a276dabe5`，该区间的标志性功能是 MCP：新的 `packages/mcp` 客户端库加上 coding-agent 集成（`core/mcp-servers.ts`、 `extensions/mcp/*`）。整个区间远超单次变更可吸收的规模（7.8 万行新增：durable 运行 时、codemode、TUI 重写），且大部分构建在本移植尚未到达的扩展宿主面（CA4/CA6）之 上。若等基准追平再移植，Go 项目将持续缺少 MCP——该区间的核心功能；若全部移植， 则会把一个未完成的移植耦合到移动的参考上。

## Decision

立即从 `a276dabe5` 移植 MCP 栈，作为对 `ff72faba2` 基准的固定例外，分三层并给出 明确边界：

1. **`mcp/` 包，完整。** `packages/mcp` 1:1：基于 jsonx 的 JSON-RPC 模型、协议类型
、stdio / Streamable HTTP / 内存传输、SSE 消费者、带超时/中止/进度的客户端，以 及 OAuth 闭包（发现、PKCE 流程、动态注册、provider 状态、回环回调服务器）。测 试移植参考套件。
2. **`codingagent` MCP 运行时，完整。** 配置校验与服务器注册表
（`mcp_servers.go`）、`mcp.json` 加载/合并/覆盖/编辑（`mcp_config.go`）、服务器 日志（`mcp_log.go`）、凭据存储 + auth provider + 浏览器登录（`mcp_oauth.go`）、 工具/结果转换（`mcp_tools.go`、`mcp_resources.go` 辅助）、可重连连接 （`mcp_runtime.go`），以及 `openagent mcp add/remove/list/login/logout` CLI （`mcp_cli.go`）。支撑性新增：`ai.OauthSuccessHTML/OauthErrorHTML`（pi-ai `utils/oauth-page.ts`）、`TruncateMiddle`（`core/tools/truncate.ts`），以及 `AgentToolResult.StructuredContent/IsError`（同区间 agent `types.ts`）。
3. **扩展宿主接线，命名归属后延迟。** `extensions/mcp/index.ts`（会话生命周期、工
具暴露/激活、`/mcp` 管理器）与 `ui.ts` 需要扩展事件宿主、`ToolDefinition` 的 exposure/namespace 字段、`setActiveTools`、codemode/tool_search，以及模型注册 表的 provider 令牌查询——均为 CA4/CA6 面。该缺口在 PORTING.md/PARITY.md 记录为 剩余 MCP 工作，而非已移植文件行为的偏差。

强制偏差（均记入 PARITY.md）：

- **跨进程锁。** proper-lockfile 的 mkdir 锁与续约定时器映射为带 mtime 过期接管的
O_EXCL 锁文件（`mcp_file_lock.go`）；单机保证，无续约定时器（刷新的请求上限 15 秒，低于 20 秒的过期窗口）。
- **进程退出清理。** Go 没有 `process.once("exit")` 钩子，stdio 传输无法在宿主退
出时杀死存活的服务器进程组；会话在关闭时断开连接，被杀的宿主留给服务器随 stdin 关闭退出（或走过期路径）。
- **客户端身份。** `initialize` 发送 `AppName()`/`Version()`，参考发送字面量
`"pi"`；服务器看到真实客户端。
- **超时编码。** `Number.POSITIVE_INFINITY` 无 Go 对应，逐请求超时用整数：零继承
默认链，负值禁用。
- **`AdaptOAuthProvider`/provider 结构体。** TS 可选方法接口映射为函数字段结构体
，单飞刷新共享同一错误。

## Alternatives considered

**等基准到达 `a276dabe5`。** 否决：MCP 是被请求的能力且分层清晰、今天即可干净移
植；等待会把它与它从不触碰的 TUI/durable 工作耦合。

**移植整个区间。** 否决：codemode（QuickJS）、pi-durable 与 TUI 重写远超 MCP 栈且
依赖未移植的宿主；移植会变成一切的残缺拷贝。

**现在就给扩展接线打桩。** 否决：伪造会话宿主会冻结错误语义；连接层正是参考自身
的切分点（runtime.lazy.ts）。

## Consequences

`mcp/` 固定于 `packages/mcp@a276dabe5`，其余树保持 `ff72faba2`；PARITY.md 记录逐 文件映射与延迟清单，PORTING.md 承载阶段行。`openagent mcp` CLI 今天即可使用（对 本地 stdio 服务器端到端验证）；会话内 MCP 工具随 CA6 扩展宿主工作到来。验证： `gofmt -l .`、`go vet ./...`、`go build ./...`、`go test ./...`、 `node scripts/run-gates.mjs`。创建时超替检查：无先前 note 覆盖 MCP 或参考基准例外。
