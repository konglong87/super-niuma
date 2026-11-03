package query

import (
	"testing"

	"github.com/konglong87/super-niuma/internal/tools"
)

func TestContextManifestIncludesAgentProfileIdentity(t *testing.T) {
	session := New(nil, tools.NewRegistry(), Options{
		CWD:                          t.TempDir(),
		PromptMode:                   "chat",
		AgentProfileKey:              "copywriter",
		AgentProfileVersion:          3,
		AgentProfileSource:           "assignment",
		AgentProfileRequestedHash:    "requested",
		AgentProfileEffectiveHash:    "effective",
		AgentProfileBlockedOverrides: 2,
	})
	manifest := session.contextManifest(nil, contextAssembly{}, SkillsCatalogManifest{}, TenantSkillInlineManifest{})
	if manifest.AgentProfileKey != "copywriter" || manifest.AgentProfileVersion != 3 || manifest.AgentProfileSource != "assignment" || manifest.AgentProfileRequestedHash != "requested" || manifest.AgentProfileEffectiveHash != "effective" || manifest.AgentProfileBlockedOverrides != 2 {
		t.Fatalf("agent profile manifest = %+v", manifest)
	}
}
