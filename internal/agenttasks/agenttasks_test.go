package agenttasks_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/sessioncontrol/handoff"
)

func TestSessionHandoffEventTypeIsStable(t *testing.T) {
	if agenttasks.EventSessionHandoff != "session_handoff" {
		t.Fatalf("EventSessionHandoff = %q", agenttasks.EventSessionHandoff)
	}
}

func TestDecodeSessionHandoffEventValidatesTypedBoundedPackage(t *testing.T) {
	payload := validSessionHandoffEvent(t, 42)
	decoded, err := agenttasks.DecodeSessionHandoffEvent(payload)
	if err != nil {
		t.Fatalf("DecodeSessionHandoffEvent() error = %v", err)
	}
	if decoded.TargetTaskID != 42 || decoded.PackageID == "" || decoded.PackageSHA256 == "" {
		t.Fatalf("decoded = %#v", decoded)
	}

	for name, invalid := range map[string]string{
		"malformed":          `{`,
		"transcript field":   strings.TrimSuffix(payload, "}") + `,"transcript":[{"role":"user","content":"secret"}]}`,
		"package transcript": strings.Replace(payload, `"objective":"continue"`, `"objective":"continue","messages":[{"content":"secret"}]`, 1),
		"forged package":     strings.Replace(payload, `"objective":"continue"`, `"objective":"tampered"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := agenttasks.DecodeSessionHandoffEvent(invalid); err == nil {
				t.Fatal("DecodeSessionHandoffEvent() error = nil")
			}
		})
	}
}

func TestSessionHandoffEventOptionalRefreshLineageIsStrictAndBackwardCompatible(t *testing.T) {
	legacy := validSessionHandoffEvent(t, 42)
	decodedLegacy, err := agenttasks.DecodeSessionHandoffEvent(legacy)
	if err != nil || decodedLegacy.RefreshOfPackageID != "" || decodedLegacy.RefreshOfEventID != 0 {
		t.Fatalf("legacy decode = %#v, err = %v", decodedLegacy, err)
	}
	decodedPackage, err := agenttasks.DecodeSessionHandoffEvent(legacy)
	if err != nil {
		t.Fatal(err)
	}
	refreshedPackage := refreshedSessionHandoffPackage(t)
	refreshed, err := agenttasks.EncodeSessionHandoffEventWithRefresh(43, refreshedPackage, decodedPackage.PackageID, 42)
	if err != nil {
		t.Fatalf("EncodeSessionHandoffEventWithRefresh() error = %v", err)
	}
	decodedRefresh, err := agenttasks.DecodeSessionHandoffEvent(refreshed)
	if err != nil || decodedRefresh.RefreshOfPackageID != decodedPackage.PackageID || decodedRefresh.RefreshOfEventID != 42 {
		t.Fatalf("refresh decode = %#v, err = %v", decodedRefresh, err)
	}
	if _, err := agenttasks.EncodeSessionHandoffEventWithRefresh(43, refreshedPackage, "handoff:wrong", 42); err == nil {
		t.Fatal("forged refresh package ID was accepted")
	}
	if _, err := agenttasks.EncodeSessionHandoffEventWithRefresh(43, refreshedPackage, decodedPackage.PackageID, 0); err == nil {
		t.Fatal("incomplete refresh lineage was accepted")
	}
}

func TestSessionHandoffEventCarriesValidatedOperationMetadata(t *testing.T) {
	legacyEnvelope := validSessionHandoffEvent(t, 42)
	legacyDecoded, err := agenttasks.DecodeSessionHandoffEvent(legacyEnvelope)
	if err != nil {
		t.Fatal(err)
	}
	packageJSON := string(legacyDecoded.PackageJSON)
	keyHash := strings.Repeat("a", 64)
	fingerprint := strings.Repeat("b", 64)
	operationID := sessionControlOperationID("attach", keyHash, fingerprint)
	metadata := fmt.Sprintf(`{"schema":"niuma.session-control-operation.v1","operation":"attach","operation_id":"%s","key_hash":"%s","fingerprint":"%s"}`, operationID, keyHash, fingerprint)
	payload, err := agenttasks.EncodeSessionHandoffEventWithOperationMetadata(42, packageJSON, "", 0, keyHash, metadata)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := agenttasks.DecodeSessionHandoffEvent(payload)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.OperationIdentity != keyHash || decoded.OperationMetadataJSON == "" {
		t.Fatalf("decoded = %#v", decoded)
	}
	if _, err := agenttasks.EncodeSessionHandoffEventWithOperationMetadata(42, packageJSON, "", 0, keyHash, strings.Replace(metadata, fingerprint, strings.Repeat("c", 64), 1)); err == nil {
		t.Fatal("tampered operation metadata was accepted")
	}
	legacy, err := agenttasks.EncodeSessionHandoffEventForOperation(42, packageJSON, "", 0, keyHash)
	if err != nil {
		t.Fatal(err)
	}
	decodedLegacy, err := agenttasks.DecodeSessionHandoffEvent(legacy)
	if err != nil || decodedLegacy.OperationMetadataJSON != "" {
		t.Fatalf("legacy decode = %#v, err = %v", decodedLegacy, err)
	}
}

func sessionControlOperationID(operation, keyHash, fingerprint string) string {
	payload, _ := json.Marshal(struct {
		Schema      string `json:"schema"`
		Operation   string `json:"operation"`
		KeyHash     string `json:"key_hash"`
		Fingerprint string `json:"fingerprint"`
	}{"niuma.session-control-operation.v1", operation, keyHash, fingerprint})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func TestSessionHandoffEventCarriesStrictOperationIdentity(t *testing.T) {
	legacy := validSessionHandoffEvent(t, 42)
	decoded, err := agenttasks.DecodeSessionHandoffEvent(legacy)
	if err != nil {
		t.Fatal(err)
	}
	identity := strings.Repeat("a", 64)
	payload, err := agenttasks.EncodeSessionHandoffEventForOperation(42, string(decoded.PackageJSON), "", 0, identity)
	if err != nil {
		t.Fatalf("EncodeSessionHandoffEventForOperation() error = %v", err)
	}
	got, err := agenttasks.DecodeSessionHandoffEvent(payload)
	if err != nil || got.OperationIdentity != identity {
		t.Fatalf("decoded operation identity = %q, err=%v", got.OperationIdentity, err)
	}
	if _, err := agenttasks.EncodeSessionHandoffEventForOperation(42, string(decoded.PackageJSON), "", 0, "raw-key"); err == nil {
		t.Fatal("non-hash operation identity was accepted")
	}
}

func TestSessionHandoffEventRejectsUnapprovedPackageBudget(t *testing.T) {
	pkg, err := handoff.BuildPackage(handoff.PackageInput{Source: handoff.Source{Ref: "tenant:source", Cursor: "message:1"}, Target: handoff.Target{Ref: "tenant:target"}, Objective: "continue", Budget: handoff.Budget{EstimatedTokens: 2049, LimitTokens: handoff.DefaultPackageTokenLimit}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(pkg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agenttasks.EncodeSessionHandoffEvent(42, string(encoded)); err == nil {
		t.Fatal("over-budget package was accepted by typed event codec")
	}
}

func TestSessionHandoffEventAcceptsLegacySmallerPositiveLimit(t *testing.T) {
	pkg, err := handoff.BuildPackage(handoff.PackageInput{Source: handoff.Source{Ref: "tenant:source", Cursor: "message:1"}, Target: handoff.Target{Ref: "tenant:target"}, Objective: "continue", Budget: handoff.Budget{EstimatedTokens: 1024, LimitTokens: 1024}})
	if err != nil {
		t.Fatalf("BuildPackage() error = %v", err)
	}
	encoded, _ := json.Marshal(pkg)
	if _, err := agenttasks.EncodeSessionHandoffEvent(42, string(encoded)); err != nil {
		t.Fatalf("smaller legacy budget rejected: %v", err)
	}
}

func TestSessionHandoffEventRejectsUnderstatedRealTokenEstimate(t *testing.T) {
	pkg, err := handoff.BuildPackage(handoff.PackageInput{Source: handoff.Source{Ref: "tenant:source", Cursor: "message:1"}, Target: handoff.Target{Ref: "tenant:target"}, Objective: strings.Repeat("substantial objective ", 20), Budget: handoff.Budget{EstimatedTokens: 1, LimitTokens: 2048}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(pkg)
	if _, err := agenttasks.EncodeSessionHandoffEvent(42, string(encoded)); err == nil {
		t.Fatal("understated estimated_tokens was accepted")
	}
}

func TestSessionHandoffEventRejectsRealBodyOverMaximum(t *testing.T) {
	completed := make([]string, 32)
	for index := range completed {
		completed[index] = fmt.Sprintf("%03d-%s", index, strings.Repeat("x", handoff.MaxCandidateBytes-4))
	}
	pkg, err := handoff.BuildPackage(handoff.PackageInput{Source: handoff.Source{Ref: "tenant:source", Cursor: "message:1"}, Target: handoff.Target{Ref: "tenant:target"}, Objective: "continue", Completed: completed, Budget: handoff.Budget{EstimatedTokens: 2048, LimitTokens: 2048}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(pkg)
	if _, err := agenttasks.EncodeSessionHandoffEvent(42, string(encoded)); err == nil {
		t.Fatal("real package body over 2048 tokens was accepted")
	}
}

func refreshedSessionHandoffPackage(t *testing.T) string {
	t.Helper()
	pkg, err := handoff.BuildPackage(handoff.PackageInput{
		Source: handoff.Source{Ref: "tenant:source", Cursor: "task_event:12"}, Target: handoff.Target{Ref: "tenant:target"}, Objective: "continue",
		Budget: handoff.Budget{LimitTokens: handoff.DefaultPackageTokenLimit},
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(pkg)
	if err != nil {
		t.Fatal(err)
	}
	return string(payload)
}

func TestDecodeSessionHandoffEventMatchesPackageEvidenceContract(t *testing.T) {
	sha := strings.Repeat("a", 64)
	pkg, err := handoff.BuildPackage(handoff.PackageInput{
		Source: handoff.Source{Ref: "tenant:source", Cursor: "task_event:11"}, Target: handoff.Target{Ref: "tenant:target"}, Objective: "continue",
		Evidence: []handoff.Evidence{
			{Ref: "tenant:source#task_event:11", Claim: "reported", Verification: handoff.VerificationReported, SHA256: sha},
			{Ref: "tenant:source#task_event:12", Claim: "verified", Verification: handoff.VerificationVerified, SHA256: sha, Verifier: handoff.Verifier{Kind: handoff.VerifierToolTrace, Ref: "tenant:source#tool_trace:12:0", SHA256: sha}},
		},
		Budget: handoff.Budget{LimitTokens: handoff.DefaultPackageTokenLimit},
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := handoff.EncodeSessionHandoffEvent(pkg, 42)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agenttasks.DecodeSessionHandoffEvent(payload); err != nil {
		t.Fatalf("DecodeSessionHandoffEvent(evidence package) error = %v", err)
	}
}

func validSessionHandoffEvent(t *testing.T, targetTaskID uint64) string {
	t.Helper()
	pkg, err := handoff.BuildPackage(handoff.PackageInput{
		Source: handoff.Source{Ref: "tenant:source", Cursor: "task_event:11"},
		Target: handoff.Target{Ref: "tenant:target"}, Objective: "continue",
		Budget: handoff.Budget{LimitTokens: handoff.DefaultPackageTokenLimit},
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := handoff.EncodeSessionHandoffEvent(pkg, targetTaskID)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}
