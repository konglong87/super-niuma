package native

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestIPCProcessChild(t *testing.T) {
	args := os.Args
	at := -1
	for i, arg := range args {
		if arg == "ipc-process-child" {
			at = i
			break
		}
	}
	if at < 0 {
		return
	}
	defer syscall.Exit(0) // bypass race runtime one-second exit delay in fixture child
	mode := args[at+1]
	codec := NewCodec(0)
	if mode == "no-read" {
		time.Sleep(time.Hour)
		return
	}
	var stored *Envelope
	for {
		req, err := codec.ReadFrame(os.Stdin)
		if err != nil {
			return
		}
		switch mode {
		case "no-response":
			continue
		case "exit":
			return
		case "malformed":
			_, _ = os.Stdout.Write([]byte{0, 0, 0, 6, 'S', 'E', 'C', 'R', 'E', 'T'})
			continue
		case "reorder":
			if stored == nil {
				stored = &req
				continue
			}
			_ = codec.WriteFrame(os.Stdout, req)
			_ = codec.WriteFrame(os.Stdout, *stored)
			stored = nil
			continue
		}
		_ = codec.WriteFrame(os.Stdout, req)
		if mode == "final" {
			return
		}
	}
}
func startTestProcess(t *testing.T, mode string) *Process {
	t.Helper()
	p, err := StartProcess(context.Background(), os.Args[0], []string{"-test.run=^TestIPCProcessChild$", "--", "ipc-process-child", mode}, []string{}, NewCodec(0))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		p.Abort(errors.New("test cleanup"))
		_ = p.Wait(context.Background())
		select {
		case <-p.waitDone:
		case <-time.After(time.Second):
			t.Error("child not reaped")
		}
	})
	return p
}
func requestFor(id string) Envelope {
	req := testEnvelope()
	req.RequestID = id
	req.Deadline = time.Now().Add(time.Second)
	return req
}
func waitForProcess(t *testing.T, p *Process) {
	t.Helper()
	select {
	case <-p.readerDone:
	case <-time.After(time.Second):
		t.Fatal("reader leaked")
	}
	select {
	case <-p.waitDone:
	case <-time.After(time.Second):
		t.Fatal("Wait owner leaked")
	}
}

func TestProcessCancellationClosesReaderAndReaps(t *testing.T) {
	p := startTestProcess(t, "no-response")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := p.Call(ctx, requestFor("request-1"))
	var call *CallError
	if !errors.Is(err, context.DeadlineExceeded) || !errors.As(err, &call) || !call.MayHaveRun {
		t.Fatalf("err=%v", err)
	}
	waitForProcess(t, p)
	_, err = p.Call(context.Background(), requestFor("request-2"))
	if !errors.As(err, &call) || call.MayHaveRun {
		t.Fatalf("dead helper accepted request: %v", err)
	}
}

type notifyingWriter struct {
	io.WriteCloser
	started chan struct{}
	once    sync.Once
}

func (w *notifyingWriter) Write(data []byte) (int, error) {
	w.once.Do(func() { close(w.started) })
	return w.WriteCloser.Write(data)
}

func TestProcessBlockedWriteIsCancelled(t *testing.T) {
	p := startTestProcess(t, "no-read")
	req := requestFor("write")
	req.Payload, _ = json.Marshal(map[string]string{"text": strings.Repeat("x", 900000)})
	writer := &notifyingWriter{WriteCloser: p.stdin, started: make(chan struct{})}
	p.stdin = writer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := p.Call(ctx, req); result <- err }()
	// Cancel only after dispatch starts. A deadline during JSON encoding is
	// correctly rejected without poisoning the helper, not a blocked write.
	select {
	case <-writer.started:
	case <-time.After(5 * time.Second):
		t.Fatal("write never dispatched")
	}
	cancel()
	select {
	case err := <-result:
		var call *CallError
		if !errors.Is(err, context.Canceled) || !errors.As(err, &call) || !call.MayHaveRun {
			t.Fatalf("blocked write not cancelled after dispatch: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked write cancellation timed out")
	}
	waitForProcess(t, p)
}

func TestProcessRoutesOutOfOrderResponses(t *testing.T) {
	p := startTestProcess(t, "reorder")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for _, id := range []string{"one", "two"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			response, err := p.Call(ctx, requestFor(id))
			if err != nil || response.RequestID != id {
				t.Errorf("route %s: %v", id, err)
			}
		}(id)
	}
	wg.Wait()
}

func TestProcessFinalResponseIsDrained(t *testing.T) {
	p := startTestProcess(t, "final")
	response, err := p.Call(context.Background(), requestFor("last"))
	if err != nil || response.RequestID != "last" {
		t.Fatalf("lost final response: %v", err)
	}
	waitForProcess(t, p)
}

func TestProcessRejectsBeforeDispatch(t *testing.T) {
	p := startTestProcess(t, "echo")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := p.Call(ctx, requestFor("cancelled"))
	var call *CallError
	if !errors.As(err, &call) || call.MayHaveRun {
		t.Fatalf("cancelled call classification: %v", err)
	}
	req := requestFor("oversized")
	req.Payload, _ = json.Marshal(strings.Repeat("x", int(DefaultMaxFrameSize)))
	_, err = p.Call(context.Background(), req)
	if !errors.As(err, &call) || call.MayHaveRun {
		t.Fatalf("oversized call classification: %v", err)
	}
	if _, err = p.Call(context.Background(), requestFor("still-live")); err != nil {
		t.Fatal("pre-dispatch rejection poisoned stream", err)
	}
}

func TestProcessParentCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p, err := StartProcess(ctx, os.Args[0], []string{"-test.run=^TestIPCProcessChild$", "--", "ipc-process-child", "no-response"}, []string{}, NewCodec(0))
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	waitForProcess(t, p)
}
func TestProcessWaitBoundedAndMalformedRedacted(t *testing.T) {
	p := startTestProcess(t, "no-response")
	start := time.Now()
	if err := p.Wait(context.Background()); err == nil {
		t.Fatal("Wait should report timeout")
	}
	if time.Since(start) > time.Second {
		t.Fatal("Wait unbounded")
	}
	waitForProcess(t, p)
	p = startTestProcess(t, "malformed")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := p.Call(ctx, requestFor("bad"))
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("malformed response leaked content: %v", err)
	}
	waitForProcess(t, p)
}
