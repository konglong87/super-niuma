# scripts/

Acceptance, side-by-side comparison and capture scripts. Nothing here is built by
`go build`, so nothing here fails a normal build when it rots — which is how a
directory this size ended up with scripts nobody could tell were alive
(AUDIT-P0-19). The classification below says what each script actually needs.

## Start here

```bash
scripts/offline-acceptance.sh                # everything checkable without a key — this is what CI runs
scripts/offline-acceptance.sh --static-only  # skip the scenarios, just the static checks
scripts/go-cache-maintenance.sh --status     # inspect the shared Go build cache
scripts/race-impacted.sh <base-sha>          # race-test changed Go packages and reverse dependents
go run ./scripts/runtime-topology-check       # topology registry, anchors and rendered artifacts
```

The normal CI race job runs the full suite on `main`, tags, and manual runs. Pull
requests and feature-branch pushes use `race-impacted.sh`, which maps changed
Go files to packages and expands the selection through reverse imports. Changes
to `go.mod`, `go.sum`, `vendor/`, or `third_party/` conservatively fall back to
the full suite; documentation-only changes skip race tests.

`runtime-topology-check` keeps the global runtime map tied to the code. Static
mode verifies that every tracked runtime file maps to an `RT-*` node, registered
code anchors still exist, and every Mermaid view has SVG, PNG and Excalidraw
artifacts. `--base <git-ref>` adds change-impact validation: runtime changes need
the PR/commit declarations `Topology impact`, `Blast radius` and `Topology
reason`; a changed Mermaid source must include all three regenerated artifacts.
Add `--working-tree` before committing so the comparison also includes staged,
unstaged and untracked files. The CI pull-request job runs both static and Git
range modes.

`offline-acceptance.sh` is the single entry point for the offline subset. It
verifies every script parses, that every `--help` still works, that scripts which
chain into siblings reference files that exist, that the release gate refuses
cleanly when its prerequisites are absent, and that no script has regressed to a
hardcoded home directory. Then it runs the scenarios that need neither a key nor a
companion checkout.

Project-owned build and offline-acceptance entrypoints automatically run
`go-cache-maintenance.sh` after their work. The shared Go build cache is cleaned
when it exceeds 6 GB by default; override the limit with
`NIUMA_GO_CACHE_MAX_GB`, or set `NIUMA_GO_CACHE_MAINTENANCE=0` to disable the
automatic check for one invocation. This does not clear the module cache.

It also runs `python3 scripts/release_test.py` (real tar/zip packaging with a
fake compiler), `node scripts/test-release-assets.mjs` (release asset names,
checksums and notes), and
`node --test scripts/upstream-agent-lifecycle-capture.test.cjs`.
These checks require Python 3, a Node.js version supporting `node:test`, zip,
and GNU tar or bsdtar. On macOS the archive test injects a synthetic xattr to
check that no AppleDouble metadata is shipped. `build.sh` always uses
`-trimpath`; `release.sh` normalizes tar ownership and omits host metadata.

## The three classes

### Memory release acceptance

`memory-release-acceptance.py` needs Python 3, PyYAML, a built `niuma`
binary and the named provider in global settings. It calls a real model and
incurs provider usage. It copies only that provider into an isolated `0600`
settings file, uses fresh HOME/projects and records evidence under a new `0700`
persistent directory outside the checkout. It rejects `/tmp` and inherited
guidance. Neither real memories nor global settings are modified.

```bash
python3 -m unittest scripts/memory_release_acceptance_test.py
python3 scripts/memory-release-acceptance.py \
  --binary ./niuma \
  --out-dir <local-evidence>-memory-acceptance-run1 \
  --provider sensenova-glm-5.2
```

Choose a new durable output path suitable for your OS on every run. Raw evidence
and the copied provider credential stay private; never commit them. The offline
unit tests above use synthetic data and do not access settings or the network.
The gate verifies persisted frontmatter/index, cold recall, negative scopes,
readonly hashes and document/total-budget behavior. It reports the logical
pre-provider request context, usage, cache tokens, turns, tools and latency.

### 1. Offline — no API key, no companion checkout

