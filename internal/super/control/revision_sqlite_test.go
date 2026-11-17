package control_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/konglong87/super-niuma/internal/super/control"
	"github.com/konglong87/super-niuma/internal/super/execution"
	"github.com/konglong87/super-niuma/internal/super/verification"
)

// A failed independent check revises only the writer, with the failed checks
// as guidance; the read-only analysis is neither rerun nor paid for again.
func TestSQLiteIntegrationVerificationRevisionTargetsWriterWithFindings(t *testing.T) {
	h := newSQLiteHarness(t)
	var mu sync.Mutex
	calls := map[string]int{}
	var findings [][]execution.VerificationFinding
	h.engine.Executor = sqliteExecutor(func(ctx context.Context, r execution.Request) (execution.Result, error) {
		mu.Lock()
		calls[r.JobID]++
		attempt := calls[r.JobID]
		if r.JobID == "write" {
			findings = append(findings, r.VerificationFailures)
		}
		mu.Unlock()
		if r.JobID != "write" {
			return sqliteWork(ctx, r)
		}
		if err := r.Budget.Reserve(ctx, r.AttemptID+".request", 20, 10); err != nil {
			return execution.Result{UsageKnown: true}, err
		}
		data := []byte("draft without the required phrase")
		if attempt > 1 {
			data = []byte("fixture accepted: revised")
		}
		if err := os.WriteFile(filepath.Join(r.Workspace, "result.txt"), data, 0600); err != nil {
			return execution.Result{}, err
		}
		sum := sha256.Sum256(data)
		result := execution.Result{Text: "written", UsageKnown: true, Usage: execution.Usage{InputTokens: 5, OutputTokens: 3}, Writes: []execution.WriteReceipt{{Path: "result.txt", SHA256: hex.EncodeToString(sum[:]), Bytes: int64(len(data)), AttemptID: r.AttemptID}}}
		return result, r.Budget.Settle(ctx, r.AttemptID+".request", result.Usage, true)
	})
	r := h.start(t, "revision", h.grant(sqliteNode("research", nil, nil, nil), sqliteNode("write", []string{"research"}, nil, []string{"result.txt"})))
	for i := 0; i < 8; i++ {
		h.step(t, "worker")
	}
	got := h.get(t, r.ID)
	sqliteAssertReceipt(t, h, got)
	if calls["research"] != 1 || calls["write"] != 2 {
		t.Fatalf("revision reran the wrong jobs: %v", calls)
	}
	if len(findings) != 2 || len(findings[0]) != 0 || len(findings[1]) != 1 || findings[1][0].Path != "result.txt" || findings[1][0].Kind != verification.KindTextContains || findings[1][0].Code != verification.CodeTextMissing {
		t.Fatalf("revision guidance=%+v", findings)
	}
}

// With no artifact writer nothing can repair a failed check, so the root fails
// immediately instead of burning the budget on identical read-only reruns.
func TestSQLiteIntegrationVerificationWithoutWriterFailsWithoutRerun(t *testing.T) {
	h := newSQLiteHarness(t)
	var calls int
	var mu sync.Mutex
	h.engine.Executor = sqliteExecutor(func(ctx context.Context, r execution.Request) (execution.Result, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return sqliteWork(ctx, r)
	})
	r := h.start(t, "no-writer", h.grant(sqliteNode("research", nil, nil, nil)))
	for i := 0; i < 4; i++ {
		h.step(t, "worker")
	}
	got := h.get(t, r.ID)
	if got.Status != control.Failed || got.SafeCode != "verification_failed" || calls != 1 {
		t.Fatalf("status=%s code=%s calls=%d", got.Status, got.SafeCode, calls)
	}
}
