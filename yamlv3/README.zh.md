# yamlv3 — 整体内联的 gopkg.in/yaml.v3

`yamlv3` 是 [gopkg.in/yaml.v3](https://github.com/go-yaml/yaml) 在 **v3.0.1** 标签（上游最终版本，2022-04；此后项目处于休眠状态）的逐字树内副本。包名保持 `yaml`；导入路径为 `github.com/gladmo/openagent/yamlv3`。

## Why vendored（为何内联）

openagent 以零外部模块依赖构建。YAML 仅用于解析 `---` frontmatter（技能与 prompt 模板），但输入是用户可写的任意 YAML，其接受/拒绝语义已被 parity 测试钉死在 yaml.v3 上——因此整体上游实现直接入树，而非手写子集。依据与备选方案见[决策记录](../.agents/notes/implemented/architecture/2026-10-25-vendor-yaml-v3.zh.md)。

## Copied from upstream v3.0.1（自上游复制的内容）

全部 13 个非测试 `.go` 文件（`apic`、`decode`、`emitterc`、`encode`、`parserc`、`readerc`、`resolve`、`scannerc`、`sorter`、`writerc`、`yaml`、`yamlh`、`yamlprivateh`）以及 `LICENSE`、`NOTICE`。

未复制：上游 `*_test.go`（依赖 `gopkg.in/check.v1`）、上游 `go.mod`、`README.md`、`.github/`。`yamlv3_test.go` 是本仓库自有的子集测试。

## Deviations from upstream（与上游的偏差）

唯一的机械改动：对 `apic.go`、`emitterc.go`、`parserc.go`、`readerc.go`、`scannerc.go`、`writerc.go`、`yaml.go`、`yamlh.go`、`yamlprivateh.go` 应用了 `gofmt`（Go ≥ 1.19 doc-comment 规范化——制表符缩进代码块、删除注释尾随空行）。仅注释空白，无代码变化。其余逐字节一致。

对原始模块缓存的审计：

```sh
go mod download gopkg.in/yaml.v3@v3.0.1
diff -r yamlv3 "$(go env GOMODCACHE)/gopkg.in/yaml.v3@v3.0.1"
```

预期输出：上面列出的未复制文件、本仓库新增文件，以及仅注释格式的差异。

## Updating（更新）

上游已冻结，预期没有版本升级。若必须回移安全修复：在此应用、在本节列出，并用 Agent Note 记录决策。

## License（许可）

Apache License 2.0（Copyright 2011-2019 Canonical Ltd），其中自 libyaml 移植的部分为 MIT 许可；见本目录 `LICENSE` 与 `NOTICE`。
