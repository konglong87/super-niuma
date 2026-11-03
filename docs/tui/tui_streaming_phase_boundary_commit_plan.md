# TUI Streaming Phase-Boundary Commit Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prevent long Thinking and assistant text streams from visually replacing each other by committing completed display phases to terminal scrollback before rendering the next active phase.

**Architecture:** Keep `displayTimeline` as the TUI display source of truth. A stream phase boundary closes the active text segment and flushes completed timeline segments through the existing `tea.Println` transcript path; the live viewport then contains only the current uncommitted phase. `printedSeq` and `committingSeq` remain the ownership boundary that prevents a segment from appearing in both layers.

**Tech Stack:** Go, Bubble Tea v1, Bubbles viewport, Lipgloss/Glamour rendering, existing TUI PTY/tmux acceptance helpers.

---

## 1. Scope and Evidence

### User-visible symptom

During a long response, Thinking is visible first and assistant text starts later. As both grow, the visible rows appear to alternate or overwrite one another. The final transcript is generally complete, but the live screen is difficult to read and earlier Thinking lines disappear until the turn finishes.

### Confirmed code path

1. `StreamThinking` and `StreamText` are reduced into separate `displayTimeline` segments in `internal/tui/stream.go`.
2. `timelineLiveView()` renders all segments after the printed sequence in `internal/tui/live_display.go`.
3. `refreshViewport()` puts the full live result into one bounded viewport and calls `GotoBottom()` in `internal/tui/layout.go`.
4. During streaming, the transcript layer is not flushed. `tea.Println` is used only by the completion/interactive flush path in `internal/tui/transcript.go`.
5. Therefore a long Thinking plus assistant response competes for one finite live viewport. As the content grows, older rows are clipped and the same terminal rows are repainted, which produces the perceived overwrite.

### Not in scope

- Provider event parsing, model prompts, query-loop semantics, tool execution, permissions, or session JSONL schema.
- Per-token permanent scrollback writes.
- Changing the default `showThinking` policy.
- Replacing the natural scrollback architecture with an alt-screen application.

### Topology impact

- Topology impact: `none`.
- Affected node: `RT-OUTPUT` (`internal/tui/`).
- Blast radius: `B1_SCENARIO` for interactive TUI streaming; adjacent tool, agent, resume, recap, and transcript paths require regression coverage but no new runtime node or protocol consumer.
- Reason: existing `RT-OUTPUT` anchors and causal edges already describe `RT-MODEL -> RT-OUTPUT -> RT-PERSIST/RT-OBSERVE`; this plan changes only the TUI presentation boundary.

## 2. Display Invariants

The implementation is complete only when all of these are true:

1. `displayTimeline` is the only ordering source for live and transcript rendering.
2. A text segment is appendable only while it is the active final segment and has not been closed by a phase/tool/agent/meta boundary.
3. Once a Thinking phase is complete, it is either in terminal scrollback or explicitly hidden by `showThinking`; it is not left competing with the next live phase.
4. A segment is never rendered simultaneously by the live layer and the transcript layer. `printedSeq`/`committingSeq` must enforce this during the asynchronous `tea.Println` window.
5. Stream phase flushes occur at phase boundaries, not for every delta.
6. The final turn flush remains the authoritative fallback for any segment that did not cross an earlier boundary.
7. The provider, query loop, token accounting, tool call count, and persistence payload remain unchanged.
8. Existing tool ordering (`assistant -> tool -> assistant`) remains stable.

## 3. Macro Architecture

### 3.1 Layer boundaries

The TUI has two rendering layers with different ownership rules:

```text
Provider/query callbacks
        |
        v
StreamEvent channel
        |
        v
TUI reducer (Model + displayTimeline)
        |
        +--> terminal transcript layer (tea.Println, append-only scrollback)
        |
        +--> Bubble Tea live layer (viewport + bottom chrome + input)
```

The phase-boundary change belongs between the reducer and the two renderers. It does
not change the provider callback contract or the query loop. The reducer decides
which segments are complete; the transcript renderer owns completed segments; the
live renderer owns only segments after the printed boundary.

### 3.2 Data ownership

