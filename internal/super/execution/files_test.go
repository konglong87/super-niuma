//go:build darwin || linux

package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/query"
	superstate "github.com/konglong87/super-niuma/internal/super"
	"golang.org/x/sys/unix"
)

func toolRequest(t *testing.T, req Request, name, p, content string) (Result, error) {
	t.Helper()
	payload := map[string]string{"path": p}
	if name == ToolWrite {
		payload["content"] = content
	}
	raw, _ := json.Marshal(payload)
	calls := 0
	r := fixtureRunner(t, func() query.MessageStreamer {
		return streamFunc(func(c context.Context, q anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
			calls++
			if calls == 1 {
				return toolResult(name, string(raw)), nil
			}
			return respond(cb, textResult("Action recorded."))
		})
	})
	return r.Execute(context.Background(), req)
}

func TestPathEscapeAndProtectedNames(t *testing.T) {
	for _, p := range []string{
		"../outside", "/etc/passwd", "artifacts/../outside", "artifacts/./a", "artifacts//a", "artifacts/", "artifacts-sibling/secret",
		"artifacts/.hidden", "artifacts/.git/config", "artifacts/.env", "artifacts/credentials.json", "artifacts/secrets/token", "artifacts/key.pem",
		"artifacts/fixtures/trusted.txt", "artifacts/testdata/fixture.txt", "artifacts/policies/verify.json", "artifacts/.super-niuma/work-orders.db",
		"artifacts/work-orders.db-wal", "artifacts/token.key", "artifacts/ space", "artifacts/a\x00b", "artifacts/foo\\bar", "artifacts/../../.git/hooks/a",
	} {
		t.Run(p, func(t *testing.T) {
			req := fixtureRequest(t)
			got, err := toolRequest(t, req, ToolWrite, p, "payload")
			if !errors.Is(err, ErrAuthority) || len(got.Writes) != 0 {
				t.Fatalf("path=%q result=%+v err=%v", p, got, err)
			}
		})
	}
}

func TestReadAndWriteAuthorizationsAreIndependent(t *testing.T) {
	req := fixtureRequest(t)
	writeFixture(t, req.Workspace, "artifacts/private.txt", "never implicitly readable")
	if _, err := toolRequest(t, req, ToolRead, "artifacts/private.txt", ""); err != ErrAuthority {
		t.Fatal("write grant allowed read", err)
	}
	if _, err := toolRequest(t, req, ToolWrite, "inputs/private.txt", "no"); err != ErrAuthority {
		t.Fatal("read grant allowed write", err)
	}
	req.WritePaths = []string{"artifacts/exact.txt"}
	if _, err := toolRequest(t, req, ToolWrite, "artifacts/exact.txt.extra", "no"); err != ErrAuthority {
		t.Fatal("string prefix broadened file grant", err)
	}
	got, err := toolRequest(t, req, ToolWrite, "artifacts/exact.txt", "yes")
	if err != nil || len(got.Writes) != 1 {
		t.Fatalf("exact grant failed: %+v %v", got, err)
	}
}

func TestRejectSymlinksIntermediateAndLeaf(t *testing.T) {
	for _, leaf := range []bool{false, true} {
		for _, internal := range []bool{false, true} {
			for _, tool := range []string{ToolRead, ToolWrite} {
				name := tool + "/"
				if leaf {
					name += "leaf"
				} else {
					name += "directory"
				}
				if internal {
					name += "-internal"
				}
				t.Run(name, func(t *testing.T) {
					req := fixtureRequest(t)
					req.ReadPaths = []string{"artifacts"}
					outside := fixtureDir(t)
					target := outside
					if internal {
						target = filepath.Join(req.Workspace, "protected")
					}
					writeFixture(t, target, "guard.txt", "unchanged")
					if err := os.MkdirAll(filepath.Join(req.Workspace, "artifacts"), 0700); err != nil {
						t.Fatal(err)
					}
					linkTarget, linkPath := target, filepath.Join(req.Workspace, "artifacts/link")
					p := "artifacts/link/guard.txt"
					if leaf {
						linkTarget = filepath.Join(target, "guard.txt")
						p = "artifacts/link"
					}
					if internal {
						rel, err := filepath.Rel(filepath.Dir(linkPath), linkTarget)
						if err != nil {
							t.Fatal(err)
						}
						linkTarget = rel
					}
					if err := os.Symlink(linkTarget, linkPath); err != nil {
						t.Fatal(err)
					}
					got, err := toolRequest(t, req, tool, p, "overwrite")
					if err != ErrAuthority || len(got.Writes) != 0 {
						t.Fatalf("result=%+v err=%v", got, err)
					}
					data, _ := os.ReadFile(filepath.Join(target, "guard.txt"))
					if string(data) != "unchanged" {
						t.Fatal("symlink followed")
					}
				})
			}
		}
	}
}

