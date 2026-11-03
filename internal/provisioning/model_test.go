package provisioning

import (
	"strings"
	"testing"
	"time"
)

func TestSessionTransitionsAndIdempotency(t *testing.T) {
	session := NewSession("tenant-a", "copywriter", "copywriter-feishu")
	if session.Status != StatusDraft || session.IdempotencyKey() == "" {
		t.Fatalf("unexpected initial session: %#v", session)
	}
	for _, next := range []Status{StatusValidating, StatusPublished, StatusPreflight, StatusStarting, StatusRunning} {
		if err := session.Transition(next); err != nil {
			t.Fatalf("transition to %s: %v", next, err)
		}
	}
	if session.IdempotencyKey() != session.IdempotencyKey() {
		t.Fatal("idempotency key must be stable")
	}
	if err := session.Transition(StatusDraft); err == nil {
		t.Fatal("expected invalid transition")
	}
}

func TestSessionSecretRedaction(t *testing.T) {
	session := NewSession("tenant-a", "writer", "writer-feishu")
	session.Credential = CredentialRef{ID: "cred-1", Provider: "feishu", SecretPresent: true}
	session.Worker = WorkerSpec{AccountKey: "writer-feishu", PayloadKeyRef: "payload-ref", Provider: "glm-5.1", Model: "glm-5.1"}
	redacted := session.Redacted()
	if strings.Contains(redacted.Credential.SecretValue, "secret") || redacted.Credential.SecretValue != "" {
		t.Fatalf("secret leaked: %#v", redacted.Credential)
	}
	if redacted.Worker.PayloadKeyRef == "" || redacted.Credential.ID != "cred-1" {
		t.Fatalf("safe references missing: %#v", redacted)
	}
}

func TestWorkerDesiredObservedState(t *testing.T) {
	worker := WorkerStatus{State: WorkerStateRunning, PID: 42, Screen: "niuma-channel-writer", ObservedAt: time.Now()}
	if !worker.Healthy() {
		t.Fatal("running worker with pid should be healthy")
	}
	worker.ObservedAt = time.Time{}
	if worker.Healthy() {
		t.Fatal("zero observed time should not be healthy")
	}
}
