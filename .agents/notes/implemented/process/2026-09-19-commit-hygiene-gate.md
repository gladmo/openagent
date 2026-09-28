# Agent Note: Commit hygiene gate

Status: implemented

English | [中文](2026-09-19-commit-hygiene-gate.zh.md)

## Problem

The pre-commit hook ran the documentation gates only when a `*.md` file was staged — the `glob` in [lefthook.yml](../../../../lefthook.yml) — so a code-only commit passed with no gate at all, and nothing enforced the root convention that files end with exactly one trailing newline. Worse, clones carried no installed hook: `.git/hooks` held only samples, and an unfilled, unquoted pre-push command slot made lefthook.yml unparseable, so even `lefthook install` failed.

## Decision

[verify-commit-hygiene.mjs](../../../../scripts/verify-commit-hygiene.mjs) joins the suite as its first, file-type-agnostic gate. The corpus is the git index (`git ls-files`), so staged additions are checked and staged deletions drop out, while content is read from the working tree like every other gate. Every tracked text file must end with exactly one trailing newline and carry no trailing whitespace and no leftover merge-conflict markers; binaries (NUL byte), non-regular files, and unreadable paths are skipped and the counts printed so an empty corpus stays visible.

[run-gates.mjs](../../../../scripts/run-gates.mjs) keeps its single-entrypoint role, and [lefthook.yml](../../../../lefthook.yml) drops the `*.md` glob so every commit — code or docs — runs the full suite. Clones without the lefthook binary install the same command as a plain `.git/hooks/pre-commit` shell shim, and the pre-push command slot stays quoted so an unfilled command fails loud instead of breaking hook installation.

## Alternatives considered

**Husky + lint-staged + Prettier.** Rejected: they need a package.json and devDependencies, while this scaffold's scripts are zero-dependency by contract ([scripts/AGENTS.md](../../../../scripts/AGENTS.md)) and run on `node:` builtins alone.

**Ship only the lefthook config.** Rejected: this clone vendors no lefthook binary — there is no package.json — so its availability on PATH is environment luck, and lefthook's generated shim exits 0 when it cannot find the binary: the gate would silently stop blocking commits. The plain hook needs only git and node.

**Check staged blobs via `git show :path`.** Rejected: it spawns git once per file to read index content precisely, while the sibling gates already read the working tree; the corner case it fixes — unstaged dirty edits failing a commit whose staged content is clean — is accepted as the documented trade rather than bought with divergent machinery.

**Walk the filesystem like the Markdown gates.** Rejected: untracked scratch files would block commits; the index is exactly the set of paths a commit admits.

## Consequences

Every commit runs the full gate suite — sub-second, offline — and a commit whose index contains hygiene violations is rejected with file-and-line detail. Unstaged working-tree edits of tracked files can fail a commit whose staged content is clean; the documentation gates already have the same property. Running `lefthook install` later replaces the plain shim with lefthook's own, which then requires the binary to be findable at commit time. The rule set is deliberately minimal — EOF newline, trailing whitespace, conflict markers; widening it is a new decision that supersedes this note.