func TestRejectHardlinksAndSpecialFilesWithoutBlocking(t *testing.T) {
	for _, kind := range []string{"hardlink", "fifo"} {
		for _, tool := range []string{ToolRead, ToolWrite} {
			t.Run(kind+tool, func(t *testing.T) {
				req := fixtureRequest(t)
				req.ReadPaths = []string{"artifacts"}
				writeFixture(t, req.Workspace, "guard.txt", "unchanged")
				if err := os.Mkdir(filepath.Join(req.Workspace, "artifacts"), 0700); err != nil {
					t.Fatal(err)
				}
				p := filepath.Join(req.Workspace, "artifacts/target")
				if kind == "hardlink" {
					if err := os.Link(filepath.Join(req.Workspace, "guard.txt"), p); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := unix.Mkfifo(p, 0600); err != nil {
						t.Fatal(err)
					}
				}
				got, err := toolRequest(t, req, tool, "artifacts/target", "overwrite")
				if err != ErrAuthority || len(got.Writes) != 0 {
					t.Fatalf("got=%+v err=%v", got, err)
				}
				data, _ := os.ReadFile(filepath.Join(req.Workspace, "guard.txt"))
				if string(data) != "unchanged" {
					t.Fatal("alias modified")
				}
			})
		}
	}
}

func TestSuperStateCannotBeGrantedEvenWithInnocentName(t *testing.T) {
	req := fixtureRequest(t)
	stateDir := filepath.Join(req.Workspace, "artifacts/runtime")
	t.Setenv(superstate.StateDirectoryEnv, stateDir)
	got, err := toolRequest(t, req, ToolWrite, "artifacts/runtime/innocent.txt", "state-corruption")
	if err != ErrAuthority || len(got.Writes) != 0 {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		t.Fatal(err)
	}
	req.Workspace = stateDir
	if _, err := toolRequest(t, req, ToolWrite, "artifacts/a", "bad"); err != ErrAuthority {
		t.Fatal("state root accepted", err)
	}
}

func TestWorkspaceRootMustNotBeSymlink(t *testing.T) {
	req := fixtureRequest(t)
	outer := fixtureDir(t)
	alias := filepath.Join(outer, "alias")
	if err := os.Symlink(req.Workspace, alias); err != nil {
		t.Fatal(err)
	}
	req.Workspace = alias
	if _, err := toolRequest(t, req, ToolWrite, "artifacts/a", "bad"); err != ErrAuthority {
		t.Fatal(err)
	}
}

func TestRootRemainsBoundAfterPathReplacement(t *testing.T) {
	req := fixtureRequest(t)
	writeFixture(t, req.Workspace, "inputs/a", "original")
	fs, err := openWorkspace(req)
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	moved := req.Workspace + "-moved"
	if err := os.Rename(req.Workspace, moved); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(moved) })
	writeFixture(t, req.Workspace, "inputs/a", "replacement")
	got, err := fs.read(context.Background(), "inputs/a", func(context.Context) error { return nil })
	if err != nil || got != "original" {
		t.Fatalf("bound root read=%q err=%v", got, err)
	}
}

