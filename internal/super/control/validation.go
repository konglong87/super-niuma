package control

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/konglong87/super-niuma/internal/super/execution"
	"github.com/konglong87/super-niuma/internal/super/verification"
)

func Digest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func ValidID(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.' || c == ':') {
			return false
		}
	}
	return true
}
func ValidPath(s string) bool {
	if s == "" || s == "." || len(s) > 1024 || strings.ContainsAny(s, "\\\x00\n\r") || path.IsAbs(s) || path.Clean(s) != s {
		return false
	}
	for _, p := range strings.Split(s, "/") {
		if p == ".." || strings.HasPrefix(p, ".") || p == "node_modules" || p == "credentials" {
			return false
		}
	}
	return true
}
func ValidateGrant(g Grant) error {
	if !ValidID(g.ProfileID) || !ValidID(g.Revision) || !filepath.IsAbs(g.Workspace) || filepath.Clean(g.Workspace) != g.Workspace || strings.ContainsAny(g.Workspace, "\x00\n\r") || g.MaxTokens < 1 || g.MaxTokens > MaxBudgetTokens || g.TimeoutMS < 1000 || g.TimeoutMS > MaxTimeoutMS || g.ExpiresAtMS <= 0 || len(g.Nodes) == 0 || len(g.Nodes) > MaxNodes || len(g.Targets) == 0 || len(g.Targets) > MaxTargets || g.Model == "" || len(g.SystemPrompt) > 8192 {
		return ErrInvalid
	}
	if err := verification.ValidatePolicy(g.Verification); err != nil {
		return ErrInvalid
	}
	nodes := map[string]Node{}
	for _, n := range g.Nodes {
		if !ValidID(n.ID) || strings.HasPrefix(n.ID, "super.") || len(n.Instruction) == 0 || len(n.Instruction) > 8192 || !utf8.ValidString(n.Instruction) || n.MaxTurns < 1 || n.MaxTurns > execution.MaxTurns || n.MaxOutputTokens < 1 || n.MaxOutputTokens > execution.MaxOutputTokens || len(n.ReadPaths) > 64 || len(n.WritePaths) > 64 || len(n.DependsOn) > MaxNodes {
			return ErrInvalid
		}
		if _, ok := nodes[n.ID]; ok {
			return ErrInvalid
		}
		nodes[n.ID] = n
		for _, p := range append(append([]string{}, n.ReadPaths...), n.WritePaths...) {
			if !ValidPath(p) {
				return ErrInvalid
			}
		}
	}
	visiting, done := map[string]bool{}, map[string]bool{}
	var visit func(string) bool
	visit = func(id string) bool {
		if done[id] {
			return true
		}
		if visiting[id] {
			return false
		}
		n, ok := nodes[id]
		if !ok {
			return false
		}
		visiting[id] = true
		deps := map[string]bool{}
		for _, d := range n.DependsOn {
			if deps[d] || !visit(d) {
				return false
			}
			deps[d] = true
		}
		if n.ParentID != "" {
			if n.ParentID == id {
				return false
			}
			if _, ok := nodes[n.ParentID]; !ok {
				return false
			}
		}
		visiting[id] = false
		done[id] = true
		return true
	}
	for id := range nodes {
		if !visit(id) {
			return ErrInvalid
		}
	}
	for id, n := range nodes {
		seenParents := map[string]bool{id: true}
		p := n.ParentID
		depth := 0
		for p != "" {
			if seenParents[p] || depth >= 8 {
				return ErrInvalid
			}
			seenParents[p] = true
			parent, ok := nodes[p]
			if !ok {
				return ErrInvalid
			}
			p = parent.ParentID
			depth++
		}
		if n.ParentID != "" {
			parent := nodes[n.ParentID]
			hasParent := false
			for _, d := range n.DependsOn {
				if d == n.ParentID {
					hasParent = true
				}
			}
			if !hasParent {
				return ErrInvalid
			}
			for _, p := range n.ReadPaths {
				if !pathAllowed(p, parent.ReadPaths) {
					return ErrInvalid
				}
			}
			for _, p := range n.WritePaths {
				if !pathAllowed(p, parent.WritePaths) {
					return ErrInvalid
				}
			}
		}
	}

	seen := map[string]bool{}
	for _, t := range g.Targets {
		if !ValidID(t.ID) || len(t.ID) > 96 || seen[t.ID] {
			return ErrInvalid
		}
		seen[t.ID] = true
		switch t.Provider {
		case ProviderDesktop:
			if t.Account != "" || t.Chat != "" || t.Recipient != "" || t.Thread != "" {
				return ErrInvalid
			}
		case ProviderFeishu, ProviderWebhook:
			if !ValidID(t.Account) || t.Chat == "" || len(t.Chat) > 256 || len(t.Thread) > 256 || len(t.Recipient) > 256 || len(t.ReplyTo) > 256 {
				return ErrInvalid
			}
		default:
			return ErrInvalid
		}
	}
	return nil
}
func ValidateRun(r Run) error {
	if !ValidID(r.ID) || !ValidID(r.Scope) || !ValidID(r.Owner) || !ValidID(r.RequestKey) || len(r.Objective) == 0 || len(r.Objective) > MaxObjectiveBytes || !utf8.ValidString(r.Objective) || r.Version == 0 || r.GrantHash != Digest(r.Grant) || len(r.Jobs) != len(r.Grant.Nodes) || len(r.Reservations) > MaxReservations || len(r.Notices) > MaxTargets {
		return ErrInvalid
	}
	if err := ValidateGrant(r.Grant); err != nil {
		return err
	}
	switch r.Status {
	case Queued, Running, Verifying, AwaitingApproval, DeliveryPending, Delivered, Cancelling, Cancelled, Failed, NeedsAttention:
	default:
		return ErrInvalid
	}
	for i, j := range r.Jobs {
		if Digest(j.Spec) != Digest(r.Grant.Nodes[i]) || j.Attempts < 0 || j.Attempts > MaxAttempts || len(j.Result.Text) > execution.MaxResultBytes {
			return ErrInvalid
		}
		switch j.Status {
		case JobQueued, JobRunning, JobSucceeded, JobFailed, JobCancelled, JobUnknown:
		default:
			return ErrInvalid
		}
	}
	ids := map[string]bool{}
	for _, b := range r.Reservations {
		if !ValidID(b.ID) || ids[b.ID] || b.Input < 0 || b.Output < 0 || b.Actual.InputTokens < 0 || b.Actual.OutputTokens < 0 {
			return ErrInvalid
		}
		ids[b.ID] = true
	}
	data, err := json.Marshal(r)
	if err != nil || len(data) > MaxRunBytes {
		return ErrCapacity
	}
	return nil
}
func SameIntent(a, b Run) bool {
	return a.Scope == b.Scope && a.Owner == b.Owner && a.RequestKey == b.RequestKey && a.IntakeID == b.IntakeID && a.Objective == b.Objective && a.GrantHash == b.GrantHash
}
func CanRead(p Principal, r Run) bool { return p.Scope == r.Scope && p.Owner == r.Owner }
func IsTerminal(s Status) bool {
	return s == Delivered || s == Cancelled || s == Failed || s == NeedsAttention
}
func ResourcePath(g Grant) string { return filepath.Clean(g.Workspace) }
func PathsOverlap(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	return a == b || strings.HasPrefix(a, b+string(filepath.Separator)) || strings.HasPrefix(b, a+string(filepath.Separator))
}
func ReservedTokens(r Run) int {
	n := 0
	for _, b := range r.Reservations {
		if b.Settled {
			n += b.Actual.InputTokens + b.Actual.OutputTokens
		} else {
			n += b.Input + b.Output
		}
	}
	return n
}

func pathAllowed(name string, roots []string) bool {
	for _, r := range roots {
		if name == r || strings.HasPrefix(name, r+"/") {
			return true
		}
	}
	return false
}
