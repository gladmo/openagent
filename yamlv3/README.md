# yamlv3 — vendored gopkg.in/yaml.v3

`yamlv3` is a verbatim in-tree copy of [gopkg.in/yaml.v3](https://github.com/go-yaml/yaml) at tag **v3.0.1** (the final upstream release, 2022-04; the project is dormant since). The package name stays `yaml`; import it as `github.com/gladmo/openagent/yamlv3`.

## Why vendored

openagent builds with zero external module dependencies. YAML is used only to unmarshal `---` frontmatter (skills and prompt templates), but that input is arbitrary user-authored YAML and its accept/reject semantics are pinned to yaml.v3 by parity tests — so the full upstream implementation ships in-tree rather than a hand-rolled subset. Rationale and alternatives: [decision note](../.agents/notes/implemented/architecture/2026-10-25-vendor-yaml-v3.md).

## Copied from upstream v3.0.1

All 13 non-test `.go` files (`apic`, `decode`, `emitterc`, `encode`, `parserc`, `readerc`, `resolve`, `scannerc`, `sorter`, `writerc`, `yaml`, `yamlh`, `yamlprivateh`) plus `LICENSE` and `NOTICE`.

Not copied: upstream `*_test.go` (they require `gopkg.in/check.v1`), upstream `go.mod`, `README.md`, `.github/`. `yamlv3_test.go` is this repo's own subset test battery.

## Deviations from upstream

One mechanical change: `gofmt` (Go ≥ 1.19 doc-comment canonicalization — tab-indented code blocks, dropped trailing comment lines) applied to `apic.go`, `emitterc.go`, `parserc.go`, `readerc.go`, `scannerc.go`, `writerc.go`, `yaml.go`, `yamlh.go`, `yamlprivateh.go`. Comment whitespace only; no code change. Everything else is byte-identical.

Audit against the pristine module cache:

```sh
go mod download gopkg.in/yaml.v3@v3.0.1
diff -r yamlv3 "$(go env GOMODCACHE)/gopkg.in/yaml.v3@v3.0.1"
```

Expected output: the not-copied files listed above, this repo's added files, and doc-comment reflow only.

## Updating

Upstream is frozen; no upgrades are expected. If a security fix must be backported, apply it here, list it in this section, and record the decision in an Agent Note.

## License

Apache License 2.0 (Copyright 2011-2019 Canonical Ltd) with MIT-licensed portions ported from libyaml; see `LICENSE` and `NOTICE` in this directory.
