# 添加模块

[English](adding-a-module.md) | 中文

向 openagent 添加一个 Go 包，并把它接入架构地图、归属参考页与验证门。

## 步骤

1. 创建包：`<name>/` 目录放入 `.go` 文件，包注释陈述模块契约（配置、语义、局限、扩展点），旁边放一个 `<name>_test.go`。

2. 在依赖可用的位置接线——由上层父包导入；持有效果的注册把清理（disposer）返回给调用方。

3. 把包加入[模块地图](../architecture.zh.md)，附一行职责说明。

4. 当包暴露值得参考的类型或配置时，在 [modules/](../modules/README.zh.md) 下创建归属参考页。

5. 仅当该页成为常驻文档时，才把它加入字数预算清单。

6. 编写能让"该模块要防止的回归"失败的测试；移植代码另需把行为钉在 pi 或 npm 基准上；见[测试策略](../testing.zh.md)。

## 验证

```sh
go test ./<name>/ && go vet ./...
node scripts/run-gates.mjs
```

两条命令都退出零，即模块接线正确且其文档可解析。
