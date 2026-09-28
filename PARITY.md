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
  （超时校验/Prepare/截断脚注/错误映射）
- `harness/env/nodejs/`：真实 ExecutionEnv（路径解析、errno 映射、完整 FS、拉式行读、
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
| lane accept/queue/abort | ✅ | AcceptRun（收件箱选取+pending 校验+安装写）、QueueMessage/CancelQueued、RequestAbort（幂等+载荷提取）|
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
| agent-harness.ts 门面 | ✅ | 骨架 + lane 获取/配置 + prompt 驱动 + steer/followUp/nextRun/cancel/abort 转发 + watch/resnapshot（facade 包）|

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
