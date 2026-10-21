# PARITY.md — pi/packages/agent → openagent 导出审计

基准：pi @ `ff72faba2`。本文按源包逐文件审计 Go 侧的移植状态：
**✅ 完成**（含测试）、**🟡 部分**（核心语义已移植，集成面待接）、**⬜ 未移植**。
行数指 TS 源。测试：`go build ./... && go vet ./... && go test ./...` 全绿（21 包）。

## 依赖闭包（P0–P2）

| 源 | 行数 | 状态 | Go 位置 | 备注 |
|---|---|---|---|---|
| jsonx 模型（JS 语义 JSON） | — | ✅ | `jsonx/` | 插入序对象、JS 数字格式化、深拷贝 |
| typebox 1.3.27 | — | ✅ | `typebox/` | 构建器+Value.Convert+Compile/Check；`~kind` 隐藏键语义 |
| partial-json 0.1.7 | — | ✅ | `partialjson/` | |
| jsdiff 8.0.4 | — | ✅ | `diff/` | Myers 行差分 + StructuredPatch/FormatPatch |
| ignore 7.0.8 | — | ✅ | `ignore/` | wildmatch、尾 globstar、POSIX 类 |
| yaml（唯一外部依赖） | — | ✅ | go.mod | gopkg.in/yaml.v3 |
| chord 根（JsonValue/CopyJson） | — | ✅ | `chord/` | 对象= jsonx.Obj |
| chord/context | — | ✅ | `chord/context/` | 链、withoutAbortSignal 遮蔽 |
| chord/delta（op/apply/codec） | — | ✅ | `chord/delta/` | 七元组 op、Apply/ApplyImmutable、encoder/decoder |
| chord/delta tracker（proxy 式） | — | ✅ | `pico3/legacy_tracker.go` | Go 无代理：显式工作副本键差分（文档化等价物）|
| chord/services | — | ✅ | `chord/services/` | 错误码/注册表/槽（最小移植，供 pico3 chord 桥）|

## ai（P3）✅ 全量 + providers 子集（7/46）

`pi/packages/ai` → `ai/`：types（消息/事件）、event_stream、utils（uuidv7/diagnostics）、codec（字节级字段序）、json_parse、retry、estimate、transcript、validation（裸 JSON-Schema强制转换路径——typebox 无 Symbol 检测）、frame（帧编码器/重放）、models_store、faux、models（注册表/CalculateCost/ClampThinkingLevel）、options、faux_provider（完整 createFauxCore）。

### providers（P3b，2026-09-28 本轮）：anthropic / openai / openai-codex / deepseek / zai / zai-coding-cn / openrouter

- **模型目录快照**：`ai/catalog/data/`（42 shard + .manifest.json，go:embed；pi 仓库
  `node scripts/generate-models.ts` 非 strict 生成，models.dev/OpenRouter 可达），
  加载器 `catalog_data.go`（schemaVersion/哈希校验 + flatten chat）。
- **API 客户端**（TS 依赖 OpenAI/Anthropic SDK 0.124/6.40；Go 直连线协议）：
  `api_anthropic_messages*.go`（POST /v1/messages?beta=true、beta 特性、stealth 工具名、
  adaptive/budget thinking、1h 缓存成本、deferred 占位工具、mid-convo system/tool changes、
  跨 API 消息转换）、`api_openai_completions*.go`（compat 解析（detect+catalog 覆盖）、
  zai/deepseek/openrouter 等 thinkingFormat、reasoning_details 增量合并回放、
  grammar 工具流、anthropic cacheControl、prompt_cache_key）、
  `api_openai_responses*.go` + `_shared`（/responses、reasoning.encrypted_content 回放、
  msg/fc id 签名、tool_search/additional_tools、service tier 定价）、
  `api_openai_codex_responses.go`（/codex/responses URL 解析、JWT accountId、
  codex 重试策略 + ChatGPT 用量友好错误、response.done/end_turn 归一）。
