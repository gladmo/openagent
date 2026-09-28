# Adding a module

English | [中文](adding-a-module.zh.md)

Add one Go package to openagent and wire it into the architecture map, its owning reference page, and the verification gates.

## Steps

1. Create the package: a `<name>/` directory with the `.go` files, a package comment stating the module contract (config, semantics, limitations, extension points), and a `<name>_test.go` beside them.

2. Wire the package in where its dependencies are available — an import in the owning parent; registrations that hold effects return their cleanup (disposer) to the caller.

3. Add the package to the [module map](../architecture.md) with a one-line responsibility.

4. Create the owning reference page under [modules/](../modules/README.md) when the package exposes types or configuration worth referencing.

5. Add the page to the word-budget manifest only if it becomes a standing document.

6. Write the behavior tests that fail for the regression this module exists to prevent; ported code additionally pins its behavior to the pi or npm reference; see [testing policy](../testing.md).

## Verify

```sh
go test ./<name>/ && go vet ./...
node scripts/run-gates.mjs
```

Both commands exit zero when the module is wired correctly and its documentation resolves.
