# codingagent

[English](codingagent.md) | 中文

`codingagent` 是 pi-coding-agent v0.87.1 的 Go 移植：编码代理 CLI，含会话、自有工具实现、压缩、扩展系统与 print/JSON-event/RPC/interactive 模式。二进制入口为 `cmd/openagent`。移植清单见 [PARITY.md](../../PARITY.md)；逐轮记录见 [PORTING.md](../../PORTING.md)。

## 配置

- `config.go` 解析安装方式、包资产路径（主题、导出模板、交互资产，经 PI_PACKAGE_DIR 覆盖）、package.json 应用标识（名称/版本 → 环境变量名）与 `~/.pi/agent/` 下的用户配置路径（models.json、auth.json、settings.json、sessions）。
- `settings_manager.go` 以递归深合并合并全局（`~/.pi/agent/settings.json`）与项目（`<cwd>/.pi/settings.json`）作用域，迁移遗留键并做项目信任门控。

## 模型栈

- `model_runtime.go` 组合内置提供者与 models.json 覆盖及扩展注册；可用性快照驱动模型选择。
- `model_resolver.go` 解析模型模式（精确、含别名优先的部分、glob）与思维级别后缀。
- `provider_composer.go` 分层内置 + models.json + 扩展提供者配置。

## 会话

- `session_manager.go` 持久化只追加 JSONL 树：条目类型（消息、模型/思维变更、压缩、分支摘要、上下文编辑、标签）、版本迁移（v1→v2→v3）、惰性文件创建、带摘要的分支、保溯源投影。

## 工具

`core/tools` 移植 bash（共享 shell 工具工厂覆盖 PowerShell）、edit（+ diff）、read（图像附件、截断通知）、write、grep（ripgrep JSON 事件）、find（fd glob）、ls——各自带可插拔操作用于远程委托，mutation 队列串行化同文件写入。

## 压缩

- `compaction.go` 计算 token 估算与切割点；LLM 摘要调用随运行时阶段接续。

## 模式

- `print_mode.go` 服务 `--mode text|json`；`rpc_mode.go` 经 stdin/stdout 分发 JSONL 命令；`interactive_mode.go` 驱动 TUI 会话（编辑器输入分发、实时 agent 事件渲染、真实运行、状态/页脚布局、经 `/compact` 的压缩）。`cli_args.go` 解析旗标表；`cmd/openagent/main.go` 为二进制入口（interactive 需要 TTY）。

## 压缩

`session_compaction.go` 执行完整链路：路径准备（会话投影上的切点规划）、LLM 摘要（初始/更新提示、分裂轮合并）、持久化检查点（`CompactionEntry` + `compaction_end` 事件）。`ShouldAutoCompact` 门控基于阈值的自动压缩。

## 扩展

`extension_runner.go` 向 Go 原生扩展分发事件（jiti TS 加载偏差）；注册覆盖工具、命令、旗标与快捷键。

## 已知限制

- 17 个选择器类交互组件（tree/config/session/settings 选择器、armin、mermaid）经缝合消息；对应命令报告不可用而非打开选择器。
- 扩展加载器（TS 模块发现/校验）与 `experimental/`（CA9）为最小核心范围决定下的文档化偏差；见 PARITY.md。
- resource-loader、sdk.ts、cache-warmer、migrations、rpc-client、startup-ui、theme-controller、changelog 未移植；见 PARITY.md 缺口清单。
- remote-catalog 刷新与 radius 提供者为文档化缺口。