- **基础设施**：`auth_provider.go`（env-key 解析 + InMemory 凭证存储 + resolveProviderAuth）、
  `env_api_keys.go`、`provider_retry.go`（SDK 重试策略）、`error_body.go`（含
  WrapStainlessHTTPError——SDK 错误消息 = `<status> <JSON.stringify(body)>`）、
  `http_sse.go`（fetch seam + abort/timeout + SSE 读取器）、`provider_core.go`
  （createProvider）、`api_simple_options.go`、`api_constrained_sampling.go`、
  `api_transform_messages.go`、`api_copilot_headers.go`、`jsonx_access.go`。
- **注册表**：modelsImpl 接 applyAuth（auth 解析→apiKey/headers/env 合入→派发），
  `providers_all.go` BuiltinProviders()/BuiltinModels()。
- **测试**：4 客户端各带 httptest SSE 回放套件（事件映射/usage+成本/thinking 模式/
  工具流/错误体/abort/重试/请求形状），provider/目录/transform 套件，
  `providers_smoke_test.go`（PI_SMOKE_TESTS=1 + 真实 key 时 1-token 实流冒烟）。
- **偏差（见 PORTING.md）**：OAuth 交互式登录/刷新未移植（槽位保留，报显式错误；
  openai-codex 以 OPENAI_CODEX_API_KEY 承接 ChatGPT token）；codex WebSocket 传输未移植
  （auto→SSE 回退）；zstd 请求压缩省略；pi-user-agent 无内核版本段；
  其余 39 个 provider 目录数据已嵌入、定义未接。

## agent 根（P4）✅ 全量

`pi/packages/agent` 根 → `agent/`：types、stream_fn、agent_loop（runLoop 时序）、agent（Agent 类）、proxy（SSE 客户端）、search。

## harness 顶层 + tools + env（P5）✅ 全量

- `harness/`：types、utils×5、messages、skills、prompt-templates、telemetry schema、
  events 总线、hooks 注册表、execution（gate/tools/assistant）、aliases
- `harness/tools/`：path-utils、image 嗅探、write/read+变更队列、edit（+diff）、bash
  （超时校验/Prepare/截断脚注/错误映射）；`ReleaseMutationQueue` 为 Go-native 补充
  （参考用 WeakMap，Go 无弱引用，需显式释放）
- `harness/toolset/`（openagent-native，非 1:1 移植；同 compaction 出处类）：
  `Adapt`（纯管线适配）+ `Local`（env+gate 捆绑、幂等 Close）+ `Coding()` 四件套，
  把 harness 工具接进裸 agent 循环；seam 决策见 Agent Note `toolset-adapter-seam`
- `harness/env/local/`（移植自参考的 `env/nodejs.ts`；改名见 Agent Note
  `env-local-rename`）：真实 ExecutionEnv（路径解析、errno 映射、完整 FS、拉式行读、
  shell 发现链+进程组树杀、OutputCapture 集成）；spill 文件写延迟（布线完成，文档化）

## session + jsonl（P6）✅ 全量

- `harness/session/`：values（保留命名空间全量）、mutation-line、types（13 叶状态机、
  全接口）、commit（校验）、fork-policy、in-memory-state、session（StorageBackedSession）、
  memory（MemoryStorage/Repo）
- `harness/session/jsonl/`：codec（v4/v3 头、事务、撕裂尾）、io（原子发布）、storage
  （追加前应用、v3 升级、高水位）、legacy_v3（重铸 id/派生值/importedUsage）、repo
  （目录/文件名 JS 编码、list 头过滤、fork）
- `harness/session/testing/`：Decorator/Instrumented/Gating + 六组一致性套件

## runtime（P7）✅ 决策核心 + 供应商流接线完成

