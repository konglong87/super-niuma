package session

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	providerstate "github.com/konglong87/super-niuma/internal/provider"
)

const EntryTypeProviderContinuation = "provider_continuation"

type TranscriptProviderContinuationStore struct {
	recorder *Recorder
}

var _ providerstate.ProviderContinuationStore = (*TranscriptProviderContinuationStore)(nil)

func NewTranscriptProviderContinuationStore(recorder *Recorder) *TranscriptProviderContinuationStore {
	return &TranscriptProviderContinuationStore{recorder: recorder}
}

func (s *TranscriptProviderContinuationStore) Load(ctx context.Context, key providerstate.ContinuationKey) (*providerstate.Continuation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil || s.recorder == nil || strings.TrimSpace(s.recorder.Path) == "" {
		return nil, errors.New("provider continuation transcript recorder is required")
	}
	entries, err := LoadConversation(s.recorder.Path)
	if err != nil {
		return nil, err
	}
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Type != EntryTypeProviderContinuation {
			continue
		}
		value, err := providerstate.DecodeContinuation(entries[i].Metadata)
		if err != nil {
			// A malformed latest envelope must not resurrect an older remote state.
			// Stateless local message replay remains the safe fallback.
			return nil, nil
		}
		if !providerstate.MatchesKey(value, key) {
			continue
		}
		if value.Invalidated {
			return nil, nil
		}
		return &value, nil
	}
	return nil, nil
}

func (s *TranscriptProviderContinuationStore) Save(ctx context.Context, key providerstate.ContinuationKey, value providerstate.Continuation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.recorder == nil {
		return errors.New("provider continuation transcript recorder is required")
	}
	fillContinuationKey(&value, key)
	data, err := providerstate.EncodeContinuation(value)
	if err != nil {
		return err
	}
	return s.recorder.Append(Entry{Type: EntryTypeProviderContinuation, Metadata: json.RawMessage(data)})
}

func (s *TranscriptProviderContinuationStore) Invalidate(ctx context.Context, key providerstate.ContinuationKey, reason string) error {
	value := providerstate.Continuation{Version: providerstate.ContinuationVersion, Invalidated: true, Invalidation: strings.TrimSpace(reason)}
	return s.Save(ctx, key, value)
}

func fillContinuationKey(value *providerstate.Continuation, key providerstate.ContinuationKey) {
	if value.Version == 0 {
		value.Version = providerstate.ContinuationVersion
	}
	value.Protocol = key.Protocol
	value.Provider = key.Provider
	value.EndpointID = key.EndpointID
	value.Model = key.Model
	value.BranchID = key.BranchID
}

func AppendProviderContinuation(recorder *Recorder, value providerstate.Continuation) error {
	if recorder == nil {
		return nil
	}
	if value.BranchID == "" && recorder.IsV2() {
		value.BranchID = recorder.Leaf()
	}
	data, err := providerstate.EncodeContinuation(value)
	if err != nil {
		return err
	}
	return recorder.Append(Entry{Type: EntryTypeProviderContinuation, Metadata: data})
}