Each builds its own deterministic TUI harness with a fake provider.

| Script | In CI |
|---|---|
| `tui-display-order-acceptance.sh` | yes |
| `tui-ten-turn-acceptance.sh` | yes |
| `tui-tool-progress-acceptance.sh` | yes |
| `tui-semantic-terminal-acceptance.sh` | no — needs macOS `screencapture` |
| `tui-recap-terminal-acceptance.sh` | no — needs macOS `screencapture` |
| `tui-visual-regression-sop.sh` | no — needs macOS `screencapture` |
| `tui-session-replay-acceptance.sh` | yes — gates on the in-repo fixtures under `internal/tui/testdata/session-replay` |
| `tui-render-budget-acceptance.sh` | no — currently fails its own `last=Bash` assertion (real defect, see `docs/todo.md`) |

`tui-session-replay-acceptance.sh` gates on the in-repo fixture transcripts, and
additionally replays the two most recent size-appropriate sessions from
`~/.niuma-legacy/projects` as an advisory pass: that pass is machine-dependent, so a
failure is recorded in `status.json` (`local_session_status`) and printed as a
warning without failing the script. Set `TUI_REPLAY_LOCAL_SESSIONS=0` to replay
the fixtures alone. If neither source yields a transcript the script reports
`status: skipped` and exits 0 instead of breaking the visual regression SOP.

Also offline: `build.sh`, `install.sh`, `release.sh`, `webui-dev.sh`,
`web-agent-start.sh`, `web-agent-restart.sh` are plain build/run helpers, not
acceptance gates.

`channel-worker-screen.sh` is the standard long-running Feishu/channel worker
launcher. It compiles a standalone binary, runs provider preflight, and keeps
one worker per Feishu account in an isolated `screen` session with an account
lock. Its default state directory is the durable
`~/.niuma/channel-workers/`; `stop` preserves each `0600` worker env so a
later `start` can recover after a computer reboot without re-entering secrets.
An existing `/tmp/niuma-channel-workers/<worker>.env` is migrated and
removed once when no durable config exists. See
[channel_worker_screen_operations.md](../docs/architecture/channel_worker_screen_operations.md).
Explicit supported environment values on a later start override and refresh
the persisted defaults, so configuration remains editable after persistence.

`web-agent-start.sh` enables the tenant image tools for local WebUI acceptance by
default, referencing `jiuan-responses-gpt-5.6sol` and `gpt-image-2` without copying
credentials into the command line. Override `NIUMA_WEB_AGENT_IMAGE_PROVIDER`
or provide a complete `NIUMA_WEB_AGENT_IMAGE_SETTINGS` JSON value when using
another configured image provider.

`image-worker-screen.sh` runs the durable image queue independently from the
channel process. Each instance is scoped to exactly one tenant and can be
started, inspected, restarted, or stopped with:

```bash
export NIUMA_IMAGE_WORKER_NAME=tenant-7-a
export NIUMA_IMAGE_WORKER_TENANT_ID=7
export NIUMA_MYSQL_DSN='user:password@tcp(127.0.0.1:3306)/golang_cc'
export NIUMA_IMAGE_WORKER_SETTINGS_FILE="$HOME/.niuma/settings.json"
scripts/image-worker-screen.sh start
scripts/image-worker-screen.sh status
scripts/image-worker-screen.sh restart
scripts/image-worker-screen.sh stop
```

The settings file must enable `imageGeneration` and contains the stable
provider configuration used after restarts. The launcher builds a dedicated
binary, writes process environment to a `0600` state file, and waits for the
worker's post-recovery readiness file. Provider credentials and the MySQL DSN
remain in configuration/environment files and are never placed in the screen
process arguments. Multiple names may serve the same tenant; row leases fence
claims and terminal writes.