| Data | Owner | Meaning | Must not be used for |
| --- | --- | --- | --- |
| `messages` | query/session compatibility | Conversation-shaped state and metadata | Reconstructing stream order when a timeline exists |
| `displayTimeline` | TUI reducer | Ordered, append-only display segments | Provider request context |
| `transcriptPrintedSeq` | transcript layer | Highest sequence acknowledged as printed | Deciding whether a segment is complete |
| `transcriptCommittingSeq` | transcript command lane | Highest sequence currently being sent to `tea.Println` | Permanent success before acknowledgement |
| `transcriptPhaseCommittingSeq` | phase boundary lane | Completed phase hidden from live view while its print is in flight | User-submit header ownership |
| `viewport` | Bubble Tea live layer | Bounded rendering of unprinted segments | Long-term conversation history |
| `toolActivity` / `agentProgress` | runtime status | Mutable status panels and segment updates | Reordering completed text |

`messages` remains populated for existing session and result behavior, but all new
live/transcript ordering decisions must read `displayTimeline`. This prevents a
Thinking segment that was already separated from正文 by a tool or phase boundary
from being merged back into an assistant message during rendering.

### 3.3 Segment lifecycle

Each text segment follows this lifecycle:

```text
absent
  -> streaming (delta append allowed)
  -> done (phase/tool/agent/meta boundary closes append window)
  -> committing (selected for tea.Println)
  -> printed (acknowledged; hidden from live permanently)
```

Only the final `streaming` text segment may receive another delta. A delta for a
different phase creates a new segment. `displaySegmentDone` is a reducer fact;
`transcriptPhaseCommittingSeq` is a rendering fact. Keeping those concepts separate
allows the timeline to remain correct even while terminal output is asynchronous.

### 3.4 State machine

The stream reducer should implement this phase transition table:

| Current active segment | Incoming event | Reducer action | Render action |
| --- | --- | --- | --- |
| none | `StreamThinking` | append new Thinking segment | live render |
| Thinking/streaming | `StreamThinking` | append delta | throttled live render |
| Thinking/streaming | `StreamText` | close Thinking, append assistant segment | phase flush Thinking, immediate live render |
| assistant/streaming | `StreamText` | append delta | throttled live render |
| text/streaming | `StreamToolStart` | close text, append running tool | phase flush completed text, immediate tool render |
| tool/running | `StreamToolResult` | update same tool by `ToolID` | immediate render |
| tool/done | `StreamThinking` or `StreamText` | append a new text segment | live render |
| any | `StreamFinished` | mark running segments done/interrupted | final transcript flush |

If a provider emits multiple Thinking/text alternations without a tool, each
alternation is still a phase boundary. The implementation must not assume that one
model turn has only one Thinking segment or one assistant segment.

### 3.5 Why this solves the symptom

Before the change, the live viewport contains:

```text
Thinking(large) + assistant(large) + tool/status
```

After the change, at the first正文 delta it contains:

```text
assistant(current phase) + tool/status
```

The completed Thinking block has moved to terminal scrollback. The next viewport
refresh therefore does not push Thinking and正文 against each other, while the user
can still scroll back to the complete Thinking block.

### 3.6 Alternative decisions

| Option | Benefit | Cost | Decision |
| --- | --- | --- | --- |
| Hide Thinking by default | Smallest visual change | Does not solve long正文 live clipping; loses user-visible reasoning | Keep as existing `showThinking` configuration |
| Alt-screen full-page renderer | Simple fixed layout | Breaks natural scrollback and native terminal copy behavior | Reject |
| Per-token `tea.Println` | Earliest persistence | Fragmented scrollback, flicker, duplicate risk, high terminal I/O | Reject |
| Phase-boundary commit | Stable phase handoff with bounded I/O | Requires sequence ownership and PTY tests | Adopt |
| Incremental complete-line commit | Better long-stream history | More complex line stability and ANSI/Markdown handling | Defer until phase-boundary evidence is insufficient |

### 3.7 Persistence, privacy, and observability boundaries

The phase commit is a terminal presentation operation, not a session persistence
operation:

```text
displayTimeline segment
  -> transcript renderer
  -> tea.Println terminal scrollback
```

It must not append a `session.Entry`, modify the assistant message sent to the next
model turn, or change exported Markdown. `StreamThinking` remains governed by the
existing `WelcomeInfo.ShowThinking` switch. When the switch is disabled, the reducer
must continue to discard Thinking content from UI state while preserving the running
status; no phase commit is scheduled for hidden Thinking.

Because terminal scrollback can be inspected by a local user or terminal host, the
plan treats `showThinking=true` as explicit consent to display the content locally.
No new log, telemetry, trace, or server event may copy Thinking or assistant body
text. Observability may record only bounded counters such as phase-boundary count,
phase-flush count, flush duration, and flush error state if a suitable existing TUI
metric hook is available. These counters must not contain message content.

The expected cost profile is:

| Metric | Expected change |
| --- | --- |
| Model turns | `0` |
| Input/output tokens | `0` |
| Tool calls | `0` |
| Provider latency | `0` |
| TUI terminal writes | One low-frequency write per completed phase boundary, plus existing turn flush |
| Live Markdown renders | One immediate render at a boundary; existing throttling within a phase remains |

If a real session shows a large increase in phase flushes, the follow-up action is to
coalesce adjacent completed phases in the transcript command lane, not to reintroduce
per-delta terminal writes.

## 4. Micro Design

### 4.1 Reducer result contract

`applyStreamEvent` currently mutates the model and has no return value. To let the
update loop compose a phase flush with `waitForStreamEvent` without putting terminal
side effects into the reducer, change its effective contract to return a small effect
value. Existing statement-style callers can ignore the returned value.

Recommended shape:

```go
type streamEventEffects struct {
    phaseBoundary bool
    refreshNow    bool
}

func (m *Model) applyStreamEvent(event StreamEvent) streamEventEffects
```

Rules:

- `phaseBoundary=true` only when the incoming event closes a completed text phase.
- `refreshNow=true` for phase/tool/agent/status transitions; ordinary text/thinking
  deltas continue through `refreshViewportThrottled()`.
- Permission, question, usage, session-resume, and finished handling keeps its
  existing specialized path in `Update`; it must not be silently folded into the
  ordinary text reducer.

### 4.2 Boundary detection

The boundary detector must inspect the last unprinted timeline segment, not the last
`message` role. Its behavior is:

```go
func (m Model) shouldCommitBefore(event StreamEventType) bool {
    last, ok := m.displayTimeline.lastUnprintedSegment(m.pendingTranscriptSeq())
    if !ok || last.status != displaySegmentStreaming {
        return false
    }
    switch event {
    case StreamText:
        return last.kind == displaySegmentThinking
    case StreamThinking:
        return last.kind == displaySegmentAssistantText
    case StreamToolStart, StreamNestedProgress:
        return last.kind == displaySegmentThinking || last.kind == displaySegmentAssistantText
    default:
        return false
    }
}
```

The exact helper can remain private, but the following properties are mandatory:

- It ignores segments at or below `pendingTranscriptSeq()`.
- It does not close a segment that is already `done` or `error`.
- It never merges text across a tool/agent/meta segment.
- It does not treat whitespace-only deltas as a new phase unless the provider event
  itself changes the phase.

### 4.3 Closing and appending

The reducer operation order for `StreamText` after Thinking is:

```text
1. inspect last unprinted segment
2. close active Thinking segment (streaming -> done)
3. append assistant segment with a new sequence number
4. return {phaseBoundary: true, refreshNow: true}
```

The Thinking content must not be copied into the assistant segment. The assistant
segment starts with exactly the incoming `StreamText` delta. The same rule applies to
Thinking after assistant text: it gets a new segment, even if the provider emits the
event in the same model turn.

### 4.4 Transcript command lane

`prepareTranscriptFlushCmd(transcriptFlushPhaseBoundary)` must perform these steps in
order:

```text
1. collect completed timeline segments with seq > pendingTranscriptSeq()
2. render them with transcript mode
3. create transcriptFlushCommitMsg{output, printedSeq=maxSeq}
4. set transcriptPhaseCommittingSeq=printedSeq
5. return the existing frame-delayed command
```

