# 用户指南

[English](index.md) | 中文

面向使用 openagent 但不修改它的人的产品文档。贡献者流程、决策历史与生成式参考表不属于这里（[层级标准](../AGENTS.md)）。

## 安装与运行

尚未发布任何制品；从检出构建并运行示例 agent：

```sh
git clone https://github.com/gladmo/openagent && cd openagent
go build ./... && go build -o data_query_agent ./examples/data_query_agent
ZAI_CODING_CN_API_KEY=<密钥> ./data_query_agent
```

## 指南

- [examples/data_query_agent](../../examples/data_query_agent) —— 跑在 `ai` 提供方栈上的可运行 agent，带一个自定义工具。

## 获取帮助

在 issue 跟踪器报告缺陷并提问：https://github.com/gladmo/openagent/issues。
