package capabilityloop

// Wire vocabulary for the capability_loop protocol.
//
// Three packages parse this protocol independently, because each needs a
// different output shape from the same payload:
//
//   - internal/capabilityloop (this package) -> HintData + ArtifactHints, used to
//     build the parent's runtime-status and follow-up gate lines.
//   - internal/toolresult                    -> a human-readable summary string
//     preserved when a large tool result is externalized.
//   - internal/compact/facts                 -> a Facts struct of categorized
//     slices, preserved across context compaction.
//
// AUDIT-P2-02 called these "three duplicated parser implementations". They never
// were duplicates — no two shared a byte-identical function body — they were
// three implementations that disagreed about the parse itself, so the same
// sub-agent payload could be read as different results depending on which path
// it took. TODO-092/093/094 resolved those disagreements; the record of what was
// decided and why:
//
//  1. Candidate extraction (was TODO-092). Now single-sourced in JSONCandidates
//     and shared by all three packages: tags match ASCII-case-insensitively,
//     EVERY tag pair becomes a candidate, content without the key short-circuits,
//     and the un-tagged fallback tries the whole content before the outermost
//     {...} slice. Case leniency and every-pair each strictly widen what parses;
//     the guard is behavior-neutral because every decoder here requires the key;
//     the two-step fallback exists because neither half alone covers both an
//     array root and JSON embedded in prose.
//  2. Placeholder filtering (was TODO-093 ①, extended by TODO-101, finished by
//     TODO-110). Single-sourced in IsPlaceholder for **every** consumer: this
//     package, compact, internal/goal, internal/agentruntime and internal/tui.
//     No private copy of this set remains anywhere. The machine-injected
//     "completed" default next_action is filtered on all of them. The
//     failure-path defaults are deliberately NOT filtered: they tell the parent
//     it must handle a failure, and the follow-up gate is tested to surface them.
//     The two copies TODO-101 folded in were private sets that omitted the
//     completed default, so that one string was real signal to them and noise to
//     everyone else. In internal/goal that made the goal driver publish the
//     boilerplate as must_handle_next_action while the query driver suppressed
//     it — one sub-agent output, two different obligations, decided by which
//     driver happened to run it. internal/goal is pinned by a cross-driver test
//     that compares the two drivers against each other rather than against a
//     fixed expectation. internal/tui's copy (TODO-110) was different in kind:
//     it agreed with this set byte-for-byte, so folding it in changed nothing
//     that is displayed. It was removed because a copy that merely happens to
//     agree today is the same setup the other two divergences grew out of.
//  3. Follow-up gate labels (was TODO-093 ②, unblocked by TODO-102). The names
//     compact accepts beyond the canonical ones are not sub-agent alias
//     spellings — they are the labels the follow-up gate line emits, which
//     compaction reads back. FollowUpField* below single-sources them for
//     FollowUpLine, compact and internal/goal, so that emitter and that reader
//     cannot drift. Only compact maps them, because only compact parses that
//     surface. internal/goal had to spell them as literals until this package
//     stopped importing internal/storage/mysql (see StoredTask), which imports
//     internal/goal and so closed an import cycle on the reference.
//  4. Field coverage (was TODO-094). The preserved summary and its reader now
//     both carry follow_up_id, resolved_follow_up and both supersedes_* keys.
//     Dropping them let an already-resolved follow-up return to pending once a
//     result was externalized.
//  5. Still divergent by design: truncation limits (toolresult 700/220/80,
//     compact 240, per-call-site here) and output shape. Each package renders for
//     a different consumer; only the parse had to agree.
const (
	// Key is the object key and tag name carrying the payload.
	Key = "capability_loop"
	// OpenTag and CloseTag delimit an inline payload embedded in tool output.
	OpenTag  = "<" + Key + ">"
	CloseTag = "</" + Key + ">"
	// LineMarker prefixes a payload flattened onto a single text line, which is
	// the form that survives compaction and transcript persistence.
	LineMarker = Key + ":"
)

// Canonical field names, as a sub-agent spells them inside the payload.
const (
	FieldEvidence              = "evidence"
	FieldAssumptions           = "assumptions"
	FieldUnknowns              = "unknowns"
	FieldVerification          = "verification"
	FieldRisks                 = "risks"
	FieldNextAction            = "next_action"
	FieldFollowUpID            = "follow_up_id"
	FieldResolvedFollowUp      = "resolved_follow_up"
	FieldSupersedesEvidenceID  = "supersedes_evidence_id"
	FieldSupersedesEvidenceIDs = "supersedes_evidence_ids"
)

// Follow-up gate labels. FollowUpLine (and internal/goal's equivalent) relabels
// the canonical fields when it renders the gate line, because on that surface the
// label states the parent's obligation rather than naming a payload field. That
// line is itself part of the transcript, so compaction reads it back: these
// constants are the contract between the two, single-sourced so a rename on the
// emitting side cannot silently stop the reader from recovering the fact.
const (
	FollowUpFieldNextAction   = "must_handle_next_action"
	FollowUpFieldVerification = "verification_required"
	FollowUpFieldUnknown      = "unknown_to_resolve_or_disclose"
	FollowUpFieldRisk         = "risk_to_account_for"
)

// Artifact pointer field names. These sit alongside the capability_loop object
// rather than inside it, and all three parsers agree on the set.
const (
	ArtifactSessionID      = "session_id"
	ArtifactTranscriptPath = "transcript_path"
	ArtifactOutputFile     = "output_file"
	ArtifactWorktreePath   = "worktree_path"
	ArtifactWorktreeBranch = "worktree_branch"
)
