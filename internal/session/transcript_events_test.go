package session

import (
	"encoding/json"
	"sync"
	"testing"
)

func TestRecorderAppendEventAssignsStableSequence(t *testing.T) {
	store := Store{TranscriptProjectsRoot: t.TempDir(), SchemaV2: true}
	const id = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	recorder, created, err := store.OpenOrCreateRecorder("/workspace/events", id)
	if err != nil || !created {
		t.Fatalf("OpenOrCreateRecorder created=%v err=%v", created, err)
	}
	defer recorder.Close()

	first, err := recorder.AppendEvent(TranscriptEvent{TaskID: 7, EventType: "message", PayloadJSON: `{"content":"hello"}`, Source: "desktop", Surface: "wails", Scope: "tenant"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := recorder.AppendEvent(TranscriptEvent{TaskID: 7, EventType: "text_delta", PayloadJSON: `{"content":"world"}`, Source: "desktop", Surface: "wails", Scope: "tenant"})
	if err != nil {
		t.Fatal(err)
	}
	if first != 1 || second != 2 {
		t.Fatalf("sequences = %d, %d; want 1, 2", first, second)
	}
	summary, ok, err := store.Find(id)
	if err != nil || !ok {
		t.Fatalf("Find() ok=%v err=%v", ok, err)
	}
	events, err := LoadEvents(summary.Path, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Sequence != 1 || events[1].EventType != "text_delta" || events[1].Source != "desktop" {
		t.Fatalf("events = %#v", events)
	}
}

func TestRecorderAppendEventSerializesConcurrentWriters(t *testing.T) {
	store := Store{TranscriptProjectsRoot: t.TempDir(), SchemaV2: true}
	const id = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	first, created, err := store.OpenOrCreateRecorder("/workspace/events", id)
	if err != nil || !created {
		t.Fatalf("first recorder created=%v err=%v", created, err)
	}
	second, created, err := store.OpenOrCreateRecorder("/workspace/events", id)
	if err != nil || created {
		t.Fatalf("second recorder created=%v err=%v", created, err)
	}
	defer first.Close()
	defer second.Close()

	const count = 40
	errs := make(chan error, count*2)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		for _, recorder := range []*Recorder{first, second} {
			wg.Add(1)
			go func(recorder *Recorder) {
				defer wg.Done()
				_, err := recorder.AppendEvent(TranscriptEvent{TaskID: 11, EventType: "text_delta", PayloadJSON: `{"content":"x"}`})
				errs <- err
			}(recorder)
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	summary, ok, err := store.Find(id)
	if err != nil || !ok {
		t.Fatalf("Find() ok=%v err=%v", ok, err)
	}
	events, err := LoadEvents(summary.Path, 0, count*2+1)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != count*2 {
		t.Fatalf("event count = %d, want %d", len(events), count*2)
	}
	for index, event := range events {
		if event.Sequence != uint64(index+1) {
			t.Fatalf("event[%d] sequence=%d, want %d", index, event.Sequence, index+1)
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
			t.Fatalf("event[%d] payload: %v", index, err)
		}
	}
}
