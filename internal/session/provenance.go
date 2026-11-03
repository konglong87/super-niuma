package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// SummaryProvenance identifies the persisted transcript material used by a
// bounded summary. It stores references and a digest, never source contents.
type SummaryProvenance struct {
	Version          int      `json:"version"`
	SourceEntryIDs   []string `json:"source_entry_ids,omitempty"`
	SourceStartID    string   `json:"source_start_id,omitempty"`
	SourceEndID      string   `json:"source_end_id,omitempty"`
	SourceEntryCount int      `json:"source_entry_count"`
	SourceDigest     string   `json:"source_digest,omitempty"`
}

// NewSummaryProvenance builds a compact provenance record. sourceEntries is
// the logical range considered by the summarizer; referencedEntries are the
// entries rendered or otherwise carried into the summary.
func NewSummaryProvenance(sourceEntries, referencedEntries []Entry) SummaryProvenance {
	provenance := SummaryProvenance{
		Version:          1,
		SourceEntryCount: len(sourceEntries),
	}
	for _, entry := range sourceEntries {
		if provenance.SourceStartID == "" {
			provenance.SourceStartID = strings.TrimSpace(entry.ID)
		}
		if id := strings.TrimSpace(entry.ID); id != "" {
			provenance.SourceEndID = id
		}
	}
	if len(sourceEntries) > 0 {
		provenance.SourceDigest = digestEntries(sourceEntries)
	}
	seen := make(map[string]struct{}, len(referencedEntries))
	for _, entry := range referencedEntries {
		id := strings.TrimSpace(entry.ID)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		provenance.SourceEntryIDs = append(provenance.SourceEntryIDs, id)
	}
	return provenance
}

func digestEntries(entries []Entry) string {
	hash := sha256.New()
	for _, entry := range entries {
		data, err := json.Marshal(entry)
		if err != nil {
			continue
		}
		_, _ = hash.Write(data)
		_, _ = hash.Write([]byte{'\n'})
	}
	return hex.EncodeToString(hash.Sum(nil))
}
