# Testing policy

English | [中文](testing.zh.md)

Evidence matches the surface: the narrowest check that would fail for the regression owns the change. Never default to the full suite locally; CI owns exhaustive coverage and the platform matrix. The evidence-selection procedure lives in [pre-push-checks](../.agents/skills/pre-push-checks/SKILL.md).

## Test tiers

- **Unit:** behavior of one package in isolation; fast and deterministic. `*_test.go` files live beside the code they test.
- **Parity / golden:** ported libraries (`jsonx`, `typebox`, `partialjson`, `diff`, `ignore`, `telemetry`, `chord`) are pinned by golden and conformance tests against the npm originals and the pi TypeScript source; the reference behavior is the contract.
- **End-to-end:** live provider smoke tests (`ai/providers_smoke_test.go`) call real APIs and self-skip unless enabled.

## Secrets

Live provider smoke tests run only with `PI_SMOKE_TESTS=1` plus the target provider's key — `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `DEEPSEEK_API_KEY`, `OPENROUTER_API_KEY`, `ZAI_CODING_CN_API_KEY`, and the rest of the table in `ai/env_api_keys.go`. Never print secrets; never commit credentials or recordings that contain them.

## Test quality rules

- Tests describe behavior, not correctness: an assertion fails on the intended regression and verifies observable state rather than restating the implementation.
- A test that passes only when it runs alone is a defect in the test: own every port, temporary path, and child process through teardown.
- Change obsolete behavior together with its tests and explain why in the PR.
- A non-trivial change to user- or model-visible output updates its owning snapshot or expected-output fixture in the same commit.
