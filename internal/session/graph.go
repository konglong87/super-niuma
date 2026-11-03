package session

import (
	"strings"

	"github.com/konglong87/super-niuma/internal/product"
)

// SchemaV2 tags every entry of a v2 message-graph transcript. The presence of
// this schema prefix on any line is what distinguishes a v2 transcript (a tree of
// messages linked by parent_id, with an append-only branch_head pointer) from a
// v1 linear log.
const SchemaV2 = product.TranscriptSchemaV2

// EntryTypeSessionMeta is the mandatory first line of a v2 transcript; it is a
// header, not a conversation node, so it never joins the parent_id chain.
const EntryTypeSessionMeta = "session_meta"

// EntryTypeSessionEvent stores a canonical cross-surface runtime event. The
// event payload stays in Metadata so the transcript remains compatible with
// the existing JSONL envelope while readers can project it into their native
// transport DTOs.
const EntryTypeSessionEvent = "session_event"

// EntryTypeBranchHead records a move of the active leaf (the tip of the current
// conversation). It is append-only: rewind/redo/new-turn each append one, and the
// active leaf is derived by replaying them in file order. A branch_head is a
// pointer event, not a conversation node, so it never joins the parent_id chain.
const EntryTypeBranchHead = "branch_head"

// Branch-head reasons. They are advisory (for UI/inspect); active-leaf resolution
// does not depend on the reason string.
const (
	BranchHeadReasonNewTurn = "new_turn"
	BranchHeadReasonRewind  = "rewind"
	BranchHeadReasonRedo    = "redo"
)

// IsV2Entries reports whether a loaded transcript uses the v2 message-graph
// schema. A single v2-tagged line is enough: v2 files tag every entry, and mixed
// files are rejected earlier by schema detection.
func IsV2Entries(entries []Entry) bool {
	for i := range entries {
		if strings.HasPrefix(entries[i].Schema, SchemaV2) {
			return true
		}
	}
	return false
}

// ActiveLeaf returns the id of the current active leaf by replaying the transcript
// in file order. Physical order is NOT conversation order: a chain entry advances
// the tip only when it extends the current tip (parent_id == tip), so abandoned
// branch nodes are ignored; a branch_head redirects the tip to its leaf_id. With
// no branch_head the tip simply follows the last chain entry, which reproduces v1
// linear behavior. Returns "" for an empty or non-graph transcript.
func ActiveLeaf(entries []Entry) string {
	tip := ""
	for i := range entries {
		entry := entries[i]
		switch entry.Type {
		case EntryTypeBranchHead:
			if entry.LeafID != "" {
				tip = entry.LeafID
			}
		case EntryTypeSessionMeta, EntryTypeSessionEvent, EntryTypeRuntimeSpan:
			// Side-band metadata, not a conversation node.
		default:
			if entry.ID == "" {
				continue
			}
			if entry.ParentID == tip {
				tip = entry.ID
			}
		}
	}
	return tip
}

// CurrentChain returns the entries on the current conversation chain in root→leaf
// order. Every consumer that treats a transcript as a conversation (resume,
// compact, inspect, rewind, file history) MUST derive its view from this rather
// than from raw file order, because in a v2 graph the file also holds abandoned
// branches. For a v1 (linear) transcript the entries are returned unchanged.
func CurrentChain(entries []Entry) []Entry {
	if !IsV2Entries(entries) {
		return entries
	}
	return ChainToLeaf(entries, ActiveLeaf(entries))
}

// LoadConversation loads a transcript and returns its current conversation view:
// the leaf-walk current chain for a v2 message graph (abandoned branches
// excluded), or all entries unchanged for a v1 linear transcript.
//
// Use this from consumers that treat a transcript as "the conversation" —
// compact, recap, export, rewind-candidate listing — so a non-destructive rewind
// never folds an abandoned branch into their output. Consumers that need the
// whole graph (rewind file restore, GC reachability, branch listing/compare,
// fork, raw show/inspect/search) must use Load instead.
func LoadConversation(path string) ([]Entry, error) {
	entries, err := Load(path)
	if err != nil {
		return nil, err
	}
	return CurrentChain(entries), nil
}

// LatestConversationEntryID returns the id of the newest entry that belongs to
// the conversation view. Side-band metadata such as usage, runtime spans, and
// recaps do not advance this head.
func LatestConversationEntryID(entries []Entry) string {
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		switch entry.Type {
		case "message", "tool_call", "tool_result", "compact_summary":
			if strings.TrimSpace(entry.ID) != "" {
				return entry.ID
			}
		}
	}
	return ""
}

// ChainToLeaf returns the chain of entries from the root down to leaf (inclusive)
// in root→leaf order, following parent_id. branch_head and session_meta lines are
// not chain nodes and are excluded. An unknown or empty leaf yields an empty chain.
func ChainToLeaf(entries []Entry, leaf string) []Entry {
	byID := make(map[string]Entry, len(entries))
	for i := range entries {
		entry := entries[i]
		if entry.ID == "" || isGraphMetadataEntry(entry.Type) {
			continue
		}
		byID[entry.ID] = entry
	}
	var reversed []Entry
	seen := make(map[string]bool)
	for id := strings.TrimSpace(leaf); id != ""; {
		entry, ok := byID[id]
		if !ok || seen[id] {
			break
		}
		seen[id] = true
		reversed = append(reversed, entry)
		id = strings.TrimSpace(entry.ParentID)
	}
	out := make([]Entry, len(reversed))
	for i, entry := range reversed {
		out[len(reversed)-1-i] = entry
	}
	return out
}

// commonPrefixLen returns the number of leading entries two root→leaf chains
// share by id — i.e. the length of their shared history up to (and including)
// their fork point.
func commonPrefixLen(a, b []Entry) int {
	n := 0
	for n < len(a) && n < len(b) && a[n].ID == b[n].ID && a[n].ID != "" {
		n++
	}
	return n
}

// Leaves returns the id of every leaf (a chain node with no children on the
// active-or-any branch) in the transcript, i.e. every branch tip. The active leaf
// is included. Order is by first appearance in the file. session_meta/branch_head
// are excluded. Used by branch listing/compare (phase E).
func Leaves(entries []Entry) []string {
	var order []string
	hasChild := make(map[string]bool)
	seen := make(map[string]bool)
	for i := range entries {
		entry := entries[i]
		if entry.ID == "" || isGraphMetadataEntry(entry.Type) {
			continue
		}
		if !seen[entry.ID] {
			seen[entry.ID] = true
			order = append(order, entry.ID)
		}
		if p := strings.TrimSpace(entry.ParentID); p != "" {
			hasChild[p] = true
		}
	}
	leaves := make([]string, 0)
	for _, id := range order {
		if !hasChild[id] {
			leaves = append(leaves, id)
		}
	}
	return leaves
}

func isGraphMetadataEntry(entryType string) bool {
	switch entryType {
	case EntryTypeBranchHead, EntryTypeSessionMeta, EntryTypeSessionEvent, EntryTypeRuntimeSpan:
		return true
	default:
		return false
	}
}