| 文件 | 状态 | 备注 |
|---|---|---|
| types.ts | ✅ | Config/LaneState/命令联合 |
| reducer.ts | ✅ | 完整事件表 + 测试 |
| transcript.ts | ✅ | 链/事件/有界读/队列解析 |
| restore.ts | ✅ | 分类/还原/intent 矩阵 |
| lane.go 命令机 | ✅ | command/settle/continue + DrainEvents |
| lane accept/queue/abort | ✅ | AcceptRun（收件箱选取+pending 校验+安装写）、accept 请求分发（RunRequest：prompt 文本/预建消息+images、skill 经 Resources 查找→FormatSkillInvocation、prompt_template→SubstituteArgs；UnknownSkill/UnknownTemplate、pending_assistant 拒绝；LaneOptions.ReadConfig 注入）、QueueMessage/CancelQueued、RequestAbort（幂等+载荷提取）|
| drive.ts | ✅ | 调度循环（abort-continue 语义）|
| drive/boundary.ts | ✅ | 规划器 + 事件 |
| drive/checkpoint.ts | ✅ | startRun/runCheckpoint/finishRunBoundary |
| drive/terminal.ts | ✅ | 清理写 + 结果记录 |
| drive/retry.ts | ✅ | RetryNotBefore + WaitUntil |
| drive/reconcile.ts | ✅ | 取消终态发布 + 每叶分发 |
| drive/compaction 阈值 | ✅ | ShouldCompact/TriggerCompaction/DefaultThreshold |
| drive/response.ts | ✅ | 分类助手 + publishResponse 决策 + 配置失败发布 |
| drive/deferred.ts | ✅ | 句柄解析 + 轮询准备 + 轮询意图发布（许可扣减）|
| drive/recovery.ts | ✅ | 帧前缀归约 + 中断消息 |
| drive/tools.ts | ✅ | 批次助手 + 调度决策（tools_batch/tools_run）|
| drive/tool-placement.ts | ✅ | 源读取/暂存校验/写规划/完成路由/事件渲染 |
| drive/structural.ts | ✅ | 决策核心 + 状态机构造器 + 钩子路由 + 结果发布决策 |
| drive/generation.ts | ✅ | prepare/intent/perform/publish 四阶段全量（models_registry + pipeline）|
| progress.ts | ✅ | 分页 + stillOwns 通道 |
| navigation 提交 | ✅ | ValidateNavigation + CommitNavigation |
| agent-harness.ts 门面 | ✅ | 骨架 + lane 获取/配置 + prompt 驱动 + steer/followUp/nextRun/cancel/abort 转发 + watch/resnapshot + Resources 注入（AgentHarnessOptions.Resources → runtime.Config，queue-mode/tool-execution 默认在此解析；Skill/PromptFromTemplate 驱动入口）（facade 包）|

## pico3（P8）✅ 全量

| 文件 | 行数 | 状态 | 备注 |
|---|---|---|---|
| types.ts | 1038 | ✅ | 记录/Write/扫描/Storage/RuntimeAPI/kind 类型 |
| memory.ts | 278 | ✅ | 原子批/fork 感知扫描/文档折叠/docAsOf |
| jsonl.ts | 339 | ✅ | 发布点协议/重放/撕裂尾/jsonx 编解码 |
| session.ts | 811 | ✅ | 属主/提交线/权威/故障终结/索引/Tx 面 |
| scheduler.ts | 486 | ✅ | 契约校验+门状态机（线集成经 Session 基座）|
| legacy-tracker.ts | 64 | ✅ | flush 基操作/键差分/Rebase |
| view.ts | 470 | ✅ | watch 生命周期/信封/有序投递/三投影/update |
| harness.ts | 812 | ✅ | 注册核心+会话操作+conversation handle（harness.go/ops/handle）|
| system.ts | 376 | ✅ | 段落/Canonical/Draft/Freeze/快照 |
| hooks.ts | 47 | ✅ | 作用域+错误隔离 |
| context.ts | 105 | ✅ | deriveContext §1.3 + 工具结果重排/合成 |
| membrane.ts | 123 | ✅ | Go 句柄式适配 |
| bash.ts | 29 | ✅ | 流式+进程组击杀 |
| bounded.ts | 82 | ✅ | 头/尾保留 + 字节/行双预算 + 丢弃计数（`bounded.go`） |
| chord.ts | ✅ | 服务定义 + ChordViewBridge（`chord_bridge.go`，经 `chord/services`）|
| kinds/ | 2332 | ✅ | 全部 8 文件：entries/task-api/job/plugin/frames/collapse/tool/post-tools/generation（含请求派生与恢复）|

## P9 审计结论（定稿）

