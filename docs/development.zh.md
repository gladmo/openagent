# 开发指南

[English](development.md) | 中文

搭建教程带新贡献者从前置条件走到验证通过的检出；贡献者参考覆盖日常工作流与 CI 组织。测试策略见 [testing.md](testing.zh.md)；设计依据存放在 [Agent Note](../.agents/notes/README.md)。

## 搭建教程

### 前置条件

- Go ≥ 1.27（与 `go.mod` 一致）。
- Node ≥ 18（运行 `scripts/` 下的文档门禁）；git（钩子）。
- 可选：做对齐工作时检出的 pi TypeScript 基准（提交 `ff72faba2`，见 [PORTING.md](../PORTING.md)）；跑真实提供方测试所需的 API 密钥（见 [testing.md](testing.zh.md)）。

### 首次搭建

```sh
go mod download
go build ./... && go vet ./...
```

`go mod download` 拉取唯一依赖（`gopkg.in/yaml.v3`），之后没有生成步骤。钩子可选：运行 `lefthook install`，或手工把 `node scripts/run-gates.mjs` 接进 `.git/hooks/pre-commit`。

`go build ./... && go vet ./...` 成功退出即搭建完成。

## 贡献者参考

### 日常工作流

从 `main` 建分支、修改，然后按 [pre-push-checks](../.agents/skills/pre-push-checks/SKILL.md) 收集聚焦证据：`gofmt -l .` 无输出、`go vet ./...` 通过、所属包测试——`go test ./agent/` 或 `-run TestName`——覆盖你触碰的行为。移植工作在同一变更中更新其覆盖文件的 [PARITY.md](../PARITY.md)。提交主题写清做了什么、为什么。

### CI 组织

尚未配置 CI 通道。CI 将接管的目标集合：`gofmt -l .`、`go vet ./...`、`go test ./...`、`node scripts/run-gates.mjs`。

### TODO 标记语义

`FIXME` 表示必须在下个发布前修复的缺陷；`TODO` 表示已接受但未排期的工作；`XXX` 表示需要设计决策的地雷。每个标记都要为其未来的主人留下足够上下文。
