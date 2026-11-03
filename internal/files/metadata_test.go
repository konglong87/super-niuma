package files

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestApplyMetadataRestoresMtime(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(f, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	want := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	if msgs := ApplyMetadata(f, Metadata{ModTimeKnown: true, ModTimeUnixNano: want.UnixNano()}, false); len(msgs) > 0 {
		t.Fatalf("unexpected degradation: %v", msgs)
	}
	info, err := os.Stat(f)
	if err != nil {
		t.Fatal(err)
	}
	if info.ModTime().Unix() != want.Unix() {
		t.Fatalf("mtime = %v, want %v", info.ModTime().Unix(), want.Unix())
	}
}

func xattrTestName() string {
	if runtime.GOOS == "linux" {
		return "user.rewind_test"
	}
	return "rewind_test"
}

func TestXattrCaptureAndRestoreRoundTrip(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("xattrs only supported on linux/darwin here")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	if err := os.WriteFile(src, []byte("src"), 0644); err != nil {
		t.Fatal(err)
	}
	name := xattrTestName()
	value := base64.StdEncoding.EncodeToString([]byte("hello-xattr"))
	if msgs := applyXattrs(src, map[string]string{name: value}); len(msgs) > 0 {
		t.Skipf("filesystem does not support xattrs: %v", msgs)
	}
	captured, ok := captureXattrs(src)
	if !ok {
		t.Skip("xattr listing unsupported on this filesystem")
	}
	if captured[name] != value {
		t.Fatalf("captured xattr = %q, want %q", captured[name], value)
	}
	// Restore onto a fresh file through the public ApplyMetadata path.
	dst := filepath.Join(dir, "dst.txt")
	if err := os.WriteFile(dst, []byte("dst"), 0644); err != nil {
		t.Fatal(err)
	}
	if msgs := ApplyMetadata(dst, Metadata{Xattrs: captured, XattrsKnown: true}, false); len(msgs) > 0 {
		t.Fatalf("restore degraded: %v", msgs)
	}
	got, _ := captureXattrs(dst)
	if got[name] != value {
		t.Fatalf("restored xattr = %q, want %q", got[name], value)
	}
}

func TestApplyOwnerReportsDegradationWithoutPrivilege(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX ownership on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root can change ownership; degradation path not exercised")
	}
	dir := t.TempDir()
	f := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(f, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	// Chown to root as a non-root process must fail and be reported, never faked.
	msg := applyOwner(f, 0, 0)
	if msg == "" {
		t.Fatal("expected an observable degradation message for unprivileged chown")
	}
}

func TestCaptureMetadataCapabilityFlags(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(f, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(f)
	if err != nil {
		t.Fatal(err)
	}
	m := CaptureMetadata(f, info, false)
	if !m.ModTimeKnown {
		t.Errorf("mtime should be captured for a regular file")
	}
	if runtime.GOOS != "windows" && !m.OwnerKnown {
		t.Errorf("owner should be captured on unix")
	}
}