**pi/packages/agent @ ff72faba2 → openagent 移植按本审计范围功能完整
（P3 providers 子集见上方偏差：7/46 provider，OAuth 交互流程与 codex WebSocket 传输未移植）。**

- ✅ 全部层均有对应 Go 测试：依赖闭包（含 chord/services 最小移植）、ai 全量、
  agent 根全量、harness 顶层+tools+env、session+jsonl 全量、runtime（决策核心 +
  供应商流接线 + 门面 + 推送式 watch）、pico3 全量（存储/会话/调度/视图/系统/
  kinds 8 文件/harness 门面与 conversation handle/chord 桥）。
- 基线质量门槛：`go build ./...` 通过、`gofmt -l` 空、`go vet` 零告警、
  `go test ./...` **24 包全绿**；providers 轮后约 79,400 行 Go（ai 包 18,973 行/56 文件）。
- 1:1 程度说明：TS 判别联合映射为 Go sealed 结构 + Kind 判别方法；代理式
  membrane/tracker 采用文档化的 Go 等价物（句柄守卫/键差分）；运行时供应商流经
  ai.Provider 流式面注入。逐文件差异记录于 `PORTING.md` 各轮条目。
- 接续点与逐轮记录见 `PORTING.md`。

## MCP 移植：pi/packages/mcp @ a276dabe5（M0–M2）✅🟡

基准例外：MCP 栈取自 `a276dabe5`（用户指定的区间 `ff72faba2..a276dabe5` 的核心功能），其余树仍钉定 `ff72faba2`。决策与分层见 [Agent Note](.agents/notes/implemented/architecture/2026-10-14-mcp-port-ahead-of-baseline.md)。

### mcp/ 包（M0）✅ 全量

pi/packages/mcp（31 文件 ≈ 4.9K 行 TS + 测试）→ `mcp/`（16 文件 ≈ 6.9K 行 Go，32 测试）。

| 参考文件 | Go 位置 | 状态 | 备注 |
|---|---|---|---|
| protocol/jsonrpc.ts | `jsonrpc.go` | ✅ | jsonx 对象承载消息；ID 结构体等值 |
| protocol/types.ts + content.ts | `protocol.go` | ✅ | 类型化视图保留 Raw 透传；ToLlmContent |
| transports/transport.ts | `transport.go` | ✅ | 监听器经条目指针身份（Go 函数不可比较）|
| transports/in-memory.ts | `inmemory.go` | ✅ | queueMicrotask → goroutine 投递 |
| transports/stdio.ts | `stdio.go` + `stdio_unix.go`/`stdio_windows.go` | ✅ | 进程组 Setpgid/taskkill；**偏差**：无 process exit 钩子杀存活进程组 |
| transports/streamable-http.ts | `streamable_http.go` + `sse.go` + `fetch.go` | ✅ | SSE 消费者字节级；fetch 注入抽象（FetchResponse）；TypeError→`IsNetworkError` |
| client.ts | `client.go` | ✅ | 超时/中止/进度重置；**偏差**：超时 0=继承默认、负=禁用（无 Infinity）|
| oauth/types.ts | `oauth_types.go` | ✅ | null/"" 视为缺省 |
| oauth/errors.ts | `oauth_errors.go` | ✅ | |
| oauth/discovery.ts | `oauth_discovery.go` | ✅ | RFC 8414/OIDC 候选 URL、iss 校验 |
| oauth/flow.ts | `oauth_flow.go` | ✅ | PKCE、DCR、刷新、iss 守卫、step-up、`AdaptOAuthProvider`（结构体函数字段）|
| oauth/provider.ts | `oauth_provider.go` | ✅ | 状态序列化 ToObj/FromObj |
| oauth/callback.ts | `oauth_callback.go` | ✅ | 回环服务器 + WaitForCallback 结果信道 |
| auth-provider.ts | `fetch.go`（AuthProvider）| ✅ | 可选方法 → 函数字段 |
| index.ts | `doc.go` + 各文件 | ✅ | 包级导出即 barrel |

### codingagent MCP 运行时（M1）✅ 全量

