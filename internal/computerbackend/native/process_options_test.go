package native

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestProcessExtraFilesChild(t *testing.T) {
	if !strings.Contains(strings.Join(os.Args, " "), "process-extra-files-child") {
		return
	}
	defer syscall.Exit(0)
	requests := os.NewFile(3, "requests")
	responses := os.NewFile(4, "responses")
	if _, err := requests.Write([]byte("request")); err != nil {
		os.Exit(1)
	}
	var reply [8]byte
	if _, err := io.ReadFull(responses, reply[:]); err != nil || string(reply[:]) != "response" {
		os.Exit(2)
	}
}

func TestProcessExtraFilesAndAbortHook(t *testing.T) {
	requests, childRequests, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	childResponses, responses, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range []*os.File{requests, childRequests, responses, childResponses} {
		t.Cleanup(func() { file.Close() })
	}
	var count atomic.Int32
	p, err := StartProcess(context.Background(), os.Args[0], []string{"-test.run=^TestProcessExtraFilesChild$", "--", "process-extra-files-child"}, nil, NewCodec(0),
		WithExtraFiles(childRequests, childResponses), WithAbortHook(func() { count.Add(1) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Abort(errors.New("test cleanup")); _ = p.Wait(context.Background()) })
	childRequests.Close()
	childResponses.Close()
	var request [7]byte
	if _, err := io.ReadFull(requests, request[:]); err != nil || string(request[:]) != "request" {
		t.Fatalf("FD3: %q %v", request, err)
	}
	if _, err := responses.Write([]byte("response")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.Exited():
	case <-time.After(time.Second):
		t.Fatal("exit not observed")
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); p.Abort(errors.New("again")) }()
	}
	wg.Wait()
	if count.Load() != 1 {
		t.Fatalf("cleanup calls %d", count.Load())
	}
}

func TestProcessAbortHookPrecedesCallFailure(t *testing.T) {
	var cleaned atomic.Bool
	p, err := StartProcess(context.Background(), os.Args[0], []string{"-test.run=^TestIPCProcessChild$", "--", "ipc-process-child", "no-response"}, nil, NewCodec(0), WithAbortHook(func() { cleaned.Store(true) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Abort(errors.New("test cleanup")); _ = p.Wait(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err = p.Call(ctx, requestFor("abort"))
	if err == nil || !cleaned.Load() {
		t.Fatal("call failed before independent cleanup")
	}
}
