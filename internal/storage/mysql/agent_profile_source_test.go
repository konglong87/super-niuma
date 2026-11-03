package mysql

import "testing"

func TestAgentProfileFromRowDerivesDatabaseSource(t *testing.T) {
	item := agentProfileFromRow(gormAgentProfile{ID: 42, TenantID: 7, ProfileKey: "writer", ProfileVersion: 3, Status: "published", ConfigJSON: `{}`, EffectiveHash: "hash"})
	if item.SourceKind != "database" || item.SourceRef != "agent_profiles/42" || item.SourcePath != "" {
		t.Fatalf("unexpected source metadata: %#v", item)
	}
}
