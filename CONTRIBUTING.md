# Contributing

English | [中文](CONTRIBUTING.zh.md)

Thank you for your interest in contributing to openagent!

The port is in progress: before starting work, open an issue at https://github.com/gladmo/openagent/issues describing the area, and check [PORTING.md](PORTING.md) for phase status; pull requests for an area already marked done there are welcome.

## Ways to help

- Report defects and upvote reports in the issue tracker: https://github.com/gladmo/openagent/issues.
- Improve documentation and examples; [docs/AGENTS.md](docs/AGENTS.md) owns the standard.
- Answer questions and help other members of the community.

## Before you submit a pull request

- Read [AGENTS.md](AGENTS.md) for the standing conventions; they apply to every change.
- Run the evidence selected by [pre-push-checks](.agents/skills/pre-push-checks/SKILL.md) for your diff and report the commands you ran; do not push and hope CI differs.
- A durable design decision in the change carries an [Agent Note](.agents/notes/README.md), and every new note triggers the [supersession check](.agents/notes/AGENTS.md).
- Bilingual documents update together: change the `.md` and `.zh.md` pair in the same commit.

## Development setup

See the [development guide](docs/development.md); the testing policy lives in [docs/testing.md](docs/testing.md).