`build.sh` is the only place Build Identity linker values are injected. `install.sh`
(build from source, drop the binary in `$PREFIX/bin`) and `release.sh`
(cross-compile every target, archive, checksum) both shell out to it rather than
repeating the flag, so no install path can end up reporting `--version` as `dev`.
See [安装](../README.md#安装) for what each one is for.

All of these resolve the Go toolchain from `PATH` (override with `GO_BIN`). They
used to hardcode `/usr/local/go/bin/go`, which on a machine whose system Go is
older than `go.mod` requires made a working script look broken.

### 2. Needs a configured model provider

Live model checks use a provider explicitly configured in
`~/.niuma/settings.json`. They can incur usage and are opt-in. Older
comparison harnesses may require migration before use; they are not instructions
to import another product's credentials.

```
agent-capability-loop-acceptance.sh              agent-capability-loop-compact-acceptance.sh
agent-capability-loop-compact-facts-acceptance.sh agent-capability-loop-resume-acceptance.sh
agent-detached-running-resume-acceptance.sh      agent-empty-output-resume-acceptance.sh
agent-long-output-resume-acceptance.sh           agent-message-resume-acceptance.sh
agent-message-terminal-acceptance.sh             closure-gate-acceptance.sh
continuation-intent-acceptance.sh                failed-agent-resume-acceptance.sh
final-diagnostic-prompt-acceptance.sh            go-agent-lifecycle-capture.sh
goal-capability-follow-up-acceptance.sh          skill-compact-prompt-acceptance.sh
subagent-multiagent-acceptance.sh                task-capability-loop-acceptance.sh
task-capability-loop-resolution-acceptance.sh    task-partial-evidence-acceptance.sh
web-agent-scroll-smoke.sh
```

Many prompt-dump scripts (`code-mode-prompt-acceptance.sh`,
`prompt-acceptance-matrix.sh`, `skills-prompt-acceptance.sh`, …) also reach a
model on a normal run, but accept `--verify-only` to re-check dumps a previous run
produced. `--verify-only` needs no key.

The `tenant-*` and `webui-smoke.sh` scripts need a running server and, for the
MySQL ones, a database — no key, but not offline either.

### 3. Needs a companion checkout

Two sibling repositories, neither vendored here:

| Repo | What it is | Override |
|---|---|---|
| `claude_code_src_2026` | original Claude Code source, the A/B baseline | `NIUMA_UPSTREAM_DIR` |
| `agent-proving-ground` | agent test harness whose `reports/` the release gate consumes | `NIUMA_APG_DIR` |

By default both are looked up as siblings of this repository's main checkout
(resolved via `git rev-parse --git-common-dir`, so it works from a worktree too).
`NIUMA_COMPANION_ROOT` moves both at once. Niuma `NIUMA_*` and
`NIUMA_*` names are the canonical inputs. See `lib/external-repos.sh`.

```
agent-cancelled-side-by-side-compare.sh          agent-child-error-side-by-side-compare.sh
agent-long-output-side-by-side-compare.sh        agent-message-resume-side-by-side-compare.sh
capability-ab-eval-matrix.sh                     final-report-side-by-side-compare.sh
skill-compact-side-by-side-compare.sh            skill-load-side-by-side-compare.sh
upstream-agent-lifecycle-capture.sh              upstream-workspace-guidance-source-capture.sh
agent-capability-release-gate.sh                 agent-capability-full-release-acceptance.sh
```

## The release gate's undocumented prerequisite

`agent-capability-full-release-acceptance.sh` consumes 11 JSON reports under
`agent-proving-ground/reports/`. **Those are APG outputs, not source.** A fresh
APG checkout does not have them; you have to run the APG suites first. The script
now reports every missing artifact at once and says so, instead of dying on the
first bare "no such file":

```
$ scripts/agent-capability-full-release-acceptance.sh
release gate preflight failed — prerequisites are not in place.

  - 11 agent-proving-ground report(s) not found:
      .../reports/p1/compare/niuma.json
      ...
    These are produced by running the APG suites, not by this repo.
```

Use `--verify-only` with explicit artifact paths to re-check existing outputs
without any model calls.

## Conventions

- `/tmp` output paths are the default, not a requirement — every script takes
  `--out-dir` / `--work-dir` / `--dump-path`.
- Model names are inlined as defaults (e.g. `FINAL_REPORT_MODEL="gpt-5.5"`) and
  are all overridable by flag. They are not kept in sync with `config.KnownModels`;
  pass `--model` when it matters.
