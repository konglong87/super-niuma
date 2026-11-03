package agentprofile

import (
	"context"
	"testing"

	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

type fakeMySQLProfileStore struct {
	profiles   []mysqlstore.AgentProfile
	assignment mysqlstore.AgentProfileAssignment
}

func (f fakeMySQLProfileStore) ListAgentProfiles(context.Context, uint64, uint64, string, int) ([]mysqlstore.AgentProfile, error) {
	return append([]mysqlstore.AgentProfile(nil), f.profiles...), nil
}

func (f fakeMySQLProfileStore) GetAgentProfile(context.Context, uint64, uint64) (mysqlstore.AgentProfile, error) {
	for _, profile := range f.profiles {
		if profile.ID == f.assignment.ProfileID {
			return profile, nil
		}
	}
	return mysqlstore.AgentProfile{}, mysqlstore.ErrNotFound
}

func (f fakeMySQLProfileStore) GetAgentProfileAssignment(context.Context, uint64, uint64, string) (mysqlstore.AgentProfileAssignment, error) {
	return f.assignment, nil
}

func TestMySQLSourceMapsPublishedProfileAndAssignment(t *testing.T) {
	copywriter := builtinCopywriter()
	configJSON, err := copywriter.Config.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	source := NewMySQLSource(fakeMySQLProfileStore{
		profiles: []mysqlstore.AgentProfile{{
			ID: 42, TenantID: 7, OwnerKey: "shared", ProfileKey: copywriter.Key, Scope: string(copywriter.Scope),
			DisplayName: copywriter.DisplayName, ProfileVersion: copywriter.Version, Status: string(StatusPublished), ConfigJSON: string(configJSON),
		}},
		assignment: mysqlstore.AgentProfileAssignment{TenantID: 7, UserID: 11, Surface: SurfaceWebChat, ProfileID: 42},
	})

	profile, err := source.FindPublishedProfile(context.Background(), 7, 11, ProfileCopywriter, 1)
	if err != nil || profile.Key != ProfileCopywriter || profile.Version != 1 || profile.Config.Prompt.Mode != PromptModeChat {
		t.Fatalf("profile=%+v err=%v", profile, err)
	}
	assignment, err := source.FindAssignment(context.Background(), 7, 11, SurfaceWebChat)
	if err != nil || assignment.ProfileKey != ProfileCopywriter || assignment.ProfileVersion != 1 {
		t.Fatalf("assignment=%+v err=%v", assignment, err)
	}
}

func TestMySQLSourcePreservesV2Metadata(t *testing.T) {
	document := textProfileV2()
	document.Identity.RoleType = RoleTypeReviewer
	encoded, err := document.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	source := NewMySQLSource(fakeMySQLProfileStore{
		profiles: []mysqlstore.AgentProfile{{
			ID: 43, TenantID: 7, OwnerKey: "shared", ProfileKey: "reviewer-v2", Scope: string(ScopeTenantShared),
			DisplayName: "Reviewer", ProfileVersion: 2, Status: string(StatusPublished), ConfigJSON: string(encoded),
		}},
	})
	profile, err := source.FindPublishedProfile(context.Background(), 7, 11, "reviewer-v2", 2)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Config.SchemaVersion != SchemaVersionV2 || profile.Config.Identity.RoleType != RoleTypeReviewer || profile.Config.Execution.Kind != ExecutionKindText {
		t.Fatalf("stored V2 metadata lost: %+v", profile.Config)
	}
	before, _ := document.CanonicalHash()
	after, err := profile.Config.CanonicalHash()
	if err != nil || before != after {
		t.Fatalf("stored V2 hash changed: %s != %s, err=%v", before, after, err)
	}
}
