package credentials

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/provisioning"
)

func TestFileStoreUses0600AndRedactsReadResult(t *testing.T) {
	store := FileStore{Dir: t.TempDir()}
	saved, err := store.Put(context.Background(), provisioning.CredentialRef{ID: "feishu-1", Provider: "feishu", AppID: "cli_x", SecretValue: "super-secret"})
	if err != nil || saved.SecretValue != "" || !saved.SecretPresent {
		t.Fatalf("saved=%#v err=%v", saved, err)
	}
	info, _ := os.Stat(filepath.Join(store.Dir, "feishu-1.credential"))
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}
	loaded, err := store.Get(context.Background(), "feishu-1")
	if err != nil || loaded.SecretValue != "super-secret" {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
	if strings.Contains(string(mustRead(t, filepath.Join(store.Dir, "feishu-1.credential"))), "super-secret") == false {
		t.Fatal("credential file should contain secret for worker use")
	}
}
func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
