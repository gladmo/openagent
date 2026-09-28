# User guide

English | [中文](index.zh.md)

Product-facing documentation for people who use openagent but do not change it. Contributor procedures, decision history, and generated reference tables do not belong here ([tier standard](../AGENTS.md)).

## Install and run

Nothing is published yet; build from a checkout and run the example agent:

```sh
git clone https://github.com/gladmo/openagent && cd openagent
go build ./... && go build -o data_query_agent ./examples/data_query_agent
ZAI_CODING_CN_API_KEY=<key> ./data_query_agent
```

## Guides

- [examples/data_query_agent](../../examples/data_query_agent) — runnable agent over the `ai` provider stack with one custom tool.

## Getting help

Report defects and ask questions in the issue tracker: https://github.com/gladmo/openagent/issues.
