# Agent Note: 将 gopkg.in/yaml.v3 整体内联，实现零外部依赖

Status: implemented

[English](2026-10-25-vendor-yaml-v3.md) | 中文

## Problem

go.mod 唯一的外部依赖是 `gopkg.in/yaml.v3 v3.0.1`，go.sum 还额外带着它仅测试使用的依赖 `gopkg.in/check.v1`。上游自 2022-04 起冻结：稳定，但不会再有维护。本仓库的消费面只有三处 `yaml.Unmarshal`，全部解析 `---` frontmatter（codingagent 的 `ParseFrontmatter`、harness 的 skills 与 prompt 模板），但输入是用户可写的任意 YAML，其语义——标量解析、flow 序列、接受/拒绝边界——已被 parity 测试钉死在 yaml.v3 上（`argument-hint: [file]` 解析为序列，字符串断言因此失败）。问题即：在不漂移任何已钉死行为的前提下移除外部模块依赖。

## Decision

把 v3.0.1 的全部 13 个非测试源文件连同 LICENSE、NOTICE 逐字复制进 [yamlv3/](../../../../yamlv3/README.md)；包名保持 `yaml`，消费端只改 import 路径（codingagent/utils_core.go、agent/harness/skills.go、agent/harness/prompt_templates.go），调用点代码零改动。唯一的机械偏差是对 9 个文件应用 `gofmt`（Go ≥ 1.19 doc-comment 规范化）——仅注释空白，无代码变化——偏差清单与审计配方（对原始模块缓存 `diff -r`）由 yamlv3/README.md 持有。上游测试套件（`gopkg.in/check.v1` 存在的唯一原因）不随附；改由 yamlv3/yamlv3_test.go 钉住被消费的子集——map/struct 解码、标量解析、flow 序列不是字符串、空文档/错误文档——并由既有的 frontmatter、skills、模板测试兜底。go.mod 以零 `require` 行收尾，go.sum 删除。

## Alternatives considered

**手写 YAML 子集解析器。** 三处调用点只需要块映射标量，但输入是任意用户 YAML：要在所有输入上对齐 yaml.v3 的接受/拒绝边界与错误语义，等于重写一个正确的 YAML 子集并永久自行维护，且按本移植自身的规则，任何对不齐都是 parity 缺陷。否决。

**仅内联解码路径（删去 encode.go/emitterc.go/writerc.go/sorter.go）。** 少约 2.7k 行，但修改了上游源码，失去逐行 `diff -r` 的可审计性；冻结副本里无用的 encode 路径并无危害。否决。

**`go mod vendor`。** 源码进树但 go.mod 仍保留 require 与模块图元数据；本仓库想要的性质——`GOPROXY=off` 且模块图为空的构建——没有达成。否决。

**保留依赖。** 上游休眠只意味着"稳定"，仅此而已；安全响应照样需要本地 fork 才能打补丁，且移植的近零依赖惯例（手写 NFD 而不引入 `golang.org/x/text`）早已定下方向。否决。

## Consequences

go.mod 无任何 require；`go list -m all` 只报告主模块，`GOPROXY=off` 下构建与测试全绿，编译不再触网、不再触碰模块缓存。`gopkg.in/check.v1` 连同拖它进来的上游测试一起离开依赖图。安全责任转入树内：未来任何 CVE 都须在本地评估并回移到 yamlv3/，记录在该 README 的 Updating 节——被接受，因为被解析的输入是本地用户文件（SKILL.md、prompt 模板），不是网络载荷。行为按构造不变化：源码逐字，消费端测试零修改通过，含 `[file]` 钉子。验证：`gofmt -l .`、`go vet ./...`、`go build ./...`、`go test ./...`、`node scripts/run-gates.mjs`。Supersession check at creation: no active note owns the dependency strategy; the coding-agent-port proposal's "yaml.v3 only" bullet is background context, not a decision owner.
