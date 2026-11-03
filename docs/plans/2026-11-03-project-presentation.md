# Project presentation delivery ledger

## Scope and starting state

- Request: improve the Chinese/English README and the public GitHub repository description/topics so newcomers can discover and quickly understand Super Niuma.
- Repository: konglong87/super-niuma, public, main. Starting HEAD: 9681de6; clean tree; `git pull --ff-only origin main` already current. No pre-existing user edits.
- No branch/worktree, runtime change, version bump, release publication or visibility change is in scope.

## Coverage and changes

| Surface | Gap | Action |
| --- | --- | --- |
| README.md / README.en.md | Old Niuma hero, scattered successor positioning, no newcomer-oriented Super journey | Unified Super identity, benefits, real screenshot, two entry points, first-order setup, architecture, acceptance boundaries and contribution paths |
| Desktop build guide | Nonexistent build script/output paths containing an old display-name substitution | Correct only the verified paths; do not change build logic |
| GitHub About | Short evolution note rather than a product explanation | Bilingual, benefit-led desktop agent positioning with explicit preview status |
| GitHub topics | Empty | Add relevant capability/stack/discovery topics; no unsupported integrations or unrelated keywords |
| Existing guides / evidence | Already cover detailed how-to, architecture and acceptance | Link rather than duplicate the implementation guide or invent success claims |

## Acceptance gates

- Validate every local README link, explicit navigation anchor, image, executable command path and current license/toolchain claim against repository files.
- Preserve honest boundaries: bounded file tools, deterministic supported channel commands, real-model HTTP 429 outcome, external-channel acceptance pending, no official installer release, Computer Use limitations.
- Run public documentation checks, legacy boundary/guard tests and whitespace checks. Build/native regression is not needed for documentation-only edits; existing screenshots remain original evidence.
- Read GitHub metadata back after mutation and inspect the live rendered project page. Confirm description/topics match and repository visibility/default branch are unchanged.
- Commit as konglong <konglong@com>, push origin/main, then verify a clean tree and HEAD == origin/main.

## Progress

- Drafts written; README and related build-command review in progress.
- Metadata update, rendered-page verification, commit and push pending.

## Metadata applied and read back

Description:

> 🐮 你的超级牛马｜开源桌面 Agent 交付工作台，统一接单、分工、追踪与独立验收。Desktop-first multi-agent orchestration with scoped execution, durable events and human approval. Development preview.

Topics (16): `ai-agents`, `multi-agent`, `agent-orchestration`, `workflow-automation`, `desktop-app`, `human-in-the-loop`, `event-driven`, `task-automation`, `golang`, `wails`, `react`, `typescript`, `sqlite`, `feishu`, `webhooks`, `llm`.

- GitHub API readback matches the intended description and topics. Visibility remains public, default branch remains main, homepage remains unchanged. No new release, domain, unsupported integration or promotional metric was invented.
- Local validation: 80 README links/assets/anchors, documented command paths and pinned toolchain references pass; CLI `--help` executes; 7 public-doc checks, 6 boundary-guard tests, legacy baseline guard, shell syntax and whitespace checks pass.
- Runtime code is unchanged. No native rebuild or paid model call is required for this documentation-only slice.
- Ready to commit/push the presentation slice; live page rendering is the remaining final check.

## Published-page review

- Presentation source slice `002a187` is committed and pushed. The public page shows the new Super title, Chinese/English navigation, screenshot, preview status, About description and all 16 topics.
- GitHub image endpoints return HTTP 200 with SVG/PNG content types; the real Desktop screenshot and all badges rendered in the built-in browser.
- Visual review found the first left-to-right architecture graph compressed its labels too far. Replaced both language diagrams with a compact top-to-bottom graph before final acceptance; no capability claims changed.
