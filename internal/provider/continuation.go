package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/konglong87/super-niuma/internal/config"
)

const (
	ContinuationVersion        = 1
	MaxContinuationEncodedSize = 1 << 20
)

type ContinuationKey struct {
	TenantID   string
	UserID     string
	SessionID  string
	BranchID   string
	Protocol   config.ProviderProtocol
	Provider   string
	EndpointID string
	Model      string
}

type OpaqueItem struct {
	Type             string `json:"type"`
	ID               string `json:"id"`
	EncryptedContent string `json:"encrypted_content"`
}

type Continuation struct {
	Version      int                     `json:"version"`
	Protocol     config.ProviderProtocol `json:"protocol"`
	Provider     string                  `json:"provider"`
	EndpointID   string                  `json:"endpoint_hash"`
	Model        string                  `json:"model"`
	ResponseID   string                  `json:"response_id,omitempty"`
	BranchID     string                  `json:"branch_id,omitempty"`
	OpaqueItems  []OpaqueItem            `json:"opaque_items,omitempty"`
	Invalidated  bool                    `json:"invalidated,omitempty"`
	Invalidation string                  `json:"invalidation_reason,omitempty"`
}

type ProviderContinuationStore interface {
	Load(context.Context, ContinuationKey) (*Continuation, error)
	Save(context.Context, ContinuationKey, Continuation) error
	Invalidate(context.Context, ContinuationKey, string) error
}

func EndpointID(endpoint string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(endpoint)))
	return hex.EncodeToString(sum[:])
}

func EncodeContinuation(value Continuation) ([]byte, error) {
	if value.Version == 0 {
		value.Version = ContinuationVersion
	}
	if err := ValidateContinuation(value); err != nil {
		return nil, err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(data) > MaxContinuationEncodedSize {
		return nil, fmt.Errorf("provider continuation exceeds %d bytes", MaxContinuationEncodedSize)
	}
	return data, nil
}

func DecodeContinuation(data []byte) (Continuation, error) {
	if len(data) == 0 {
		return Continuation{}, errors.New("provider continuation is empty")
	}
	if len(data) > MaxContinuationEncodedSize {
		return Continuation{}, fmt.Errorf("provider continuation exceeds %d bytes", MaxContinuationEncodedSize)
	}
	var value Continuation
	if err := json.Unmarshal(data, &value); err != nil {
		return Continuation{}, fmt.Errorf("decode provider continuation: %w", err)
	}
	if err := ValidateContinuation(value); err != nil {
		return Continuation{}, err
	}
	return value, nil
}

func ValidateContinuation(value Continuation) error {
	if value.Version != ContinuationVersion {
		return fmt.Errorf("unsupported provider continuation version %d", value.Version)
	}
	if strings.TrimSpace(string(value.Protocol)) == "" {
		return errors.New("provider continuation protocol is required")
	}
	if strings.TrimSpace(value.Provider) == "" || strings.TrimSpace(value.EndpointID) == "" || strings.TrimSpace(value.Model) == "" {
		return errors.New("provider continuation provider, endpoint_hash, and model are required")
	}
	for _, item := range value.OpaqueItems {
		if item.Type == "" || item.ID == "" || item.EncryptedContent == "" {
			return errors.New("provider continuation opaque item type, id, and encrypted_content are required")
		}
	}
	return nil
}

func MatchesKey(value Continuation, key ContinuationKey) bool {
	return value.Protocol == key.Protocol &&
		value.Provider == key.Provider &&
		value.EndpointID == key.EndpointID &&
		value.Model == key.Model &&
		(key.BranchID == "" || value.BranchID == "" || value.BranchID == key.BranchID)
}
