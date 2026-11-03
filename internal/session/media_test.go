package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPersistMediaWritesSidecarAndKeepsPayloadOutOfTheRef(t *testing.T) {
	dir := t.TempDir()
	transcript := filepath.Join(dir, "session.jsonl")
	payload := strings.Repeat("QUJD", 5000)

	ref, err := PersistMedia("sess-1", transcript, "image/png", payload)
	if err != nil {
		t.Fatal(err)
	}
	if ref.MediaType != "image/png" || ref.Base64Bytes != len(payload) {
		t.Fatalf("ref = %+v", ref)
	}
	// 引用里绝不能带载荷本身：transcript 是逐行 append 的会话日志，
	// 一张图内联进去就是几 MB 一行（TODO-080）。
	encoded, err := json.Marshal(ref)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), payload) {
		t.Fatal("media ref carries the base64 payload")
	}
	if len(encoded) > 512 {
		t.Fatalf("media ref is too big to sit in a transcript line: %d bytes", len(encoded))
	}
	if got := filepath.Dir(ref.Path); got != filepath.Join(dir, "sess-1", "media") {
		t.Fatalf("media dir = %s", got)
	}

	loaded, err := LoadMedia(ref)
	if err != nil {
		t.Fatal(err)
	}
	if loaded != payload {
		t.Fatalf("loaded %d chars, want %d", len(loaded), len(payload))
	}
}

// 同一张图记两次只落一个文件：文件名按内容哈希，天然去重，也不需要计数器。
func TestPersistMediaIsContentAddressed(t *testing.T) {
	dir := t.TempDir()
	transcript := filepath.Join(dir, "session.jsonl")

	first, err := PersistMedia("sess-1", transcript, "image/png", "QUJD")
	if err != nil {
		t.Fatal(err)
	}
	second, err := PersistMedia("sess-1", transcript, "image/png", "QUJD")
	if err != nil {
		t.Fatal(err)
	}
	if first.Path != second.Path || first.SHA256 != second.SHA256 {
		t.Fatalf("first = %+v second = %+v", first, second)
	}
	entries, err := os.ReadDir(filepath.Dir(first.Path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("wrote %d files, want 1", len(entries))
	}
}

func TestPersistMediaRequiresATranscript(t *testing.T) {
	if _, err := PersistMedia("", "", "image/png", "QUJD"); err == nil {
		t.Fatal("expected error without a session transcript")
	}
}

// 侧车文件被删掉（或整个目录被清理）时，读取要报错而不是返回空串 ——
// 调用方靠这个错误决定降级成占位文本。
func TestLoadMediaFailsWhenSidecarIsGone(t *testing.T) {
	dir := t.TempDir()
	transcript := filepath.Join(dir, "session.jsonl")
	ref, err := PersistMedia("sess-1", transcript, "image/png", "QUJD")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(ref.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadMedia(ref); err == nil {
		t.Fatal("expected error for a missing sidecar")
	}
}

// 载荷被改过就不能当原图用：哈希是文件名，读回来必须和它对得上。
func TestLoadMediaRejectsATamperedSidecar(t *testing.T) {
	dir := t.TempDir()
	transcript := filepath.Join(dir, "session.jsonl")
	ref, err := PersistMedia("sess-1", transcript, "image/png", "QUJD")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ref.Path, []byte("WFla"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadMedia(ref); err == nil {
		t.Fatal("expected a checksum mismatch")
	}
}

// image 必须被认成 niuma v1 的合法类型。否则一条 image 行会让
// detectTranscriptLineFormat 返回 Unknown，combineTranscriptFormat 把整个文件
// 判成 Mixed，ValidateResumeFormat 直接拒绝 resume —— 「图片丢了」会变成
// 「会话打不开了」。
func TestTranscriptWithImageEntryStaysResumableV1(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	lines := []string{
		`{"type":"session","timestamp":"2026-07-26T00:00:00Z"}`,
		`{"type":"message","role":"user","content":"screenshot please","timestamp":"2026-07-26T00:00:01Z"}`,
		`{"type":"image","content":"[image content attached below]","timestamp":"2026-07-26T00:00:02Z","metadata":{"media_type":"image/png","path":"/tmp/x.b64","sha256":"abc","base64_bytes":4}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	entries, format, err := LoadWithFormat(path)
	if err != nil {
		t.Fatal(err)
	}
	if format != TranscriptFormatGolangCCV1 {
		t.Fatalf("format = %s, want %s", format, TranscriptFormatGolangCCV1)
	}
	if err := ValidateResumeFormat(format); err != nil {
		t.Fatalf("image entry made the transcript unresumable: %v", err)
	}
	if len(entries) != 3 || entries[2].Type != "image" {
		t.Fatalf("entries = %+v", entries)
	}
}
