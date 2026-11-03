package sessioncontrol

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/konglong87/super-niuma/internal/agenttasks"
)

type managedProviderStoreFake struct{ *managedStoreFake }

func TestManagedProviderPersistsAcrossCreateAndFollowup(t *testing.T) {
	store := managedProviderStoreFake{&managedStoreFake{}}
	dispatcher := &managedDispatcherFake{startedID: 77}
	adapter := NewManagedAdapter(store, dispatcher)
	snapshot, err := adapter.Create(context.Background(), CreateRequest{
		Context: managedRequestContext(), SessionKey: "provider-session", Title: "Provider",
		Provider: "named-provider", Model: "model-a", CWD: "/repo",
		ReplayIdentity: managedOperationIdentity(t, OperationCreate, "provider-create"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Provider != "named-provider" {
		t.Fatalf("provider readback = %q", snapshot.Provider)
	}
	_, err = adapter.Send(context.Background(), SendRequest{
		Context: managedRequestContext(), Ref: snapshot.Ref, Content: "followup",
		ReplayIdentity: managedOperationIdentity(t, OperationSend, "provider-followup"),
	})
	if err != nil {
		t.Fatal(err)
	}
	var route map[string]any
	if err := json.Unmarshal([]byte(dispatcher.startedTask.MetadataJSON), &route); err != nil {
		t.Fatal(err)
	}
	if route["provider"] != "named-provider" {
		t.Fatalf("followup route = %v", route)
	}
}

func TestManagedRejectsProviderChangesBeforeDispatch(t *testing.T) {
	for _, status := range []string{agenttasks.StatusRunning, agenttasks.StatusReady} {
		t.Run(status, func(t *testing.T) {
			store := managedProviderStoreFake{managedStoreWithTask(status)}
			store.createdMetadata = `{"provider":"named-provider"}`
			dispatcher := &managedDispatcherFake{}
			adapter := NewManagedAdapter(store, dispatcher)
			_, err := adapter.Send(context.Background(), SendRequest{
				Context: managedRequestContext(), Ref: tenantRef("alpha"), Content: "followup", Provider: "other-provider",
				ReplayIdentity: managedOperationIdentity(t, OperationSend, "provider-change"),
			})
			assertServiceErrorCode(t, err, CodeInvalidState)
			if dispatcher.queuedTaskID != 0 || dispatcher.startedTask.ParentSessionID != 0 {
				t.Fatal("provider change reached dispatcher")
			}
		})
	}
}

func TestManagedIdleRuntimeConfigChangeSnapshotsNewRun(t *testing.T) {
	store := managedProviderStoreFake{managedStoreWithTask(agenttasks.StatusCompleted)}
	store.createdMetadata = `{"model":"model-a","provider":"provider-a","permission_mode":"ask","effort":"low","prompt_mode":"code"}`
	dispatcher := &managedDispatcherFake{startedID: 88}
	adapter := NewManagedAdapter(store, dispatcher)
	_, err := adapter.Send(context.Background(), SendRequest{
		Context: managedRequestContext(), Ref: tenantRef("alpha"), Content: "continue",
		Model: "model-b", Provider: "provider-b", PermissionMode: "deny", Effort: "high", PromptMode: "chat",
		ReplayIdentity: managedOperationIdentity(t, OperationSend, "config-change"),
	})
	if err != nil {
		t.Fatal(err)
	}
	var actual RuntimeConfig
	if err := json.Unmarshal([]byte(dispatcher.startedTask.MetadataJSON), &actual); err != nil {
		t.Fatal(err)
	}
	want := RuntimeConfig{"model-b", "provider-b", "deny", "high", "chat"}
	if actual != want || dispatcher.startedTask.Model != want.Model {
		t.Fatalf("actual=%+v task=%+v", actual, dispatcher.startedTask)
	}
	if store.upserted.SessionKey != "" || store.createdMetadata != `{"model":"model-a","provider":"provider-a","permission_mode":"ask","effort":"low","prompt_mode":"code"}` {
		t.Fatal("adapter changed Session defaults before accepted launch")
	}
}

func TestManagedConfiguredReadyRunRetryResumesInsteadOfQueueingItself(t *testing.T) {
	store := managedProviderStoreFake{managedStoreWithTask(agenttasks.StatusReady)}
	store.createdMetadata = `{"model":"model-a","provider":"provider-a","permission_mode":"ask","effort":"low","prompt_mode":"code"}`
	identity := managedOperationIdentity(t, OperationSend, "prepared-retry")
	store.tasks[0].IdempotencyKey = identity.KeyHash
	store.tasks[0].Model = "model-b"
	store.tasks[0].MetadataJSON = `{"model":"model-b","provider":"provider-b","permission_mode":"deny","effort":"high","prompt_mode":"chat"}`
	dispatcher := &managedDispatcherFake{startedID: store.tasks[0].ID}
	adapter := NewManagedAdapter(store, dispatcher)
	_, err := adapter.Send(context.Background(), SendRequest{Context: managedRequestContext(), Ref: tenantRef("alpha"), Content: "next", ReplayIdentity: identity})
	if err != nil || dispatcher.queuedTaskID != 0 || dispatcher.startedTask.Model != "model-b" {
		t.Fatalf("err=%v dispatcher=%+v", err, dispatcher)
	}
}

func TestManagedSendAdoptsOnlyUnstartedSideChatAndPreservesReplayMetadata(t *testing.T) {
	identity := managedOperationIdentity(t, OperationSend, "side-chat-first-send")
	for _, test := range []struct {
		name, source, status, key string
		adopt                     bool
	}{
		{"side chat", agenttasks.SourcePendingInputSideChat, agenttasks.StatusReady, "", true},
		{"normal ready", "webui", agenttasks.StatusReady, "", false},
		{"running side chat", agenttasks.SourcePendingInputSideChat, agenttasks.StatusRunning, "", false},
		{"claimed side chat", agenttasks.SourcePendingInputSideChat, agenttasks.StatusReady, "other-key", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := managedStoreWithTask(test.status)
			store.tasks[0].AgentName = agenttasks.AgentNameWeb
			store.tasks[0].IdempotencyKey = test.key
			store.tasks[0].MetadataJSON = fmt.Sprintf(`{"source":%q,"source_pending_input_id":"candidate-1","web_agent_session_id":7,"unrelated":"omit"}`, test.source)
			dispatcher := &managedDispatcherFake{startedID: store.tasks[0].ID, queuedID: store.tasks[0].ID}
			adapter := NewManagedAdapter(store, dispatcher)
			request := SendRequest{Context: managedRequestContext(), Ref: tenantRef("alpha"), Content: "followup", ReplayIdentity: identity}
			if _, err := adapter.Send(context.Background(), request); err != nil {
				t.Fatal(err)
			}
			if !test.adopt {
				if dispatcher.queuedTaskID == 0 || dispatcher.startedRequest.AdoptTaskID != 0 {
					t.Fatalf("non-adoptable task dispatched: %+v", dispatcher)
				}
				return
			}
			if dispatcher.queuedTaskID != 0 || dispatcher.startedRequest.AdoptTaskID != store.tasks[0].ID {
				t.Fatalf("side chat was stranded: %+v", dispatcher)
			}
			var metadata map[string]any
			if err := json.Unmarshal([]byte(dispatcher.startedTask.MetadataJSON), &metadata); err != nil {
				t.Fatal(err)
			}
			if metadata["source"] != test.source || metadata["source_pending_input_id"] != "candidate-1" || metadata["unrelated"] != nil {
				t.Fatalf("metadata=%+v", metadata)
			}
			preparedJSON := dispatcher.startedTask.MetadataJSON
			store.tasks[0].MetadataJSON, store.tasks[0].IdempotencyKey = preparedJSON, identity.KeyHash
			if _, err := adapter.Send(context.Background(), request); err != nil || dispatcher.startedTask.MetadataJSON != preparedJSON || dispatcher.startedRequest.AdoptTaskID != 0 {
				t.Fatalf("retry metadata changed: %+v err=%v", dispatcher, err)
			}
		})
	}
}

func TestManagedListUsesOnePrivateProjectionAndMatchesPreparedConfig(t *testing.T) {
	store := managedStoreWithTask(agenttasks.StatusReady)
	store.createdMetadata = `{"model":"obsolete-metadata","provider":"provider-a","permission_mode":"ask","effort":"low","prompt_mode":"code"}`
	store.tasks[0].Model = "model-b"
	store.tasks[0].MetadataJSON = `{"provider":"provider-b","permission_mode":"deny","effort":"high","prompt_mode":"chat"}`
	adapter := NewManagedAdapter(store, &managedDispatcherFake{})
	items, err := adapter.List(context.Background(), ListRequest{Context: managedRequestContext(), Source: SourceTenant})
	if err != nil || len(items) != 1 || store.listSessionsCalls != 1 || store.getSessionByKeyCalls != 0 {
		t.Fatalf("items=%+v err=%v list=%d get=%d", items, err, store.listSessionsCalls, store.getSessionByKeyCalls)
	}
	detail, err := adapter.Get(context.Background(), GetRequest{Context: managedRequestContext(), Ref: tenantRef("alpha")})
	if err != nil || detail.RuntimeConfig() != items[0].RuntimeConfig() || detail.Model != "model-b" {
		t.Fatalf("detail=%+v list=%+v err=%v", detail, items, err)
	}
	store.tasks = nil
	detail, err = adapter.Get(context.Background(), GetRequest{Context: managedRequestContext(), Ref: tenantRef("alpha")})
	if err != nil || detail.Model != "model-a" {
		t.Fatalf("Session model column did not override stale metadata: %+v %v", detail, err)
	}
}

func TestManagedBusyRejectsEachRuntimeConfigChangeAndAllowsSameConfigQueue(t *testing.T) {
	base := RuntimeConfig{"model-a", "provider-a", "ask", "low", "code"}
	for _, changed := range []RuntimeConfig{{Model: "model-b"}, {Provider: "provider-b"}, {PermissionMode: "deny"}, {Effort: "high"}, {PromptMode: "chat"}, base} {
		t.Run(fmt.Sprintf("%+v", changed), func(t *testing.T) {
			store := managedProviderStoreFake{managedStoreWithTask(agenttasks.StatusRunning)}
			encoded, _ := json.Marshal(base)
			store.createdMetadata = string(encoded)
			dispatcher := &managedDispatcherFake{queuedID: 42}
			adapter := NewManagedAdapter(store, dispatcher)
			_, err := adapter.Send(context.Background(), SendRequest{Context: managedRequestContext(), Ref: tenantRef("alpha"), Content: "next", Model: changed.Model, Provider: changed.Provider, PermissionMode: changed.PermissionMode, Effort: changed.Effort, PromptMode: changed.PromptMode})
			if changed == base {
				if err != nil || dispatcher.queuedTaskID == 0 {
					t.Fatalf("same config did not queue: %v", err)
				}
			} else {
				assertServiceErrorCode(t, err, CodeInvalidState)
				if dispatcher.queuedTaskID != 0 || dispatcher.startedTask.ParentSessionID != 0 {
					t.Fatal("rejected configuration reached dispatcher")
				}
			}
		})
	}
}