| 参考文件 | Go 位置 | 状态 | 备注 |
|---|---|---|---|
| core/mcp-servers.ts | `mcp_servers.go` | ✅ | 校验/别名解析/命名空间冲突/注册表 |
| extensions/mcp/config.ts | `mcp_config.go` | ✅ | 全局+项目合并、覆盖键约束、缩进保留编辑 |
| extensions/mcp/log.ts | `mcp_log.go` | ✅ | 追加 + 5MB 轮转 |
| extensions/mcp/oauth.ts | `mcp_oauth.go` + `mcp_file_lock.go` | ✅ | 键=名\|URL、legacy 迁移、刷新锁（**偏差**：proper-lockfile → O_EXCL+mtime 过期接管）|
| extensions/mcp/tools.ts | `mcp_tools.go` | ✅ 转换层 | 命名/截断/转换/schema；ToolDefinition 包装随 CA6 |
| extensions/mcp/resources.ts | `mcp_resources.go` | ✅ 辅助层 | MCP App 过滤；三个资源工具定义随 CA6 |
| extensions/mcp/runtime.ts | `mcp_runtime.go` | ✅ | 重连/会话过期重试/瞬时重试/needs-auth；**偏差**：clientInfo 发 AppName()/Version()（参考发 "pi"）|
| extensions/mcp/cli.ts | `mcp_cli.go` + `cmd/openagent` | ✅ | add/remove/list/login/logout；readline 提示经 bufio |

支撑新增：`ai/oauth_page.go`（pi-ai utils/oauth-page.ts @ a276dabe）、`TruncateMiddle`（truncate.ts）、`AgentToolResult.StructuredContent/IsError`（agent types.ts @ a276dabe，isError 并入结果终结）。

### M2 扩展接线 ⬜（文档化缺口）

`extensions/mcp/index.ts` + `ui.ts` 未移植：依赖 CA4/CA6 的扩展事件宿主（session_start/before_agent_start/tool_call/turn_start/mcp_servers_change/session_shutdown）、ToolDefinition exposure/namespace/outputSchema 字段、setActiveTools、codemode/tool_search 扩展、模型注册表 provider 令牌查询、`/mcp` TUI 管理器。`tools.ts`/`resources.ts` 的 ToolDefinition 包装与渲染器同批落地。

## 扩展移植：pi/packages/tui（CA1）✅

基准：pi-tui v0.87.1 @ `ff72faba2`（43 文件 ≈ 18.8K 行 + 48 个 node:test）→ `tui/`（70 文件 ≈ 37.7K 行 Go，261 测试）。决策与阶段见 [PORTING.md](PORTING.md) C 系列与 [Agent Note](.agents/notes/proposed/architecture/2026-09-30-coding-agent-port.md)。

| 源 | 状态 | Go 位置 | 备注 |
|---|---|---|---|
| colors/terminal-colors | ✅ | `tui/colors.go` | OKLCH↔sRGB、OSC 10/11 |
| unicode_data + EAW（生成） | ✅ | `tui/unicode_data.go` 等 | UCD 16.0.0 生成表；GCB/WB/InCB/RGI/EAW |
| grapheme/word segmenter | ✅ | `tui/grapheme.go` 等 | UAX#29；Intl.Segmenter golden |
| utils（visibleWidth/truncate/wrap） | ✅ | `tui/utils.go` | OSC-8 跟踪、sliceByColumn |
| keys/keybindings/stdin-buffer | ✅ | `tui/keys.go` 等 | Kitty CSI-u、legacy；WT_SESSION 歧义 |
| tui/tui-main-screen（差分渲染） | ✅ | `tui/tui*.go` | 焦点引擎、覆盖层栈、16ms 调度；渲染回调经 defer 重锁（渲染 panic 不再被 "unlock of unlocked mutex" 掩盖）；主屏不实现 ViewportTUI 品牌（对齐参考：仅 alt screen 为 viewport）|
| tui-alt-screen | ✅ | `tui/tui_alt_screen*.go` | 选区/滚动条/搜索/闪烁/kitty 管理 |
| 组件（text/stack/box/loader/image/select/settings/input/autocomplete/editor/markdown） | ✅ | `tui/*.go` | editor 2472 行全量（粘贴标记/kill 环/undo/跳字符/粘滞列）+ `EditorOptions.TopBorder` 缝隙（参考 CustomEditor 覆写 renderTopBorder 的 Go 对应）；markdown 渲染器含编辑器修复 |
| latex + markdown（lexer） | ✅ | `tui/latex*.go` 等 | marked 兼容词法器 27/27 golden；零值 `MarkdownTheme{}` 全样式函数补 identity 默认（此前 nil `ListBullet` 使含列表的回复崩溃）|
| terminal/terminal-image | ✅ | `tui/terminal*.go` | kitty/sixel/iTerm |
| native-{modifiers,platform,module-path} | ⬜ | — | NAPI 原生模块：文档化偏差（无 Go 等价物） |
| index.ts | — | — | 桶文件内联 |