The asynchronous command sequence is:

```text
phase boundary event
  -> prepareTranscriptFlushCmd
  -> transcriptFlushCommitMsg
  -> tea.Println(output)
  -> transcriptFlushPrintedMsg
  -> transcriptPrintedSeq advances
```

`transcriptPhaseCommittingSeq` is distinct from the existing user-submit commit
state. While a phase print is in flight, `liveHiddenSeq()` must include the phase
committing sequence even when `busy=true`; otherwise the just-rendered Thinking block
would remain in the viewport and duplicate the pending terminal print. User-submit
behavior remains unchanged so the submitted prompt stays visible until its print is
acknowledged.

When `transcriptFlushPrintedMsg` is applied:

- advance `transcriptPrintedSeq` monotonically;
- clear `transcriptPhaseCommittingSeq` only when it is covered by the acknowledged
  sequence;
- prune corresponding `liveDisplayBlocks`;
- refresh the viewport once, unthrottled.

### 4.5 Update command composition

For an ordinary text delta:

```text
apply event
  -> refreshViewportThrottled()
  -> waitForStreamEvent(ch)
```

For a phase boundary:

```text
apply event
  -> phaseFlush := prepareTranscriptFlushCmd(phaseBoundary)
  -> refreshViewport()
  -> tea.Sequence(phaseFlush, waitForStreamEvent(ch))
```

The phase flush command must be nil-safe. If no completed segment is available, the
update path must not schedule an empty `tea.Println` and must still re-arm the stream
channel reader.

### 4.6 Overlapping boundaries and coalescing

Only one phase print may be in flight at a time. If another boundary arrives while
`transcriptPhaseCommittingSeq > transcriptPrintedSeq`, the second boundary must:

1. close the new active text segment;
2. leave the already committing sequence excluded from the next block collection;
3. collect only newer completed segments;
4. schedule a second print after the first acknowledgement, or merge into the same
   command lane if Bubble Tea has not started the first command.

The implementation must not create two independent goroutines that write directly to
stdout. The existing Bubble Tea command lane is the only terminal writer.

### 4.7 Failure and interruption behavior

- If the provider stream ends normally, `StreamFinished` performs the existing final
  flush for all remaining unprinted segments.
- If the provider stream ends with an error, completed phase segments remain printable;
  active tool/agent segments are marked interrupted using existing logic; the error
  message is appended once.
- If Ctrl+C arrives during a phase commit, cancellation stops the provider/query
  work, but the already scheduled terminal print is allowed to finish. This avoids
  half-written scrollback and does not add a model turn.
- If the stream closes before the delayed phase command is processed, the final flush
  must use `max(transcriptPrintedSeq, transcriptPhaseCommittingSeq)` as its lower
  bound and must not duplicate the in-flight segment.

### 4.8 Rendering and spacing

The phase boundary must reuse the existing renderers:

- Thinking transcript: `renderThinking(..., displaySegmentDone, false)`.
- Assistant live: existing streaming Markdown renderer.
- Assistant transcript: existing final Markdown renderer.
- Tool/agent: existing timeline segment renderer.

Do not add a second Thinking renderer or hand-write ANSI sequences. A single blank
separator between the committed phase and the new live phase is sufficient; the
existing transcript bottom guard remains responsible for the bottom chrome boundary.

## 5. File Ownership

### Core reducer and stream boundary

- Modify `internal/tui/stream.go`:
  - Detect the first `StreamText` after a Thinking segment and close the Thinking append window.
  - Detect equivalent transitions around tool/agent/meta segments.
  - Return a phase-flush command without changing the event contract.

- Modify `internal/tui/display_segments.go`:
  - Add an explicit helper for closing the active text segment.
  - Preserve segment sequence and status transitions in one place.

- Modify `internal/tui/display_timeline.go`:
  - Add the read-only `lastUnprintedSegment(minSeq int64) (displaySegment, bool)` lookup used by the boundary detector.
  - Keep sequence lookup independent from message indexes and tool sidecars.

