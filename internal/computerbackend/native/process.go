package native

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

const ShutdownGrace = 500 * time.Millisecond

// CallError distinguishes rejection before dispatch from an ambiguous transport
// failure. Once writing begins, input may have happened; never replay the call.
type CallError struct {
	Cause      error
	MayHaveRun bool
}

func (e *CallError) Error() string { return e.Cause.Error() }
func (e *CallError) Unwrap() error { return e.Cause }

type reply struct {
	envelope Envelope
	err      error
}
type pendingCall struct {
	request Envelope
	result  chan reply
}

// Process has exactly one reader and one Wait owner. Calls may overlap so Stop
// is not queued behind Execute. Any stream failure poisons the whole process.
type Process struct {
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	stdout     io.ReadCloser
	codec      Codec
	writer     chan struct{}
	mu         sync.Mutex
	pending    map[string]pendingCall
	failure    error
	done       chan struct{}
	readerDone chan struct{}
	waitDone   chan struct{}
	abortOnce  sync.Once
	onAbort    func()
}

// ProcessOption configures optional, platform-neutral process resources.
type ProcessOption func(*processOptions)

type processOptions struct {
	extraFiles []*os.File
	onAbort    func()
}

// WithExtraFiles maps files in order to child descriptors 3, 4, ... . The
// caller retains ownership and must close its copies after StartProcess.
func WithExtraFiles(files ...*os.File) ProcessOption {
	return func(o *processOptions) { o.extraFiles = append([]*os.File(nil), files...) }
}

// WithAbortHook installs bounded local cleanup, run exactly once before
// pending calls are failed. It must not call Process methods or wait on IPC.
// Stream EOF, explicit Abort and context cancellation all invoke this hook.
func WithAbortHook(hook func()) ProcessOption {
	return func(o *processOptions) { o.onAbort = hook }
}

// Exited closes only after the unique cmd.Wait owner has reaped the child.
func (p *Process) Exited() <-chan struct{} { return p.waitDone }

func StartProcess(ctx context.Context, path string, args, env []string, codec Codec, options ...ProcessOption) (*Process, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var config processOptions
	for _, option := range options {
		if option != nil {
			option(&config)
		}
	}
	cmd := exec.Command(path, args...)
	cmd.ExtraFiles = config.extraFiles
	cmd.Env = append([]string{}, env...) // empty must not mean inherit
	cmd.Stderr = nil                     // os.DevNull: no sensitive logs or stderr-copy goroutine
	cmd.WaitDelay = ShutdownGrace
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		in.Close()
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		in.Close()
		out.Close()
		return nil, err
	}
	p := &Process{cmd: cmd, stdin: in, stdout: out, codec: codec, writer: make(chan struct{}, 1), pending: map[string]pendingCall{}, done: make(chan struct{}), readerDone: make(chan struct{}), waitDone: make(chan struct{}), onAbort: config.onAbort}
	go p.readLoop()
	// Wait only after the reader stops: StdoutPipe must be drained before Wait.
	go func() { <-p.readerDone; _ = cmd.Wait(); close(p.waitDone) }()
	go func() {
		select {
		case <-ctx.Done():
			p.Abort(ctx.Err())
		case <-p.done:
		}
	}()
	return p, nil
}

func (p *Process) readLoop() {
	defer close(p.readerDone)
	for {
		response, err := p.codec.ReadFrame(p.stdout)
		if err != nil {
			p.Abort(errors.New("helper response stream closed or invalid"))
			return
		}
		p.mu.Lock()
		call, ok := p.pending[response.RequestID]
		if ok {
			delete(p.pending, response.RequestID)
		}
		p.mu.Unlock()
		if !ok || response.SessionID != call.request.SessionID || response.ActionID != call.request.ActionID || response.Command != call.request.Command || !response.Deadline.Equal(call.request.Deadline) {
			err := errors.New("helper response binding mismatch")
			if ok {
				call.result <- reply{err: err}
			}
			p.Abort(err)
			return
		}
		call.result <- reply{envelope: response}
	}
}

// Abort unblocks writes and the single reader, fails pending calls, and kills
// the child. It is independent of the writer gate and never waits for an RPC.
func (p *Process) Abort(cause error) {
	p.abortOnce.Do(func() {
		if p.onAbort != nil {
			p.onAbort()
		}
		p.mu.Lock()
		p.failure = cause
		for id, call := range p.pending {
			call.result <- reply{err: cause}
			delete(p.pending, id)
		}
		close(p.done)
		p.mu.Unlock()
		_ = p.cmd.Process.Kill()
		_ = p.stdin.Close()
		_ = p.stdout.Close()
	})
}

func (p *Process) Call(ctx context.Context, request Envelope) (Envelope, error) {
	fail := func(err error, sent bool) (Envelope, error) {
		return Envelope{}, &CallError{Cause: err, MayHaveRun: sent}
	}
	// Encode before acquiring the writer: schema/size failures cannot send input.
	var frame bytes.Buffer
	if err := p.codec.WriteFrame(&frame, request); err != nil {
		return fail(errors.New("invalid helper request frame"), false)
	}
	if err := ctx.Err(); err != nil {
		return fail(err, false)
	}
	select {
	case p.writer <- struct{}{}:
	case <-ctx.Done():
		return fail(ctx.Err(), false)
	case <-p.done:
		return fail(errors.New("helper unavailable"), false)
	}
	if err := ctx.Err(); err != nil {
		<-p.writer
		return fail(err, false)
	}
	call := pendingCall{request: request, result: make(chan reply, 1)}
	p.mu.Lock()
	if p.failure != nil {
		err := p.failure
		p.mu.Unlock()
		<-p.writer
		return fail(err, false)
	}
	if _, exists := p.pending[request.RequestID]; exists {
		p.mu.Unlock()
		<-p.writer
		return fail(errors.New("duplicate request id"), false)
	}
	p.pending[request.RequestID] = call
	p.mu.Unlock()
	// A blocked pipe write is interrupted by Abort when the call is cancelled.
	writeDone := make(chan error, 1)
	go func() { err := writeAll(p.stdin, frame.Bytes()); <-p.writer; writeDone <- err }()
	select {
	case err := <-writeDone:
		if err != nil {
			p.Abort(errors.New("helper request write failed"))
			return fail(errors.New("helper request write failed"), true)
		}
	case <-ctx.Done():
		p.Abort(ctx.Err())
		<-writeDone
		return fail(ctx.Err(), true)
	}
	select {
	case response := <-call.result:
		if response.err != nil {
			return fail(response.err, true)
		}
		return response.envelope, nil
	case <-ctx.Done():
		p.Abort(ctx.Err())
		return fail(ctx.Err(), true)
	}
}

// Wait is bounded even when a malicious helper ignores shutdown/EOF. Abort
// closes the read pipe so the reader and Wait goroutines can both finish.
func (p *Process) Wait(ctx context.Context) error {
	timer := time.NewTimer(ShutdownGrace)
	defer timer.Stop()
	select {
	case <-p.waitDone:
		return nil
	case <-ctx.Done():
		p.Abort(ctx.Err())
		return ctx.Err()
	case <-timer.C:
		p.Abort(errors.New("helper shutdown timed out"))
		return errors.New("helper shutdown timed out")
	}
}
