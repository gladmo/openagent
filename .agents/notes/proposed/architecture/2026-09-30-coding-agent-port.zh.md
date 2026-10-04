# Agent Note: 将 pi-coding-agent 与 pi-tui 移植为 Go

Status: proposed

[English](2026-09-30-coding-agent-port.md) | 中文

## Problem

仓库已完成 pi agent 栈（`agent` + `ai`/`chord`/`telemetry` 闭包；PARITY.md P0–P9）的 1:1 移植。剩余的产品面是 `pi/packages/coding-agent`（约 75.3K 行源码、203 个测试文件）：`pi` CLI，含会话、自带的工具实现、compaction、扩展系统、print/json/rpc/interactive 四种模式与 `experimental/` 树。移植它不是既有包的机械延伸：coding-agent 的 `core/` 全面 import pi-tui 包（工具 renderers、能力检测、keybindings、theme、扩展 API 类型），因此 pi-tui（约 18.8K 行 + 48 个测试文件）是前置库而非 interactive 模式的细节。Go 还禁止参考实现依赖的两件事：跨目录的 TypeScript import 环（`core/agent-session.ts` ↔ `modes/interactive/theme/`，经 `SourceInfo`）与经 jiti 在运行时求值 TypeScript 扩展。

## Proposal

按 PORTING.md 跟踪的 CA0–CA10 阶段对两个包做 1:1 移植，沿用既有 TS→Go 映射约定，并附加以下绑定：

- pi-tui 先行完整移植为独立基础包 `tui/`——与 typebox/diff/ignore 同一先例（用户已确认）。
- coding-agent 落为镜像 `src/` 目录的包树 `codingagent/`，根文件放包根；二进制为 `cmd/openagent`（用户确认的名字），`rpc-entry.ts` 落为 `cmd/openagent-rpc`。
- import 环经共享叶子包破除：被双向引用的类型（首例 `core/source-info.ts`）下沉到 `codingagent/shared/`，每次下沉在 PARITY.md 记录为位置偏差。
- 强制偏差，逐项记录于 PARITY.md：jiti 运行时 TS 扩展加载变为 Go 原生 `ExtensionAPI`，保留 manifest 发现、TS-only 扩展 loud 失败；NAPI 剪贴板变为 OSC52 + 系统剪贴板命令；photon WASM 图像处理变为保持可观测行为的 Go `image` 管线；highlight.js/marked/grok-mermaid 变为测试钉定的最小手写子集或占位；`bun/` 运行时文件无 Go 等价物（cmd 入口替代）；Windows 专属路径按 GOOS 门控。
- `experimental/` 纳入范围，作为最终阶段（用户确认）；它构建在已移植完成的 harness 运行时与 pico3 之上。
- 近零依赖政策不变（仅 yaml.v3）：semver/minimatch/proper-lockfile 成为对 npm 原版做 golden 测试的小型手移植包；为 Windows 控制台引入 `golang.org/x/sys` 需要单独的 note。

## Alternatives considered

**先在部分 tui 子集上移植 headless 的 coding-agent，之后再补全 tui。** 放弃（用户决策）：该子集已覆盖 tui 大部（颜色、能力检测、组件模型），拆分节省有限，却让组件模型被钉定两次。

**嵌入 JS 运行时（goja）以保留 jiti 扩展加载。** 放弃：pi 扩展 import npm 包与 Node API，嵌入式引擎无法解析；其失败模式比显式 Go 原生 API 加 loud 诊断更糟。

**完全跳过 experimental/。** 放弃（用户决策）：PARITY.md 会永久挂一块 ⬜；它需要的 harness 运行时与 pico3 已经移植完成。

**让 coding-agent 的工具复用 `agent/harness/tools`。** 放弃：coding-agent 的工具是独立实现（自有 shell 工具、output accumulator、renderers、扩展包装），行为不同；全新移植保持 1:1 保真。

## Acceptance criteria

- `tui/` 与 `codingagent/` 逐文件镜像参考实现；PARITY.md 携带完整审计表（✅/🟡/⬜ 加已文档化偏差）。
- `go build ./...`、`go vet ./...`、`go test ./...`、`gofmt -l .` 全绿；`node scripts/run-gates.mjs` 通过。
- `cmd/openagent` 可运行 print、JSON-event、RPC、interactive 四种模式，行为对齐参考实现（faux provider 端到端冒烟）。
- 会话 JSONL、RPC 协议消息、theme JSON 校验由测试做字节级/快照钉定。

## Risks

范围约 94K 行 TypeScript 加核心测试子集；PORTING.md 的阶段表是跨多轮进展的检查点。终端渲染保真（差分渲染器、编辑器、markdown）是最难做字节级钉定的面——用 JS 参考生成的 golden 测试缓解。Windows 控制台支持可能迫使引入第二个外部依赖，那将需要单独的 note 与用户确认。
