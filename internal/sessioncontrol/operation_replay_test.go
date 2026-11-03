package sessioncontrol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOperationIdentitySeparatesKeyLookupFromSemanticFingerprint(t *testing.T) {
	requestContext := testRequestContext()
	fingerprintA := strings.Repeat("a", 64)
	fingerprintB := strings.Repeat("b", 64)
	first, err := newOperationIdentity(OperationSend, requestContext, "secret-key", fingerprintA)
	if err != nil {
		t.Fatal(err)
	}
	same, err := newOperationIdentity(OperationSend, requestContext, "secret-key", fingerprintA)
	if err != nil {
		t.Fatal(err)
	}
	changedBody, err := newOperationIdentity(OperationSend, requestContext, "secret-key", fingerprintB)
	if err != nil {
		t.Fatal(err)
	}
	changedKey, err := newOperationIdentity(OperationSend, requestContext, "other-key", fingerprintA)
	if err != nil {
		t.Fatal(err)
	}

	if first != same {
		t.Fatalf("equal request identities differ: %#v != %#v", first, same)
	}
	if first.KeyHash != changedBody.KeyHash {
		t.Fatalf("semantic change changed key lookup: %q != %q", first.KeyHash, changedBody.KeyHash)
	}
	if first.OperationID == changedBody.OperationID {
		t.Fatal("semantic change did not change operation ID")
	}
	if first.KeyHash == changedKey.KeyHash || first.OperationID == changedKey.OperationID {
		t.Fatal("different idempotency keys aliased")
	}
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "secret-key") {
		t.Fatalf("operation identity leaked the raw idempotency key: %s", encoded)
	}
}

func TestOperationIdentityScopesTenantUserActorAndOperation(t *testing.T) {
	baseContext := testRequestContext()
	fingerprint := strings.Repeat("a", 64)
	base, err := newOperationIdentity(OperationCreate, baseContext, "key", fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		operation Operation
		context   RequestContext
	}{
		"tenant":    {operation: OperationCreate, context: RequestContext{TenantID: 9, UserID: baseContext.UserID, ActorUserID: baseContext.ActorUserID}},
		"user":      {operation: OperationCreate, context: RequestContext{TenantID: baseContext.TenantID, UserID: 9, ActorUserID: baseContext.ActorUserID}},
		"actor":     {operation: OperationCreate, context: RequestContext{TenantID: baseContext.TenantID, UserID: baseContext.UserID, ActorUserID: 9}},
		"operation": {operation: OperationSend, context: baseContext},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := newOperationIdentity(test.operation, test.context, "key", fingerprint)
			if err != nil {
				t.Fatal(err)
			}
			if got.KeyHash == base.KeyHash || got.OperationID == base.OperationID {
				t.Fatalf("%s scope aliased identity: %#v", name, got)
			}
		})
	}
}

func TestOperationMetadataRoundTripAndReplayDecision(t *testing.T) {
	identity, err := newOperationIdentity(OperationAttach, testRequestContext(), "key", strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := encodeOperationMetadata(identity)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeOperationMetadata(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != identity.Metadata() {
		t.Fatalf("decoded metadata = %#v, want %#v", decoded, identity.Metadata())
	}
	if err := validateRecoveredMetadata(identity, decoded); err != nil {
		t.Fatalf("equal metadata rejected: %v", err)
	}

	changedRequest, err := newOperationIdentity(OperationAttach, testRequestContext(), "key", strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	assertServiceErrorCode(t, validateRecoveredMetadata(changedRequest, decoded), CodeIdempotencyConflict)

	for name, payload := range map[string]string{
		"unknown field": strings.TrimSuffix(encoded, "}") + `,"raw_key":"secret"}`,
		"bad schema":    strings.Replace(encoded, operationMetadataSchema, "unknown", 1),
		"bad hash":      strings.Replace(encoded, identity.KeyHash, "short", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeOperationMetadata(payload); err == nil {
				t.Fatal("decodeOperationMetadata() error = nil")
			}
		})
	}
}

func TestOperationIdentityRejectsIncompleteInputs(t *testing.T) {
	for name, test := range map[string]struct {
		operation   Operation
		context     RequestContext
		key         string
		fingerprint string
	}{
		"operation":   {context: testRequestContext(), key: "key", fingerprint: strings.Repeat("a", 64)},
		"context":     {operation: OperationSend, key: "key", fingerprint: strings.Repeat("a", 64)},
		"key":         {operation: OperationSend, context: testRequestContext(), fingerprint: strings.Repeat("a", 64)},
		"fingerprint": {operation: OperationSend, context: testRequestContext(), key: "key"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := newOperationIdentity(test.operation, test.context, test.key, test.fingerprint); err == nil {
				t.Fatal("newOperationIdentity() error = nil")
			}
		})
	}
}

func TestOperationMetadataEmbedsWithoutOverwritingResourceMetadata(t *testing.T) {
	identity, err := newOperationIdentity(OperationSend, testRequestContext(), "key", strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	merged, err := mergeOperationMetadataJSON(`{"cwd":"/repo","source":"webui"}`, identity)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal([]byte(merged), &document); err != nil {
		t.Fatal(err)
	}
	if string(document["cwd"]) != `"/repo"` || string(document["source"]) != `"webui"` {
		t.Fatalf("resource metadata was overwritten: %s", merged)
	}
	metadata, err := operationMetadataFromJSON(merged)
	if err != nil {
		t.Fatal(err)
	}
	if metadata != identity.Metadata() {
		t.Fatalf("metadata = %#v, want %#v", metadata, identity.Metadata())
	}
}

func TestOperationMetadataEmbeddingRejectsMalformedOrConflictingData(t *testing.T) {
	identity, err := newOperationIdentity(OperationCreate, testRequestContext(), "key", strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	for name, payload := range map[string]string{
		"malformed": `{`,
		"array":     `[]`,
		"occupied":  `{"session_control_operation":{"schema":"foreign"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := mergeOperationMetadataJSON(payload, identity); err == nil {
				t.Fatal("mergeOperationMetadataJSON() error = nil")
			}
		})
	}
	if _, err := operationMetadataFromJSON(`{"cwd":"/repo"}`); err == nil {
		t.Fatal("operationMetadataFromJSON() accepted missing operation metadata")
	}
}
