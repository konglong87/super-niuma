> **Super Niuma — your super workhorse.** This independent successor preserves the traditional conversation entry and adds an isolated Super work-order entry. The initial milestone records requests only; autonomous execution is not enabled. See the [delivery ledger](docs/plans/2026-11-03-super-niuma-evolution.md) and [architecture](docs/architecture/super-niuma.md). The retained traditional capabilities are described below.

## Two entries, separate responsibilities

- **Super intake:** `go run ./cmd/super-niuma` — durable explicit requests, acceptance criteria and cancellation. No automatic execution.
- **Traditional conversations:** `go run ./cmd/niuma --help` — retained conversation/runtime behavior.

Super binds only to loopback and owns a separate `~/.super-niuma` store. It does not read old sessions or model credentials. See the [Super guide](docs/usage/super-niuma.md). No binary release has been published for this successor yet.

<p align="center">
  <img src="web/src/v2/assets/niuma-mark.svg" alt="Niuma mascot mark" width="144">
</p>

<h1 align="center">Niuma <small>牛马</small></h1>

<p align="center"><strong>Let AI do the grind. Go live your life.</strong></p>

<p align="center">
  An open-source, local-first AI agent for moving long-running work forward.<br>
  Give it a goal. It works in the background, verifies progress, and hands control back when blocked.
</p>

<p align="center">
  <a href="docs/getting-started.md">Build from source</a> ·
  <a href="docs/getting-started.md">Get started</a> ·
  <a href="docs/use-cases.md">Use cases</a> ·
  <a href="https://github.com/konglong87/super-niuma/discussions">Discussions</a> ·
  <a href="README.md">中文</a>
</p>

<p align="center">
  <a href="https://github.com/konglong87/super-niuma/actions/workflows/ci.yml"><img src="https://github.com/konglong87/super-niuma/actions/workflows/ci.yml/badge.svg?branch=main" alt="CI"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/konglong87/super-niuma" alt="Apache-2.0"></a>
  <img src="https://img.shields.io/badge/Go-1.26%2B-00ADD8?logo=go&logoColor=white" alt="Go 1.26+">
</p>

<p align="center">
  <img src="docs/web_agent/images/niuma-desktop-v2.png" alt="Niuma Desktop — real workspace with the new logo and Niuma 3D companion" width="900">
</p>

<p align="center"><em>Actual Niuma desktop workspace (Chinese UI), with the new logo and Niuma 3D pet—not a composited mockup.</em></p>

### Meet your desktop Niuma

<p align="center">
  <img src="web/public/assets/pets/niuma.png" alt="Niuma 3D model render with an orange body, cream muzzle and mint work beacon" width="168">
</p>

Enable it in **Settings → Pet**. **Niuma** is the default model; **Go Companion** and **Chinese Dragon** remain available. Existing saved selections are preserved.
Switch between idle, focus and celebration animations, drag, resize or hide your companion. The small portrait above is a transparent render of the same model, not a desktop screenshot. See [desktop personalization](docs/usage/desktop-personalization.md).

## What is Niuma?

**Niuma is an open-source AI execution workspace for work you shouldn't have to watch.**
It can understand a project, plan steps, use tools, edit files, run commands and tests, and move
long-running tasks forward with background jobs, scheduled loops, session recovery and checkpoints.

It is not a black box that promises to run forever. Tasks remain subject to your device, model
provider, budgets and permissions. When it needs authorization, reaches a budget, or cannot make
progress, Niuma pauses, explains why and hands the decision back to you.

### What can it do for you?

- **Keep work moving while you're away** with background goals and scheduled loops.
- **Resume after interruptions** with session recovery, checkpoints and rewind.
- **Show its work** through task status, run history, logs and verification results.
- **Respect your boundaries** with permission approvals, path controls, budgets and stop controls.
- **Meet you where you work**: Desktop, CLI, TUI, WebUI, API, MCP and Feishu share one runtime core.

> “Always on” refers to background task and scheduling capabilities—not unlimited usage, zero
> failures or guaranteed completion. Actual runtime depends on your device, network, provider,
> task configuration and permissions.

## Get started

### Desktop

This successor has no published desktop binaries yet. Build from source using the [desktop guide](desktop-v2/README.md);
configure your model provider and credentials, then give Niuma a task with clear acceptance criteria.

### CLI

```bash
git clone https://github.com/konglong87/super-niuma.git
cd super-niuma
go run ./cmd/niuma --version
go run ./cmd/niuma -p "Inspect this project, identify one issue, and run a relevant check"
```

Requires Go 1.26+. User settings default to `~/.niuma/settings.json`:

```json
{
  "provider": "custom",
  "providerProtocol": "openai-chat-completions",
  "baseURL": "https://model.example.com/v1",
  "apiKey": "replace-me",
  "model": "model-id"
}
```

See the [getting-started guide](docs/getting-started.md) for setup and troubleshooting.

## More than a chat window

| Use case | Capabilities | What you can inspect |
| --- | --- | --- |
| Long-running work | Background goals, checkpoints, session recovery | Status, completed steps and blockers |
| Recurring work | Loops and a local scheduler | Next run, history and logs |
| Coding | File operations, commands, code search, LSP and Git workflows | Changes and test results |
| Extensibility | Skills, MCP, workflows and browser tools | Tool boundaries and task output |
| Multi-agent work | Profiles, teams, sub-agents and orchestration | Delegation and results |
| Interfaces | Desktop, CLI, TUI, WebUI, API and Feishu worker | Your preferred entry point |
| Model providers | Custom providers, compatible APIs and multimodal extensions | Bring your own service and credentials |

Explore [use cases](docs/use-cases.md) and the [platform architecture](docs/architecture/agent_platform_architecture.md).

## Local-first, with clear boundaries

Niuma uses SQLite by default and also supports MySQL for team deployments. External model providers,
MCP servers and channels must be configured by you. Never commit API keys. Review
[security and permissions](SECURITY.md) before running an agent.

Niuma has its own application identity and default data directories for settings, tasks, sessions,
memory and scheduling. It does not reuse the `go-e2e` application's default data directory.

## Brand

**Let AI do the grind. Go live your life.**

Niuma is an original, friendly bull-horse robot: diligent without overpromising, showing evidence
when done and handing off when it hits a boundary. See the [brand guide](docs/brand/niuma-brand.md).

## Origin and license

Niuma is an independent project based on the source snapshot of
[`go-e2e`](https://github.com/konglong87/go-e2e) at
`fffd62dd06d7b3a09dbc6eebdbbccbb814cea827`, and is distributed under Apache-2.0. See
[`UPSTREAM.md`](UPSTREAM.md) and [`LICENSE`](LICENSE) for details.

## Learn more

- [Getting started](docs/getting-started.md)
- [Use cases](docs/use-cases.md)
- [Configuration reference](docs/configuration.md)
- [WebUI guide](docs/usage/webui.md)
- [Feishu channel](docs/usage/feishu.md)
- [Desktop personalization](docs/usage/desktop-personalization.md)
- [Roadmap](docs/roadmap.md)
- [Changelog](CHANGELOG.md)
- [Contributing](CONTRIBUTING.md)

## Retained Computer Use scope

The retained Computer Use baseline is a **macOS single-display Developer Preview**. A second physical display and formal signing remain outside the accepted scope of this migration. New Super intake does not initiate Computer Use.
