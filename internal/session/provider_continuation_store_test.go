package session

import (
	"context"
	"testing"

	"github.com/konglong87/super-niuma/internal/config"
	providerstate "github.com/konglong87/super-niuma/internal/provider"
)

func TestTranscriptProviderContinuationStoreSaveLoadInvalidate(t *testing.T) {
	store := Store{Root: t.TempDir(), SchemaV2: true}
	recorder, err := store.NewRecorder(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()

	key := providerstate.ContinuationKey{
		Protocol: config.ProviderProtocolOpenAIResponses, Provider: "primary",
		EndpointID: providerstate.EndpointID("https://example.com/v1"), Model: "gpt-test",
	}
	continuations := NewTranscriptProviderContinuationStore(recorder)
	value := providerstate.Continuation{
		OpaqueItems: []providerstate.OpaqueItem{{Type: "reasoning", ID: "rs_1", EncryptedContent: "cipher"}},
	}
	if err := continuations.Save(context.Background(), key, value); err != nil {
		t.Fatal(err)
	}
	loaded, err := continuations.Load(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if loaded == nil || loaded.Version != providerstate.ContinuationVersion || loaded.OpaqueItems[0].ID != "rs_1" {
		t.Fatalf("loaded = %+v", loaded)
	}
	if err := continuations.Invalidate(context.Background(), key, "provider changed"); err != nil {
		t.Fatal(err)
	}
	loaded, err = continuations.Load(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if loaded != nil {
		t.Fatalf("invalidated continuation loaded: %+v", loaded)
	}

	entries, format, err := LoadWithFormat(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	if format != TranscriptFormatGolangCCV2 || len(entries) < 2 || entries[len(entries)-1].Type != EntryTypeProviderContinuation {
		t.Fatalf("format=%s entries=%+v", format, entries)
	}
}

func TestTranscriptProviderContinuationStoreFollowsActiveBranch(t *testing.T) {
	store := Store{Root: t.TempDir(), SchemaV2: true}
	recorder, err := store.NewRecorder(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()

	rootID := NewEntryID()
	if err := recorder.Append(Entry{ID: rootID, Type: "message", Role: "user", Content: "root"}); err != nil {
		t.Fatal(err)
	}
	key := providerstate.ContinuationKey{
		Protocol: config.ProviderProtocolOpenAIResponses, Provider: "primary",
		EndpointID: providerstate.EndpointID("https://example.com/v1"), Model: "gpt-test",
	}
	continuations := NewTranscriptProviderContinuationStore(recorder)
	if err := continuations.Save(context.Background(), key, providerstate.Continuation{
		OpaqueItems: []providerstate.OpaqueItem{{Type: "reasoning", ID: "old", EncryptedContent: "old-branch"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(Entry{Type: EntryTypeBranchHead, LeafID: rootID, Reason: BranchHeadReasonRewind}); err != nil {
		t.Fatal(err)
	}
	if err := continuations.Save(context.Background(), key, providerstate.Continuation{
		OpaqueItems: []providerstate.OpaqueItem{{Type: "reasoning", ID: "new", EncryptedContent: "new-branch"}},
	}); err != nil {
		t.Fatal(err)
	}

	loaded, err := continuations.Load(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if loaded == nil || len(loaded.OpaqueItems) != 1 || loaded.OpaqueItems[0].ID != "new" {
		t.Fatalf("active branch continuation = %+v", loaded)
	}
	chain, err := LoadConversation(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range chain {
		if entry.Type != EntryTypeProviderContinuation {
			continue
		}
		value, decodeErr := providerstate.DecodeContinuation(entry.Metadata)
		if decodeErr == nil && len(value.OpaqueItems) > 0 && value.OpaqueItems[0].ID == "old" {
			t.Fatalf("abandoned branch leaked into current chain: %+v", chain)
		}
	}
}