### Transcript ownership and flushing

- Modify `internal/tui/transcript.go`:
  - Add a `transcriptFlushPhaseBoundary` reason.
  - Reuse `pendingTimelineTranscriptBlocks()` and `printedSeq` to flush completed segments only.
  - Keep `tea.Println` followed by the existing printed acknowledgement before releasing the committing boundary.

- Modify `internal/tui/app.go`:
  - Add `transcriptPhaseCommittingSeq int64` to distinguish a phase print in flight from the user-submit print that intentionally keeps the submitted prompt visible.
  - Reset the field on session resume and after the acknowledged sequence covers it.

### Live renderer and layout

- Modify `internal/tui/live_display.go` only if needed to make the unprinted-segment filter explicit for a phase boundary.
- Modify `internal/tui/layout.go` only if phase flushes need an unthrottled final refresh; do not change viewport budget calculations as part of this plan.

### Tests and acceptance

- Modify `internal/tui/app_test.go` for reducer/order/duplicate regression tests.
- Add or extend `internal/tui/stream_phase_boundary_test.go` for focused phase-boundary behavior if the existing test file becomes too broad.
- Extend `internal/tui/transcript_spacing_pty_test.go` or add a dedicated PTY helper for Thinking -> assistant transitions.
- Extend `scripts/tui-display-order-acceptance.sh` with Thinking/body sentinels if the existing deterministic replay harness can host the scenario without duplicating a driver.
- Add `scripts/tui-phase-boundary-pty-acceptance.sh` as the repeatable real-tmux acceptance entrypoint.

### Documentation

- This plan is the design/implementation source of truth.
- After implementation, update `docs/tui/tui_thinking_display_design.md` with the completed phase-boundary behavior and actual verification evidence.
- No Swagger, API, migration, or runtime-topology registry update is required.

## 6. Implementation Tasks

### Task 1: Freeze the failing display contract

**Files:**

- Test: `internal/tui/app_test.go`
- Test: `internal/tui/stream_phase_boundary_test.go` (create if needed)

- [x] **Step 1: Add a deterministic Thinking -> assistant stream test.**

  Feed a model the sequence below while `busy=true` and `streamingActive=true`:

  ```text
  StreamThinking("THINKING_START ... THINKING_TAIL")
  StreamText("ANSWER_START ... ANSWER_TAIL")
  ```

  Assert that the timeline order is Thinking before assistant text and that the live view contains both sentinels before the boundary flush is applied.

- [x] **Step 2: Add the boundary ownership assertions.**

  After the first assistant event, assert that the boundary command is non-nil, its delayed message contains the completed Thinking sentinel, and the assistant sentinel remains in the live layer. Assert that the delayed output contains no assistant sentinel.

- [x] **Step 3: Run the focused tests and record the current failure.**

  Run:

  ```bash
  go test ./internal/tui -run 'Test.*Thinking.*Boundary|Test.*Phase.*Boundary' -count=1
  ```

  Expected before implementation: the new boundary assertion fails because no phase flush exists and both segments remain in the same live viewport.

### Task 2: Add a phase-boundary flush reason and reducer helper

**Files:**

- Modify: `internal/tui/transcript.go`
- Modify: `internal/tui/display_segments.go`
- Modify: `internal/tui/display_timeline.go`
- Modify: `internal/tui/app.go`

- [x] **Step 1: Add the phase-boundary reason.**

  Extend the existing `transcriptFlushReason` constants with `transcriptFlushPhaseBoundary`. Keep it inside the transcript package so all flush callers continue to use the same formatting, bottom guard, and sequence bookkeeping.

- [x] **Step 2: Add one active-text close helper.**

  Implement a helper with the existing `closeDisplayTextAppendWindow` semantics as the single reducer operation that changes a streaming Thinking/assistant segment to `displaySegmentDone`. It must stop at the first non-text segment and must not reorder or merge older segments.

