package verification

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func workspace(t *testing.T) string {
	t.Helper()
	if !secureOpenSupported {
		t.Skip("secure file opening intentionally unsupported")
	}
	// macOS /var may itself be a symlink; production rejects such paths.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}
func writeArtifact(t *testing.T, root, name, content string) {
	t.Helper()
	target := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}
func digest(content string) string {
	hash := sha256.Sum256([]byte(content))
	return hex.EncodeToString(hash[:])
}
func expectFailedCheck(t *testing.T, root string, p Policy, code string) Report {
	t.Helper()
	r, err := Verify(context.Background(), root, p)
	if err != nil || r.Passed || len(r.Checks) != 1 || r.Checks[0].Passed || r.Checks[0].Code != code {
		t.Fatalf("report=%+v err=%v want=%s", r, err, code)
	}
	return r
}
func TestVerifyAllRuleKindsAndActualArtifactHashes(t *testing.T) {
	root := workspace(t)
	contents := map[string]string{"out/text.txt": "the result is approved", "out/report.json": `{"status":"ok","count":2,"nested":{"x":[1,true,null]}}`, "raw.bin": "\x00\x01\xff", "note": "x"}
	for name, content := range contents {
		writeArtifact(t, root, name, content)
	}
	p := Policy{Version: "v1", Rules: []Rule{{Path: "out/text.txt", Kind: KindTextContains, Expected: "approved"}, {Path: "out/report.json", Kind: KindJSONKeys, Keys: []string{"status", "count"}}, {Path: "raw.bin", Kind: KindSHA256, Expected: strings.ToUpper(digest(contents["raw.bin"]))}, {Path: "note", Kind: KindNonempty}}}
	before := time.Now().UnixMilli()
	r, err := Verify(context.Background(), root, p)
	if err != nil || !r.Passed || r.PolicyHash != PolicyHash(p) || r.VerifiedAtMS < before || r.VerifiedAtMS > time.Now().UnixMilli() {
		t.Fatalf("report=%+v err=%v", r, err)
	}
	if len(r.Artifacts) != len(p.Rules) || len(r.Checks) != len(p.Rules) {
		t.Fatal("incomplete report")
	}
	for _, a := range r.Artifacts {
		content := contents[a.Path]
		if a.SHA256 != digest(content) || a.Bytes != int64(len(content)) {
			t.Fatalf("untrusted artifact metadata: %+v", a)
		}
	}
	for _, c := range r.Checks {
		if !c.Passed || c.Code != CodeOK {
			t.Fatalf("check=%+v", c)
		}
	}
	for name, want := range contents {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || string(data) != want {
			t.Fatal("verifier mutated workspace")
		}
	}
}
func TestSemanticFailuresNeverPass(t *testing.T) {
	root := workspace(t)
	writeArtifact(t, root, "a", "wrong")
	r := expectFailedCheck(t, root, textPolicy("a", "approved"), CodeTextMissing)
	if len(r.Artifacts) != 1 || r.Artifacts[0].SHA256 != digest("wrong") {
		t.Fatal("failed semantic evidence missing")
	}
	expectFailedCheck(t, root, Policy{Version: "v1", Rules: []Rule{{Path: "a", Kind: KindSHA256, Expected: digest("approved")}}}, CodeHashMismatch)
	writeArtifact(t, root, "a", `{"unrelated":true}`)
	expectFailedCheck(t, root, Policy{Version: "v1", Rules: []Rule{{Path: "a", Kind: KindJSONKeys, Keys: []string{"required"}}}}, CodeKeysMissing)
	writeArtifact(t, root, "empty", "")
	p := textPolicy("a", "unrelated")
	p.Rules = append(p.Rules, Rule{Path: "empty", Kind: KindNonempty})
	r, err := Verify(context.Background(), root, p)
	if err != nil || r.Passed || r.Checks[1].Code != CodeEmpty {
		t.Fatalf("%+v %v", r, err)
	}
}
func TestReadFailuresAndBudgetBoundaries(t *testing.T) {
	root := workspace(t)
	writeArtifact(t, root, "a", "12345")
	p := textPolicy("a", "1")
	p.Rules[0].MaxBytes = 4
	r := expectFailedCheck(t, root, p, CodeTooLarge)
	if len(r.Artifacts) != 0 {
		t.Fatal("oversized artifact trusted")
	}
	p.Rules[0].MaxBytes = 5
	if r, err := Verify(context.Background(), root, p); err != nil || !r.Passed {
		t.Fatalf("exact limit: %+v %v", r, err)
	}
	expectFailedCheck(t, root, textPolicy("missing", "ok"), CodeMissing)
	if err := os.Mkdir(filepath.Join(root, "dir"), 0700); err != nil {
		t.Fatal(err)
	}
	expectFailedCheck(t, root, textPolicy("dir", "ok"), CodeNotRegular)
	expectFailedCheck(t, root, textPolicy("a/child", "ok"), CodeNotRegular)
	writeArtifact(t, root, "a", string([]byte{0xff, 'o', 'k'}))
	expectFailedCheck(t, root, textPolicy("a", "ok"), CodeTextMissing)
}
func TestSymlinksRejectedAtLeafParentAndWorkspace(t *testing.T) {
	root := workspace(t)
	writeArtifact(t, root, "real/a", "ok")
	outside := workspace(t)
	writeArtifact(t, outside, "secret", "ok")
	for _, tc := range []struct{ name, target string }{{"leaf", "real/a"}, {"parent", "real"}, {"escape", filepath.Join(outside, "secret")}, {"dangling", "absent"}} {
		if err := os.Symlink(tc.target, filepath.Join(root, tc.name)); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"leaf", "parent/a", "escape", "dangling"} {
		t.Run(path, func(t *testing.T) {
			r := expectFailedCheck(t, root, textPolicy(path, "ok"), CodeSymlink)
			if len(r.Artifacts) != 0 {
				t.Fatal("symlink bytes exposed")
			}
		})
	}
	for _, path := range []string{filepath.Join(root, "parent"), filepath.Join(root, "parent", "nested")} {
		r, err := Verify(context.Background(), path, textPolicy("a", "ok"))
		if !errors.Is(err, ErrInvalidWorkspace) || r.Passed {
			t.Fatalf("workspace symlink: %+v %v", r, err)
		}
	}
}
func TestInvalidPolicyAndWorkspaceFailBeforeReading(t *testing.T) {
	root := workspace(t)
	for _, p := range []Policy{{}, textPolicy("../escape", "ok"), textPolicy(".git/config", "ok"), textPolicy(".super-niuma/work-orders.db", "ok")} {
		r, err := Verify(context.Background(), root, p)
		if !errors.Is(err, ErrInvalidPolicy) || r.Passed || len(r.Artifacts) != 0 || len(r.Checks) != 0 || r.PolicyHash != "" {
			t.Fatalf("%+v %v", r, err)
		}
	}
	for _, path := range []string{"", ".", root + "/../", string(filepath.Separator), filepath.Join(root, ".super-niuma"), filepath.Join(root, "absent")} {
		r, err := Verify(context.Background(), path, textPolicy("a", "ok"))
		if !errors.Is(err, ErrInvalidWorkspace) || r.Passed {
			t.Fatalf("workspace %q: %+v %v", path, r, err)
		}
	}
}
func TestJSONRejectsTrailingDuplicateKeysAndExcessiveStructure(t *testing.T) {
	root := workspace(t)
	p := Policy{Version: "v1", Rules: []Rule{{Path: "a.json", Kind: KindJSONKeys, Keys: []string{"a"}}}}
	cases := []string{`{"a":1} {"a":2}`, `{"a":1} trailing`, `{"a":1,"a":2}`, `{"a":1,"\u0061":2}`, `{"a":{"x":1,"x":2}}`, `{"a":[{"x":1,"x":2}]}`, `["a"]`, `null`, ``, `{"a":1,}`, `{"a":NaN}`, `{"a":"` + string([]byte{0xff}) + `"}`, `{"a":` + strings.Repeat("[", MaxJSONDepth+1) + "0" + strings.Repeat("]", MaxJSONDepth+1) + "}", `{"a":[` + strings.Repeat("0,", MaxJSONTokens) + "0]}"}
	for i, content := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			writeArtifact(t, root, "a.json", content)
			expectFailedCheck(t, root, p, CodeJSONInvalid)
		})
	}
	for _, content := range []string{"{\"a\":1}\n \t", `{"a":1e9999}`, `{"a":{"x":1},"b":{"x":2}}`} {
		writeArtifact(t, root, "a.json", content)
		if r, err := Verify(context.Background(), root, p); err != nil || !r.Passed {
			t.Fatalf("valid JSON rejected: %+v %v", r, err)
		}
	}
}
func TestChangedContentAndForgedWorkerManifestCannotReuseSuccess(t *testing.T) {
	root := workspace(t)
	writeArtifact(t, root, "result.txt", "trusted result")
	p := Policy{Version: "v1", Rules: []Rule{{Path: "result.txt", Kind: KindSHA256, Expected: digest("trusted result")}}}
	first, err := Verify(context.Background(), root, p)
	if err != nil || !first.Passed {
		t.Fatalf("%+v %v", first, err)
	}
	writeArtifact(t, root, "manifest.json", `{"passed":true,"artifacts":[{"path":"result.txt","sha256":"`+first.Artifacts[0].SHA256+`"}]}`)
	writeArtifact(t, root, "result.txt", "forged result")
	second := expectFailedCheck(t, root, p, CodeHashMismatch)
	if second.PolicyHash != first.PolicyHash || second.Artifacts[0].SHA256 == first.Artifacts[0].SHA256 || second.Artifacts[0].SHA256 != digest("forged result") {
		t.Fatal("cached/worker-provided hash trusted")
	}
	if err := os.Remove(filepath.Join(root, "result.txt")); err != nil {
		t.Fatal(err)
	}
	expectFailedCheck(t, root, p, CodeMissing)
}
func TestCancellationAndContextReader(t *testing.T) {
	root := workspace(t)
	writeArtifact(t, root, "a", "ok")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r, err := Verify(ctx, root, textPolicy("a", "ok"))
	if !errors.Is(err, context.Canceled) || r.Passed || len(r.Artifacts) != 0 {
		t.Fatalf("%+v %v", r, err)
	}
	ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if r, err := Verify(ctx, root, textPolicy("a", "ok")); !errors.Is(err, context.DeadlineExceeded) || r.Passed {
		t.Fatalf("%+v %v", r, err)
	}
	if r, err := Verify(nil, root, textPolicy("a", "ok")); err == nil || r.Passed {
		t.Fatal("nil context accepted")
	}
	ctx, cancel = context.WithCancel(context.Background())
	reader := &cancelReader{reader: bytes.NewReader(bytes.Repeat([]byte("a"), 128<<10)), cancel: cancel}
	if _, err := io.ReadAll(contextReader{ctx: ctx, reader: reader}); !errors.Is(err, context.Canceled) {
		t.Fatalf("mid-read cancellation=%v", err)
	}
}

type cancelReader struct {
	reader io.Reader
	cancel context.CancelFunc
}

func (r *cancelReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.cancel()
	return n, err
}
func TestParallelVerifyDoesNotMutatePolicyOrWorkspace(t *testing.T) {
	root := workspace(t)
	writeArtifact(t, root, "a.json", `{"b":2,"a":1}`)
	p := Policy{Version: "v1", Rules: []Rule{{Path: "a.json", Kind: KindJSONKeys, Keys: []string{"b", "a"}}}}
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := Verify(context.Background(), root, p)
			if err != nil || !r.Passed || r.PolicyHash != PolicyHash(p) {
				t.Errorf("%+v %v", r, err)
			}
		}()
	}
	wg.Wait()
	if !reflect.DeepEqual(p.Rules[0].Keys, []string{"b", "a"}) || p.Rules[0].MaxBytes != 0 {
		t.Fatal("mutated policy")
	}
}
