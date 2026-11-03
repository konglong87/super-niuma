package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestAppendEntryToPathSerializesConcurrentWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte("{\"id\":\"root\",\"type\":\"message\",\"role\":\"user\",\"content\":\"start\"}\n"), 0600); err != nil {
		t.Fatal(err)
	}

	const writers = 32
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			errs <- AppendEntryToPath(path, Entry{
				ID:      NewEntryID(),
				Type:    "message",
				Role:    "assistant",
				Content: string(rune('a' + index)),
			})
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	entries, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != writers+1 {
		t.Fatalf("entries = %d, want %d", len(entries), writers+1)
	}
	for _, entry := range entries {
		data, err := json.Marshal(entry)
		if err != nil || !json.Valid(data) {
			t.Fatalf("entry is not valid JSON: %+v err=%v", entry, err)
		}
	}
}

func TestAppendEntryToPathIfCurrentRejectsStaleHead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(
		"{\"id\":\"user-1\",\"type\":\"message\",\"role\":\"user\",\"content\":\"start\"}\n"+
			"{\"id\":\"assistant-1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":\"done\"}\n",
	), 0600); err != nil {
		t.Fatal(err)
	}
	if err := AppendEntryToPath(path, Entry{ID: "user-2", Type: "message", Role: "user", Content: "next"}); err != nil {
		t.Fatal(err)
	}

	appended, err := AppendEntryToPathIfCurrent(path, "assistant-1", Entry{Type: "recap_summary", Content: "stale"})
	if err != nil {
		t.Fatal(err)
	}
	if appended {
		t.Fatal("stale entry was appended")
	}
	entries, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Type == "recap_summary" {
			t.Fatalf("stale recap found: %+v", entry)
		}
	}
}