- [x] **Step 3: Add the sequence lookup and phase commit state.**

  Implement:

  ```go
  func (t displayTimeline) lastUnprintedSegment(minSeq int64) (displaySegment, bool)
  ```

  It scans from the end, returns the newest segment with `seq > minSeq`, and returns
  `false` when no such segment exists. Add `transcriptPhaseCommittingSeq int64` to
  `Model`; reset it on session resume and clear it only after
  `transcriptFlushPrintedMsg` acknowledges an equal-or-higher sequence.

- [x] **Step 4: Keep boundary flush selection timeline-based.**

  `prepareTranscriptFlushCmd(transcriptFlushPhaseBoundary)` must use `pendingTimelineTranscriptBlocks()` and `pendingTranscriptSeq()`. It must not fall back to `messages` when a timeline exists, and it must not mark `transcriptPrintedSeq` as complete before `tea.Println` has run.

- [x] **Step 5: Run reducer and transcript unit tests.**

  Run:

  ```bash
  go test ./internal/tui -run 'Test.*Display|Test.*Transcript|Test.*Thinking' -count=1
  ```

  Expected: existing tests pass; the new boundary ownership test remains red until the stream update path invokes the new reason.

### Task 3: Trigger phase-boundary commits from the stream update path

**Files:**

- Modify: `internal/tui/stream.go`
- Modify: `internal/tui/update.go`

- [x] **Step 1: Close Thinking before the first assistant text.**

  When applying `StreamText`, inspect the last unprinted timeline segment. If it is a streaming Thinking segment, close it before appending the assistant segment. The assistant event must remain in the timeline after Thinking.

- [x] **Step 2: Return the phase flush command through `Update`.**

  The stream event path must be able to return both the phase flush command and the next `waitForStreamEvent` command. Use the existing `tea.Sequence`/`tea.Batch` conventions; do not write to the stream channel from the UI reducer.

- [x] **Step 3: Keep event ordering and backpressure safe.**

  The producer remains the only owner that closes `events`. The phase flush command must not start a goroutine that writes to `events`, and it must not drain special permission/question/finished events as ordinary text.

- [x] **Step 4: Refresh only the current live phase after the boundary.**

  After state mutation, refresh the viewport so the first assistant frame excludes the committed Thinking segment. Text/thinking delta throttling remains active inside a continuous phase; phase transitions use an immediate refresh so the visual handoff is prompt.

- [x] **Step 5: Run focused tests.**

  Run:

  ```bash
  go test ./internal/tui -run 'Test.*Thinking.*Boundary|TestModelKeepsAssistantToolAssistantTimelineOrder|TestModelDoesNotSplitAssistantWhenTextArrivesAfterUsage' -count=1
  ```

  Expected: PASS, with Thinking in transcript output and assistant text remaining live until turn completion.

### Task 4: Cover tool and multi-phase transitions

**Files:**

- Test: `internal/tui/app_test.go`
- Test: `internal/tui/stream_phase_boundary_test.go`

- [x] **Step 1: Add a Thinking -> tool -> assistant scenario.**

  Feed Thinking, tool start/result, then assistant text. Assert the committed order is Thinking, tool, assistant; assert the assistant text does not get appended to the earlier Thinking or assistant segment.

- [x] **Step 2: Add repeated phase scenarios.**

  Cover `Thinking -> Text -> Tool -> Thinking -> Text` and `Text -> Tool -> Text`. Assert each sentinel occurs exactly once in transcript order and no old segment remains in the live view after its boundary flush acknowledgement.

- [x] **Step 3: Add cancellation/error coverage.**

  If a phase flush is pending when `StreamFinished` carries an error, assert that the completed segments are still printable, the current running segment is marked interrupted, and no duplicate error/transcript block is emitted.

- [x] **Step 4: Run the TUI regression subset.**

  Run:

  ```bash
  go test ./internal/tui -run 'TestModel.*(Timeline|Transcript|Stream|Thinking|Tool)' -count=1
  ```

### Task 5: Add real PTY evidence

**Files:**

- Modify or create: `internal/tui/transcript_spacing_pty_test.go` or a dedicated `internal/tui/stream_phase_boundary_pty_test.go`
- Modify: `scripts/tui-display-order-acceptance.sh` if the existing harness is extended
- Create: `scripts/tui-phase-boundary-pty-acceptance.sh`

