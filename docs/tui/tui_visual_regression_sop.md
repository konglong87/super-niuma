# TUI Visual Regression SOP

This SOP is mandatory for changes that touch TUI display ordering, transcript flushing, bottom chrome, live streaming, user input rendering, tool progress, or sub-agent progress.

## Why This Exists

Two regressions have repeatedly traded places:

1. Completed assistant text can be covered by the bottom `Usage/Input/status` chrome.
2. Fixing that by permanently printing many blank lines into terminal scrollback makes each user message appear with a large blank gap above and below.

The durable rule is:

- Do not protect completed transcript output by writing bottom-chrome-height blank lines into scrollback.
- Keep transcript flush spacing compact.
- Prove bottom chrome isolation with real PTY plus readable Terminal screenshots.

## Required Command

Run:

```bash
scripts/tui-visual-regression-sop.sh
```

For release-level verification, also run full repository tests through the same SOP:

```bash
TUI_VISUAL_SOP_FULL=1 scripts/tui-visual-regression-sop.sh
```

The script writes all artifacts under `/tmp/gocc-tui-visual-regression-*` unless `OUT_DIR` is provided.

Keep `OUT_DIR` short. The Terminal steps type a command containing that path five
times into a fresh Terminal window, and a tty in canonical mode drops an input
line longer than 1024 bytes: the shell then never runs the command, no
`typescript.log` appears, and the step fails with `timed out waiting for
turn-01.done`. The default `/tmp` path produces a ~500 byte command; a deep path
(for example a per-session scratchpad directory) can push it past the limit.

## What The Script Covers

- Real macOS Terminal window screenshots using `screencapture`.
- Real PTY execution through `script`.
- 10 continuous, semantically linked turns.
- Turn 1: markdown table with a late `prompts/` row after usage.
- Turn 6: long text pressure.
- Turn 8: simple sub-agent lifecycle: started, text, tool call, completed.
- Turn 10: final bottom-chrome isolation check.
- Session replay against the in-repo fixture transcripts
  (`internal/tui/testdata/session-replay/`), plus the most recent recorded
  sessions under `~/.go-claude/projects` as an advisory extra pass.
- TUI unit tests and `git diff --check`.

## Pass Criteria

All of these must hold:

- Each assistant answer has exactly one `Go Claude` header unless a new assistant message truly starts.
- User messages are separated by compact spacing only, not a large scrollback gap.
- The final assistant line remains visible above `Usage/Input/status`.
- `Sub-agents` stays attached to the active assistant turn and does not drift to the bottom.
- Table rows and long text remain ordered.
- The final report includes readable screenshot paths.

The machine report is:

```text
<OUT_DIR>/visual-regression-report.json
```

Its `session_replay` field carries the replay step's own status. `ok` means the
fixture transcripts replayed in order; `skipped` means the checkout had no
fixture and the machine had no usable recorded session, which is reported but
does not fail the SOP. `local_session_status: failed` means the advisory pass
over `~/.go-claude/projects` failed — inspect
`<OUT_DIR>/session-replay/local-sessions/replay.log` before dismissing it.

The screenshots to inspect first are:

```text
<OUT_DIR>/semantic-terminal/subagent-turn-08-terminal.png
<OUT_DIR>/semantic-terminal/final-turn-10-terminal.png
```

## Failure Rules

Treat any of these as a failing regression:

- More than two blank lines are needed after a completed transcript tail.
- `tail-guard-report.json` reports `guard_lines != 1`.
- A user message appears isolated by a large vertical blank area.
- Bottom chrome visually overlaps or hides the last assistant line.
- Sub-agent progress causes a second `Go Claude` header inside the same assistant turn.
- The real Terminal screenshot is missing or unreadable.

Do not accept `Model.View()` output, ANSI logs, or synthetic PNGs alone for visible TUI regressions. They are useful diagnostics, not final acceptance.
