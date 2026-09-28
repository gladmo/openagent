# Agent Note: 提交卫生门禁

Status: implemented

[English](2026-09-19-commit-hygiene-gate.md) | 中文

## Problem

提交前钩子只在暂存了 `*.md` 文件时才运行文档门禁——即 [lefthook.yml](../../../../lefthook.yml) 里的 `glob`——于是只包含代码的提交完全不经过任何门禁，"文件以恰好一个换行符结尾"这一根约定也没有任何检查在执行。更糟的是，克隆里根本没有安装钩子：`.git/hooks` 下只有样例，而未加引号、未填充的 pre-push 命令槽让 lefthook.yml 无法解析，连 `lefthook install` 都会失败。

## Decision

[verify-commit-hygiene.mjs](../../../../scripts/verify-commit-hygiene.mjs) 作为套件中第一个不区分文件类型的门禁加入。检查语料是 git 索引（`git ls-files`），因此暂存的新增文件会被检查、暂存的删除会自然退出语料，而内容与其他门禁一样从工作树读取。每个被跟踪的文本文件必须以恰好一个换行符结尾，且不含行尾空白和残留的合并冲突标记；二进制文件（NUL 字节）、非普通文件和不可读路径会被跳过并打印计数，空语料因此始终可见。

[run-gates.mjs](../../../../scripts/run-gates.mjs) 保持唯一入口的角色，[lefthook.yml](../../../../lefthook.yml) 去掉了 `*.md` glob，使每一次提交——无论代码还是文档——都运行完整套件。没有 lefthook 二进制的克隆把同一条命令安装为普通的 `.git/hooks/pre-commit` shell 垫片；pre-push 命令槽保持引号包裹，未填充时它会响亮地失败，而不是破坏钩子安装。

## Alternatives considered

**Husky + lint-staged + Prettier。** 否决：它们需要 package.json 和开发依赖，而本脚手架的脚本按契约零依赖（[scripts/AGENTS.md](../../../../scripts/AGENTS.md)），仅使用 `node:` 内建模块。

**只发布 lefthook 配置。** 否决：本克隆没有固定安装 lefthook 二进制——没有 package.json——它在 PATH 上是否可用全凭环境，而 lefthook 生成的垫片在找不到二进制时以 0 退出：门禁会静默地不再拦截提交。普通垫片只需要 git 和 node。

**通过 `git show :path` 检查暂存 blob。** 否决：它要为每个文件调用一次 git 来精确读取索引内容，而姊妹门禁读的都是工作树；它能修复的角落情形——未暂存的脏编辑导致暂存内容干净的提交失败——作为已记录的取舍被接受，不值得为此引入分叉的机制。

**像 Markdown 门禁那样遍历文件系统。** 否决：未被跟踪的临时文件会阻塞提交；索引恰好就是一次提交所接纳的路径集合。

## Consequences

每次提交都会运行完整门禁套件——亚秒级、离线——索引中含卫生违规的提交会被拒绝，并给出文件与行号细节。被跟踪文件的未暂存工作树编辑可能导致暂存内容干净的提交失败；文档门禁本来就具有同样的性质。之后运行 `lefthook install` 会用 lefthook 自己的垫片替换普通垫片，那时提交时必须能找到该二进制。规则集刻意保持最小——EOF 换行、行尾空白、冲突标记；扩充规则是取代本笔记的新决策。
