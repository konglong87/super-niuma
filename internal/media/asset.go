// Package media contains provider-neutral attachment state and authorization
// metadata. It intentionally does not perform uploads or image processing.
package media

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

type State string

const (
	StateSelected    State = "selected"
	StateHashPending State = "hash_pending"
	StateUploading   State = "uploading"
	StateUploaded    State = "uploaded"
	StateProcessing  State = "processing"
	StateReady       State = "ready"
	StateFailed      State = "failed"
)

const KindImage = "image"

type Asset struct {
	AssetID     string             `json:"asset_id"`
	Kind        string             `json:"kind"`
	MediaType   string             `json:"media_type"`
	Name        string             `json:"name,omitempty"`
	SizeBytes   int64              `json:"size_bytes,omitempty"`
	SHA256      string             `json:"sha256,omitempty"`
	State       State              `json:"state"`
	TenantID    uint64             `json:"tenant_id"`
	UserID      uint64             `json:"user_id"`
	SessionID   uint64             `json:"session_id,omitempty"`
	Original    Variant            `json:"original,omitempty"`
	Derivatives map[string]Variant `json:"derivatives,omitempty"`
	Access      AccessPolicy       `json:"access"`
	Error       string             `json:"error,omitempty"`
	ExpiresAt   time.Time          `json:"expires_at,omitempty"`
}

type Variant struct {
	URL        string `json:"url,omitempty"`
	Path       string `json:"path,omitempty"`
	MediaType  string `json:"media_type,omitempty"`
	SizeBytes  int64  `json:"size_bytes,omitempty"`
	SHA256     string `json:"sha256,omitempty"`
	InlineData string `json:"-"`
}

type AccessPolicy struct {
	TenantID  uint64 `json:"tenant_id"`
	UserID    uint64 `json:"user_id"`
	SessionID uint64 `json:"session_id,omitempty"`
}

type LegacyAttachment struct {
	AttachmentID string
	Type         string
	MediaType    string
	Name         string
	URL          string
	Path         string
	SizeBytes    int64
	SHA256       string
}

func (a *Asset) Transition(next State) error {
	if a == nil {
		return fmt.Errorf("media asset is nil")
	}
	if a.State == next {
		return nil
	}
	allowed := map[State]map[State]bool{
		StateSelected:    {StateHashPending: true, StateFailed: true},
		StateHashPending: {StateUploading: true, StateFailed: true},
		StateUploading:   {StateUploaded: true, StateFailed: true},
		StateUploaded:    {StateProcessing: true, StateReady: true, StateFailed: true},
		StateProcessing:  {StateReady: true, StateFailed: true},
		StateReady:       {StateProcessing: true},
		StateFailed:      {StateSelected: true, StateHashPending: true},
	}
	if !allowed[a.State][next] {
		return fmt.Errorf("invalid media asset transition %s -> %s", a.State, next)
	}
	a.State = next
	if next != StateFailed {
		a.Error = ""
	}
	return nil
}

func FromLegacyAttachment(legacy LegacyAttachment, tenantID, userID, sessionID uint64) (Asset, error) {
	id := strings.TrimSpace(legacy.AttachmentID)
	if id == "" {
		return Asset{}, fmt.Errorf("legacy attachment id is required")
	}
	kind := strings.TrimSpace(legacy.Type)
	if kind == "" {
		kind = KindImage
	}
	if strings.TrimSpace(legacy.URL) == "" && strings.TrimSpace(legacy.Path) == "" {
		return Asset{}, fmt.Errorf("legacy attachment %q has no readable reference", id)
	}
	if legacy.URL != "" {
		parsed, err := url.Parse(legacy.URL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return Asset{}, fmt.Errorf("legacy attachment %q has unsupported url", id)
		}
	}
	variant := Variant{URL: legacy.URL, Path: legacy.Path, MediaType: legacy.MediaType, SizeBytes: legacy.SizeBytes, SHA256: legacy.SHA256}
	return Asset{AssetID: id, Kind: kind, MediaType: legacy.MediaType, Name: legacy.Name, SizeBytes: legacy.SizeBytes, SHA256: legacy.SHA256, State: StateReady, TenantID: tenantID, UserID: userID, SessionID: sessionID, Original: variant, Access: AccessPolicy{TenantID: tenantID, UserID: userID, SessionID: sessionID}}, nil
}