偏差：NAPI 剪贴板 → OSC52 + 命令；ICU CLDR CJK 词典 → 默认 UAX#29 逐字。

## 扩展移植：pi/packages/coding-agent（CA2–CA9）🟡

基准：pi-coding-agent v0.87.1 @ `ff72faba2`（275 文件 ≈ 75.3K 行）→ `codingagent/` + `cmd/openagent`（175 文件 ≈ 61.4K 行 Go，649 测试）。

| 阶段 | 状态 | Go 位置 | 备注 |
|---|---|---|---|
| CA2 config + utils | ✅ | `config.go` 等 | 安装方式检测、资源路径、shell/exec/mime、NFD/NFKC 生成表 + golden |
| CA3 core/tools 执行逻辑 | ✅ | `*_tool.go` 等 | bash/edit/edit-diff/read/write/grep/find/ls/powershell + 截断/输出累加器/mutation 队列；renderers 推迟至 CA8（Theme 依赖） |
| CA4 settings + 资源 | ✅ | `settings_manager.go` 等 | 双作用域 + 迁移 + 信任门控；skills/prompt-templates/上下文文件（AGENTS.md 发现 + worktree 遮蔽） |
| CA5 模型栈 + session + compaction | ✅ | `model_*.go` 等 | resolver/runtime/registry/composer；session-manager 2008 行（迁移/树/投影/惰性持久化）；compaction 纯函数核心 + branch-summarization；运行时密钥覆盖（`SetRuntimeApiKey`）对可用性与请求侧鉴权均可见，Refresh 经 `ResolveProviderAuth` 解析存储/运行时凭据；ComposedProvider 委托 base 传输（Stream/StreamSimple/FetchDeferred），无 base 的 models.json-only provider 返回错误流而非 nil |
| CA6 agent-session 核心 | ✅ | `agent_session*.go`、`session_compaction.go` | 状态机（事件/工具注册表/模型循环/思维级别/排队）+ 系统提示构建 + 压缩全链（准备→LLM 摘要→编排→会话执行/自动阈值）；extensions runner 经 Go 原生 seam；agent 循环对 nil 流 fail loud（provider 无传输时返回失败轮次而非 SIGSEGV）；工具加载对齐 sdk.ts 链：注册表含 powershell 共 8 内置（`createAllToolDefinitions` 键序）、`SetActiveToolsByName` 同步安装 `Agent.SetTools`（loop 每 run 快照声明）、read/bash 构造接 settings（autoResizeImages/commandPrefix/shellPath）、初始集 nil=默认四件套+transcript 恢复 vs 空集=零工具、CLI `--tools/--exclude-tools/--no-tools/--no-builtin-tools` 与 settings `defaultTools`（`+name/-name` 解析）经 `applyPrintToolSelection` 接入所有模式 |
| CA7 无头模式 + CLI + cmd | ✅ | `print_mode.go` 等 | print/json-event/rpc 分发器 + cli args + cmd/openagent 二进制（RPC 端到端验证）；package-manager 核心；cmd 入口 resolveAppMode 对齐 main.ts（TTY 默认 interactive，`-p`/管道 → print），`--list-models` 表格（cli/list-models.ts → `list_models.go`），`--provider/--model/--thinking` 经 resolveCliModel（`model_resolver.go`）接入 print/interactive 会话，`--api-key` 作为运行时覆盖（`SetRuntimeApiKey`，无模型时报参考同款错误）；会话装配 provider-backed StreamFn（`sessionStreamFn`，此前缺失导致任何模式都发不出请求）（CLI 端到端验证：绘制 + /quit + 模型选择 + api-key + 假 provider 全链路请求/鉴权/回复） |
| CA8 interactive | ✅ | `interactive_mode.go` 等 | theme 核心 + 语法高亮 + export-html（ansi/tool-renderer/zip）+ 扩展运行器核心 + 驱动器（输入分发/agent 事件流渲染/真实运行/状态页脚布局）+ 26/43 组件（assistant-message/tool-execution/bash-execution/footer/model-selector/login-dialog/摘要组件/custom 组件/first-time-setup/oauth-selector 等）+ 关键命令（/compact /new /thinking /model /export /name /session /quit）；17 个选择器类组件经缝合（tree/config/session/settings/scoped-models/armin/mermaid 等，见偏差）；组件树按参考重组：document(header/loaded-resources/chat) + dock(status/editor/footer) 经 `mountInteractiveTui` 挂载，`chat_viewport.go` 提供全屏 ScrollView(follow-end/primary) 根；编辑器挂载进 editorContainer（此前漏挂，输入框不可见）；启动头部（logo/键位提示/引导，`quietStartup` 门控），其提示与 `interactive_keys.go` 的应用级动作表同源，只宣传已实现的键；应用级按键经 `setupKeyHandlers` 接入：escape 中断运行/bash、ctrl+c 清空+双击退出、ctrl+d 空编辑器退出、ctrl+o 展开工具输出与启动帮助、shift+tab 思维级别循环、ctrl+p/ctrl+n 模型循环；运行态经 `status_indicator.go` 嵌入编辑器上边框（对应 CustomEditor.renderTopBorder）；助手文本/思考块按值/指针双形式渲染 |
| CA9 experimental | ⬜ | — | **文档化偏差**：范围决定（最小可用核心）— client-tui/server/radius-relay/coordinator/micro/mini（52 文件 10.7K 行）未移植 |

