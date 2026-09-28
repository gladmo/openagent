# Agent Note 决策记录

[English](README.md) | 中文

这里只存放一类设计文档。**Agent Note** 记录影响本代码库的决策或提案——*为什么*与*放弃了什么*，即代码和文档无法承载的部分。本文件定义 note 的存放位置、撰写时机与文件内格式。

## 布局与命名

每个 note 有两个维度，都编码在其**路径**中——`{lifecycle}/{class}/yyyy-mm-dd-topic-title.md`：

- **生命周期**（顶层目录）是 note 的状态，note 随状态变化在目录间移动：**`proposed/`**（实现前接受评审）、**`implemented/`**（决策已发布，与实际发布内容保持同步）、**`rejected/`**（经过考虑并被否决）、**`archived/`**（冻结的历史；仅限 implemented 的 note）。
- **分类**（嵌套目录）是决策的*种类*——见[分类](#classification)。

文件名中的日期是议题**首次提出**的日期。note 之间的交叉引用使用相对 Markdown 链接，绝不使用裸文字或编号，因此可被机器校验且能在目录间移动后存活。不要添加集中的 `INDEX.md`；生命周期树就是工作清单。

## 分类

每个 note 归入下方闭集中恰好一个路径编码的分类；`verify-agent-note-classification` 拒绝其他目录。

| 分类 | 覆盖范围 |
|---|---|
| `feature` | 新的用户或模型可见能力。 |
| `bug-fix` | 修正缺陷，或填补复盘暴露的缺口。 |
| `simplification` | 在不增加能力的前提下删除代码、行为或表面。 |
| `architecture` | 关于已发布源码的结构性决策——模块如何关联、词汇表是什么。 |
| `process` | 代码周边的工具、策略或工作流，不是运行时行为。 |
| `testing` | 测试基础设施与策略。 |

`architecture` 与 `process` 的分界：architecture 关于我们发布的源码；process 是周边的工具与工作流。（刻意不设 `refactor`——它与 `simplification` 重叠，后者的判据"可观察行为是否改变？"已覆盖它。）

## 归档与删除

当已发布的决策完备、其依据不太可能指导未来工作时，归档对应的 implemented note。绝不归档 proposed 的 note：过时的提案应改为拒绝。rejected 的 note 仅在其依据能阻止一个合理错误时保留；否则成对删除完整文件。归档路径编码为 `archived/{class}/yyyy-mm-dd-topic-title.md`。

归档变更移动完整文件对、保留 `Status: implemented`、在两个语言文件的 Status 行正下方插入相同的 `Archived: YYYY-MM-DD` 行，并修复或删除指向它的链接。一旦封存，归档对永久冻结：不编辑、不翻译、不重排、不更新，也不将其当作当前行为的权威（[归档策略](archived/AGENTS.md)）。

## 何时撰写

仅当变更包含代码、测试和现有文档都无法解释的持久决策依据时，才在同一变更中新增或更新 note。重大未来工作的提案从 `proposed/` 开始；已做出的决策从 `implemented/` 开始。更新已拥有该决策的 note 即满足规则；不要创建重复。机械或局部修改豁免。

一个 note 绝不会被编辑成*另一个决策*：用新 note 取代它并保持两者交叉链接，除非旧 note 被完全并入新的拥有者——在同一变更中保留每一条独特依据、备选方案、后果与必需验证，并修复所有指向旧 note 的链接，然后才能删除。

## 文件格式

每个活跃 note 遵循同一格式，由 `verify-agent-note-format` 强制。归档 note 保留封存时的格式外加归档日期行。

### 头部块

每个 note 的前三行恰好是：

```markdown
# Agent Note: <title>

Status: <status>
```

后跟一个空行。`Status:` 值为三种形式之一，且必须与所在生命周期目录一致：

- `Status: proposed`
- `Status: implemented`
- `Status: rejected — <why, in one line>`

状态不带日期：文件名持有首次提出日期，其余归 git。拒绝理由是唯一带内容的状态，因为被拒 note 的结论正是读者要找的事实。

### 正文骨架

每个 note 的正文以 `## Problem` 开头——动机，须脱离解决方案仍可理解。复现小节使用这些规范名称；真正定制的技术小节可自由插在必需小节之间。

`proposed/`：

```markdown
## Problem
## Proposal
…bespoke sections…
## Alternatives considered
## Acceptance criteria
## Risks
```

`implemented/`：

```markdown
## Problem
## Decision
…bespoke sections…
## Alternatives considered
## Consequences
```

`## Decision` 以现在时描述已发布的现实，整个文件按 [implemented/AGENTS.md](implemented/AGENTS.md) 与其保持同步。提案期标题在 `implemented/` 中属于规格腔，验证门会拒绝。被拒的 note 是冻结的提案：保留提案期的任何小节，结论写在 `Status:` 行上。

### 备选方案——强制

每个 note 携带 `## Alternatives considered` 小节：每个真实备选方案及其落败原因，每个方案一段加粗开头，或有争议的方案用 `### Why not <X>?` 子小节。没有记录对手的决策会招致重新争论——这正是 Agent Note 要防止的失败。

### 生命周期迁移

在生命周期目录之间移动文件，意味着在同一变更中更新 `Status:` 行并满足目标目录的骨架。具体地，`proposed/` → `implemented/` 把 `## Proposal` 改写为现在时的 `## Decision`，并把 `## Acceptance criteria` 与 `## Risks` 并入 `## Consequences`；`proposed/` → `rejected/` 只在 `Status:` 行加入理由并冻结文件。

### 中文对应文件

`.zh.md` 对应文件与英文版逐小节镜像；机器校验的记号（`# Agent Note: `、`Status:` 行，以及 `verify-agent-note-format` 要求的小节标题）逐字保留英文。`verify-agent-note-format` 检查两种语言；`verify-doc-pairs` 检查成对性。