func TestDirectoryListingIsImmediateFilteredAndBounded(t *testing.T) {
	req := fixtureRequest(t)
	for _, f := range []string{"inputs/a.txt", "inputs/sub/b.txt", "inputs/.env", "inputs/credentials.json", "inputs/fixtures/hidden.txt"} {
		writeFixture(t, req.Workspace, f, "value")
	}
	if err := os.Symlink("a.txt", filepath.Join(req.Workspace, "inputs/link")); err != nil {
		t.Fatal(err)
	}
	fs, err := openWorkspace(req)
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	got, err := fs.read(context.Background(), "inputs", func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "inputs/a.txt") || !strings.Contains(got, "inputs/sub") || strings.Contains(got, "b.txt") || strings.Contains(got, ".env") || strings.Contains(got, "credentials") || strings.Contains(got, "link") || strings.Contains(got, "fixtures") {
		t.Fatal(got)
	}
	for i := 0; i < maxDirectoryEntries; i++ {
		writeFixture(t, req.Workspace, "inputs/entry"+strings.Repeat("x", i/100)+string(rune('一'+i)), "x")
	}
	if _, err := fs.read(context.Background(), "inputs", func(context.Context) error { return nil }); err != ErrFailed {
		t.Fatal("oversized listing silently truncated", err)
	}
}

func TestFileByteLimitsAndUTF8(t *testing.T) {
	for _, tc := range []struct {
		name, content string
		size          int
		tool          string
		want          error
	}{
		{"max_write", "界", MaxFileBytes / 3, ToolWrite, nil},
		{"oversized_write", "x", MaxFileBytes + 1, ToolWrite, ErrInvalid},
		{"oversized_read", "x", MaxFileBytes + 1, ToolRead, ErrAuthority},
		{"invalid_utf8", "\xff", 1, ToolRead, ErrFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := fixtureRequest(t)
			req.ReadPaths = []string{"artifacts"}
			content := strings.Repeat(tc.content, tc.size)
			if tc.tool == ToolRead {
				writeFixture(t, req.Workspace, "artifacts/a", content)
			}
			got, err := toolRequest(t, req, tc.tool, "artifacts/a", content)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err=%v", err)
			}
			if err != nil && len(got.Writes) > 0 {
				t.Fatal("failed write receipt")
			}
		})
	}
}

func TestCurrentReceiptSupersedesIntermediateWrite(t *testing.T) {
	req := fixtureRequest(t)
	n := 0
	r := fixtureRunner(t, func() query.MessageStreamer {
		return streamFunc(func(c context.Context, q anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
			n++
			if n < 3 {
				payload, _ := json.Marshal(map[string]string{"path": "artifacts/a", "content": strings.Repeat("x", n)})
				out := toolResult(ToolWrite, string(payload))
				out.Message.Content[0].ID = string(rune('a' + n))
				return out, nil
			}
			return respond(cb, textResult("Written."))
		})
	})
	got, err := r.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("xx"))
	if len(got.Writes) != 1 || got.Writes[0].SHA256 != hex.EncodeToString(digest[:]) || got.Writes[0].Bytes != 2 {
		t.Fatalf("receipts=%+v", got.Writes)
	}
	entries, err := os.ReadDir(filepath.Join(req.Workspace, "artifacts"))
	if err != nil || len(entries) != 1 {
		t.Fatal("temporary write artifacts not cleaned")
	}
}

func TestRevokeBeforeRenamePreservesExistingFileAndCleansTemp(t *testing.T) {
	req := fixtureRequest(t)
	writeFixture(t, req.Workspace, "artifacts/a", "original")
	req.CheckAuthority = func(context.Context) error {
		entries, _ := os.ReadDir(filepath.Join(req.Workspace, "artifacts"))
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".super-write-") {
				return ErrAuthority
			}
		}
		return nil
	}
	got, err := toolRequest(t, req, ToolWrite, "artifacts/a", "replacement")
	if err != ErrAuthority || len(got.Writes) != 0 {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	data, _ := os.ReadFile(filepath.Join(req.Workspace, "artifacts/a"))
	if string(data) != "original" {
		t.Fatal("revoked rename executed")
	}
	entries, _ := os.ReadDir(filepath.Join(req.Workspace, "artifacts"))
	if len(entries) != 1 {
		t.Fatal("temporary files left")
	}
}
