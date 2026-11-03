package onboarding

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFileCredentialsStoreRoundTripKeepsSecretAndOwnerOnlyMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "feishu.json")
	store := FileCredentialsStore{Path: path}
	if err := store.SaveFeishuCredentials(context.Background(), FeishuCredentials{AppID: "cli_x", AppSecret: "secret"}); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadFeishuCredentials()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.AppID != "cli_x" || loaded.AppSecret != "secret" {
		t.Fatalf("loaded = %+v", loaded)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
}