**范围决定后的文档化缺口**（最小可用核心收口）：
- 17 个选择器类交互组件（6.2K 行：tree-selector 1435/config-selector 942/scoped-models 401/armin 382/mermaid/settings-submenu 等）——对应命令保持缝合消息
- extensions/ 加载器与 types 余量（4.5K 行：loader 799/types/index/llama 578）——runner 核心与注册面已移植
- experimental/ 全部（CA9，10.7K 行）
- bash 运行中中断（`AbortBash` 为桩）与 `!` bash 的 transcript 渲染块（当前经 statusLine 显示输出）；resource-loader（1184，启动期「已加载资源」段落与已解析资源的会话接线 —— resolve() 的 skills 自动发现已移植至 `package_manager.go`：项目 .pi/skills + 祖先 .agents/skills（trust 门控）与用户 agent skills + ~/.agents/skills（跨工具池），extensions/prompts/themes 自动发现与 settings 本地条目仍缺）、sdk.ts（454）、cache-warmer（453）、migrations（315）、rpc-client（617）、startup-ui/theme-controller/changelog（655）
- remote-catalog 刷新与 radius 提供者槽位

计划内偏差（[Note](.agents/notes/proposed/architecture/2026-09-30-coding-agent-port.md)）：
- jiti 运行时 TS 扩展求值 → Go 原生 ExtensionAPI；loader 保留发现/清单校验，TS-only 扩展 loud 失败。
- photon WASM 图像管线 → Go `image`（行为对齐：尺寸/格式/EXIF）；highlight.js/marked/grok-mermaid → 最小子集或占位。
- `bun/` 运行时文件无 Go 等价物（cmd 入口替代）；npm 依赖 semver/minimatch/proper-lockfile → `semver/`/`minimatch/`/`lockfile/` 手移植包。
- TS 跨目录 import 环 → 共享类型下沉 `codingagent/shared/`（首例 source-info）。
