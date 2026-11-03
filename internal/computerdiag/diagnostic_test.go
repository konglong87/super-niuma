package computerdiag

import (
	"bufio"
	"encoding/json"
	"os"
	"testing"
)

func TestAppendWritesStructuredEventsWithoutOverwriting(t *testing.T) {
	path := t.TempDir() + "/diagnostic.log"
	Append(path, map[string]any{"layer": "go", "phase": "first"})
	Append(path, map[string]any{"layer": "go", "phase": "second"})

	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var events []map[string]any
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var event map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("event count = %d, want 2", len(events))
	}
	if events[0]["phase"] != "first" || events[1]["phase"] != "second" {
		t.Fatalf("events = %#v", events)
	}
	for _, event := range events {
		if event["schema_version"] != SchemaVersion || event["timestamp"] == "" {
			t.Fatalf("missing envelope metadata: %#v", event)
		}
	}
}