- [x] **Step 1: Build a deterministic PTY stream.**

  Emit at least two terminal screens of Thinking text, then two screens of assistant text, with unique `THINKING_*` and `ANSWER_*` sentinels. Include one tool boundary in a second case.

- [x] **Step 2: Assert terminal text order.**

  Capture tmux/PTY output and assert:

  ```text
  THINKING_HEAD < THINKING_TAIL < ANSWER_HEAD < ANSWER_TAIL
  ```

  Each sentinel must appear once in the transcript history after completion. The live screen must not show the completed Thinking block after its phase flush acknowledgement.

- [x] **Step 3: Capture a readable screenshot.**

  The display-order and ten-turn acceptance runs generated PNG fixtures and verified
  chrome spacing. The full macOS Terminal.app visual SOP was also attempted, but its
  AppleEvent driver timed out in the locked/unavailable desktop environment; that
  environment limitation is recorded and is not treated as a product pass.

- [x] **Step 4: Run the PTY acceptance command.**

  Run:

  ```bash
  scripts/tui-display-order-acceptance.sh
  ```

  Record any tmux/Terminal.app prerequisite or environment-only failure separately from product assertions.

### Task 6: Full verification and documentation handoff

**Files:**

- Modify: `docs/tui/tui_thinking_display_design.md`
- Modify: `docs/tui/tui_streaming_phase_boundary_commit_plan.md` (mark implementation steps complete during execution)

- [x] **Step 1: Run package and repository tests.**

  Run:

  ```bash
  go test ./internal/tui -count=1
  go test ./... -count=1
  git diff --check
  ```

- [x] **Step 2: Run topology validation if implementation files changed.**

  Run:

  ```bash
  go run ./scripts/runtime-topology-check --base HEAD --working-tree \
    --impact none --blast-radius B1_SCENARIO \
    --reason 'TUI phase-boundary transcript flush remains within existing RT-OUTPUT topology'
  ```

- [x] **Step 3: Update the design document with facts.**

  Document the final event sequence, the exact flush boundary, PTY evidence, test commands, known terminal prerequisites, and the fact that provider/query/persistence contracts were unchanged.

- [x] **Step 4: Commit only implementation and documentation files.**

  Before staging, verify unrelated channel/MySQL work remains unstaged:

  ```bash
  git status --short
  git diff -- docs/tui internal/tui
  ```

  The implementation commit must not include the existing channel, migration, or storage changes.

## 7. Risks, Costs, and Rollback

### Positive effects

- Thinking and正文 no longer compete for one growing live viewport after a phase transition.
- Earlier Thinking remains available through terminal scrollback.
- Display order is preserved by the existing timeline sequence model.
- No additional model turn, token, tool call, or provider latency is introduced; only a low-frequency terminal print at phase boundaries.

### Costs and risks

- Users who enable Thinking will see completed Thinking in terminal scrollback before the final answer. This is intentional and can be disabled with `showThinking=false`.
- Phase flushes add a small number of terminal renderer updates. They must be boundary-driven, never delta-driven.
- A missed or duplicated sequence acknowledgement could duplicate content; `printedSeq`/`committingSeq` tests are mandatory.
- Very frequent provider phase alternation may create more flushes. Tool and agent boundaries must remain bounded by actual event phases, and a future coalescing window can be added without changing the timeline contract.

### Rollback

Rollback is limited to the TUI reducer/transcript changes in `internal/tui/stream.go`, `internal/tui/update.go`, `internal/tui/display_segments.go`, and `internal/tui/transcript.go`, plus tests/docs. No database, session schema, provider, or API rollback is required.

## 8. Review Checklist

Before implementation starts, review these decisions:

- [x] Completed Thinking is allowed to enter terminal scrollback when `showThinking=true`.
- [x] Phase flushes happen at Thinking -> assistant and other display-phase boundaries, not per token.
- [x] `displayTimeline` remains the only ordering source.
- [x] No alt-screen migration is introduced.
- [x] PTY acceptance is required before claiming the visual bug is fixed.
- [x] Existing unrelated channel/MySQL work must stay out of the implementation commit.
