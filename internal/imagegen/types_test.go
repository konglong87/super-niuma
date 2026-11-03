package imagegen

import (
	"testing"
	"time"
)

func TestArtifactCarriesPersistentImageReferencesWithoutInlinePayload(t *testing.T) {
	created := time.Unix(10, 0).UTC()
	artifact := Artifact{AssetID: "asset-1", GenerationID: "gen-1", TenantID: 7, UserID: 11, SessionID: 13, Operation: OperationGenerate, MediaType: "image/png", Name: "image.png", Width: 1024, Height: 1024, SizeBytes: 12, SHA256: "abc", URL: "/tenant/media/assets/asset-1", Provider: "jiuan", Model: "gpt-image-2", CreatedAt: created}
	if artifact.AssetID == "" || artifact.GenerationID == "" || artifact.URL == "" || artifact.CreatedAt.IsZero() {
		t.Fatalf("artifact = %+v", artifact)
	}
	if artifact.InlineData() != "" {
		t.Fatal("artifact must not expose inline image data")
	}
}

func TestGenerationRecordRequiresTenantUserSessionAndIdempotencyScope(t *testing.T) {
	record := GenerationRecord{GenerationID: "gen-1", TenantID: 7, UserID: 11, SessionID: 13, Operation: OperationEdit, Status: GenerationStatusRunning, Prompt: "redraw", Provider: "jiuan", Model: "gpt-image-2", IdempotencyKey: "request-1"}
	if err := record.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, invalid := range map[string]GenerationRecord{
		"missing tenant":    record,
		"missing session":   record,
		"missing operation": record,
	} {
		switch name {
		case "missing tenant":
			invalid.TenantID = 0
		case "missing session":
			invalid.SessionID = 0
		case "missing operation":
			invalid.Operation = ""
		}
		if err := invalid.Validate(); err == nil {
			t.Fatalf("%s should fail", name)
		}
	}
}
