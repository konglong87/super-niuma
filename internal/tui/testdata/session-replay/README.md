# Session replay fixtures

Hand-written `~/.go-claude/projects/*/<session>.jsonl` transcripts used by
`scripts/tui-session-replay-acceptance.sh` and
`TestModelSessionReplayAcceptanceExport`.

They exist so the replay acceptance step (and therefore
`scripts/tui-visual-regression-sop.sh`) does not depend on which sessions happen
to live in `~/.go-claude/projects` on the current machine. Each fixture must keep
covering the ordering the replay asserts:

- an assistant message before the first tool call,
- a tool call plus its result,
- another assistant message after the tool,
- a final assistant message whose last line survives into the transcript tail.

Entry types the replay ignores (`checkpoint`, `prompt_context`, `usage`,
`permission`, `recap_summary`) are kept in the fixtures on purpose: real session
logs interleave them, and the replay must skip them.
