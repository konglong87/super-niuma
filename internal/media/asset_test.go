package media

import "testing"

func TestAssetLifecycleAllowsUploadProcessingAndReady(t *testing.T) {
	asset := Asset{AssetID: "asset-1", TenantID: 7, UserID: 11, SessionID: 13, Kind: KindImage, State: StateSelected}
	for _, next := range []State{StateHashPending, StateUploading, StateUploaded, StateProcessing, StateReady} {
		if err := asset.Transition(next); err != nil {
			t.Fatalf("transition %s -> %s: %v", asset.State, next, err)
		}
	}
	if asset.State != StateReady {
		t.Fatalf("state = %s", asset.State)
	}
}

func TestAssetLifecycleRejectsReadyToUploadingRegression(t *testing.T) {
	asset := Asset{AssetID: "asset-1", State: StateReady}
	if err := asset.Transition(StateUploading); err == nil {
		t.Fatal("expected invalid lifecycle transition")
	}
}

func TestLegacyAttachmentMapsToReadyAssetWithoutPayload(t *testing.T) {
	asset, err := FromLegacyAttachment(LegacyAttachment{AttachmentID: "att-1", Type: "image", MediaType: "image/png", Name: "shot.png", URL: "https://cdn.example.test/shot.png", SizeBytes: 42, SHA256: "hash"}, 7, 11, 13)
	if err != nil {
		t.Fatalf("map legacy attachment: %v", err)
	}
	if asset.AssetID != "att-1" || asset.State != StateReady || asset.Original.URL != "https://cdn.example.test/shot.png" || asset.Original.InlineData != "" {
		t.Fatalf("asset = %+v", asset)
	}
}
