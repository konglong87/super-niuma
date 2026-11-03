package macos

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"sync"
	"time"

	cu "github.com/konglong87/super-niuma/internal/computeruse"
)

const (
	mouseBrokerArgument         = "--mouse-broker"
	mouseBrokerFrameLimit       = 4096 // includes the terminating newline
	mouseBrokerTokenBytes       = 32
	mouseBrokerMaxSequence      = math.MaxInt32
	mousePhaseDown              = "down"
	mousePhaseDrag              = "drag"
	mousePhaseUp                = "up"
	mouseBrokerInputUnavailable = "input_unavailable"
)

var errMouseBrokerUnavailable = errors.New("host mouse broker unavailable")

// Driver construction must not prompt for TCC or publish input. Prepare
// allocates both down and emergency up before Down (which only posts).
type mouseDriver interface {
	checkPermission() error
	buttonsHeld() bool
	prepare(cu.MouseButton, float64, float64) (mouseGesture, error)
}

type mouseGesture interface {
	down()
	drag(float64, float64) error
	up(float64, float64) error
	close()
}

type mouseBrokerRequest struct {
	Token    string         `json:"token"`
	Sequence int64          `json:"sequence"`
	Phase    string         `json:"phase"`
	Button   cu.MouseButton `json:"button"`
	X        float64        `json:"x"`
	Y        float64        `json:"y"`
}

type mouseBrokerResponse struct {
	Sequence  int64  `json:"sequence"`
	OK        bool   `json:"ok"`
	ErrorCode string `json:"error_code,omitempty"`
}

type mouseLease struct {
	token, sessionID, actionID                  string
	epoch                                       uint64
	ctx                                         context.Context
	deadline                                    time.Time
	button                                      cu.MouseButton
	last                                        mouseBrokerRequest
	revoked, ended, pressed, released, normalUp bool
	gesture                                     mouseGesture
	x, y                                        float64
	failure                                     error
}

type mouseLeaseResult struct {
	pressed, complete bool
	err               error
}

// A broker belongs to exactly one helper lifetime. All event publication and
// revocation share mu; neither IPC reads nor writes hold it. Once revoked or
// closed, queued child messages cannot publish another down/drag.
type mouseBroker struct {
	mu          sync.Mutex
	driver      mouseDriver
	batchDriver inputBatchDriver
	batch       *inputBatchLease
	lease       *mouseLease
	closed      bool
	failure     error
	requests    *os.File
	responses   *os.File
	done        chan struct{}
}

func newMouseBroker(driver mouseDriver, batches ...inputBatchDriver) (*mouseBroker, []*os.File, error) {
	requests, childRequests, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	childResponses, responses, err := os.Pipe()
	if err != nil {
		requests.Close()
		childRequests.Close()
		return nil, nil, err
	}
	b := &mouseBroker{driver: driver, requests: requests, responses: responses, done: make(chan struct{})}
	if len(batches) > 0 {
		b.batchDriver = batches[0]
	}
	go b.serve()
	return b, []*os.File{childRequests, childResponses}, nil
}

func (b *mouseBroker) authorize(ctx context.Context, action cu.Action, epoch uint64, timeout time.Duration) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || b.failure != nil || (b.lease != nil && !b.lease.ended) || (b.batch != nil && !b.batch.ended) {
		return "", errMouseBrokerUnavailable
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := b.driver.checkPermission(); err != nil {
		return "", err
	}
	if b.driver.buttonsHeld() {
		return "", errors.New("mouse button already held")
	}
	button := cu.MouseButton(action.Button)
	if button == "" {
		button = cu.MouseButtonLeft
	}
	if button != cu.MouseButtonLeft && button != cu.MouseButtonRight {
		return "", errors.New("invalid drag button")
	}
	var entropy [mouseBrokerTokenBytes]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", err
	}
	deadline := time.Now().Add(timeout)
	if callerDeadline, ok := ctx.Deadline(); ok && callerDeadline.Before(deadline) {
		deadline = callerDeadline
	}
	token := hex.EncodeToString(entropy[:])
	b.lease = &mouseLease{token: token, sessionID: action.SessionID, actionID: action.ID, epoch: epoch, ctx: ctx, deadline: deadline, button: button}
	return token, nil
}

// revoke only removes authority for new down/drag; up remains available.
func (b *mouseBroker) revoke() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.lease != nil {
		b.lease.revoked = true
	}
	if b.batch != nil {
		b.batch.revoked = true
	}
}

func (b *mouseBroker) finish(token string) mouseLeaseResult {
	b.mu.Lock()
	defer b.mu.Unlock()
	l := b.lease
	if l == nil || l.token != token {
		return mouseLeaseResult{err: errMouseBrokerUnavailable}
	}
	l.revoked, l.ended = true, true
	b.releaseLocked(l, l.x, l.y, false)
	return mouseLeaseResult{pressed: l.pressed, complete: l.pressed && l.released && l.normalUp && l.failure == nil, err: l.failure}
}

// releaseLocked consumes the preallocated up once, including a failed attempt.
// Failures poison future leases; cleanup cannot certify delivery or replay up.
func (b *mouseBroker) releaseLocked(l *mouseLease, x, y float64, normal bool) {
	if l.gesture == nil {
		return
	}
	gesture := l.gesture
	l.gesture = nil
	l.released, l.normalUp = true, normal
	if err := gesture.up(x, y); err != nil {
		l.failure, b.failure = err, err
	}
	gesture.close()
}

func (b *mouseBroker) handle(r mouseBrokerRequest) mouseBrokerResponse {
	response := mouseBrokerResponse{Sequence: r.Sequence, ErrorCode: mouseBrokerInputUnavailable}
	b.mu.Lock()
	defer b.mu.Unlock()
	l := b.lease
	if b.closed || l == nil || r.Token != l.token || r.Button != l.button ||
		r.Sequence <= 0 || r.Sequence > mouseBrokerMaxSequence ||
		math.IsNaN(r.X) || math.IsInf(r.X, 0) || math.IsNaN(r.Y) || math.IsInf(r.Y, 0) {
		return response
	}
	if r.Phase == mousePhaseUp && r == l.last && l.released && l.failure == nil {
		response.OK, response.ErrorCode = true, "" // retransmitted up ACK, never retransmitted input
		return response
	}
	if r.Sequence != l.last.Sequence+1 {
		return response
	}
	l.last = r
	if r.Phase == mousePhaseUp {
		if !l.pressed {
			return response
		}
		// The child may have lost the last drag ACK. Only the host knows
		// the last published position; never move back to a stale child point.
		b.releaseLocked(l, l.x, l.y, true)
		response.OK = l.failure == nil
		if response.OK {
			response.ErrorCode = ""
		}
		return response
	}
	if l.revoked || l.ended {
		response.ErrorCode = helperInactiveCode
		return response
	}
	if l.failure != nil || l.released || l.ctx.Err() != nil || !time.Now().Before(l.deadline) {
		return response
	}
	switch r.Phase {
	case mousePhaseDown:
		if l.pressed {
			return response
		}
		if err := b.driver.checkPermission(); err != nil {
			l.revoked = true
			return response
		}
		if b.driver.buttonsHeld() {
			l.revoked = true
			return response
		}
		gesture, err := b.driver.prepare(r.Button, r.X, r.Y)
		if err != nil {
			l.revoked = true
			return response
		}
		l.gesture, l.pressed = gesture, true
		gesture.down()
	case mousePhaseDrag:
		if l.gesture == nil {
			return response
		}
		if err := l.gesture.drag(r.X, r.Y); err != nil {
			l.failure, l.revoked = err, true
			b.failure = err
			return response
		}
	default:
		return response
	}
	l.x, l.y = r.X, r.Y
	response.OK, response.ErrorCode = true, ""
	return response
}

func (b *mouseBroker) close() error {
	b.mu.Lock()
	if !b.closed {
		b.closed = true
		if b.batch != nil {
			b.batch.revoked = true
			b.batch.operations = nil
		}
		if b.lease != nil {
			b.lease.revoked = true
			b.releaseLocked(b.lease, b.lease.x, b.lease.y, false)
		}
	}
	err := b.failure
	b.mu.Unlock()
	// File.Close interrupts blocked Read/Write; do not hold mu across either.
	b.requests.Close()
	b.responses.Close()
	return err
}

func (b *mouseBroker) serve() {
	defer close(b.done)
	defer b.close()
	reader := bufio.NewReaderSize(b.requests, mouseBrokerFrameLimit)
	for {
		line, err := reader.ReadSlice('\n')
		if err != nil || len(line) > mouseBrokerFrameLimit {
			return
		}
		fields, err := decodeBrokerFields(line)
		if err != nil {
			return
		}
		var phase string
		if json.Unmarshal(fields["phase"], &phase) != nil {
			return
		}
		var response mouseBrokerResponse
		if phase == inputBatchPhase {
			request, err := decodeInputBatchRequest(line)
			if err != nil {
				return
			}
			response = b.handleBatch(request)
		} else {
			request, err := decodeMouseBrokerRequest(line)
			if err != nil {
				return
			}
			response = b.handle(request)
		}
		encoded, _ := json.Marshal(response)
		encoded = append(encoded, '\n')
		if _, err := b.responses.Write(encoded); err != nil {
			return
		}
	}
}

// Exact fields, including explicit x/y, and no duplicate keys. JSON's default
// zero values or last-key-wins must not silently authorize a malformed event.
func decodeBrokerFields(line []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(line))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return nil, errors.New("invalid mouse broker frame")
	}
	fields := make(map[string]json.RawMessage, 6)
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		name, ok := key.(string)
		if !ok {
			return nil, errors.New("invalid mouse broker key")
		}
		if _, duplicate := fields[name]; duplicate {
			return nil, errors.New("duplicate mouse broker key")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		fields[name] = value
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("trailing mouse broker data")
	}
	return fields, nil
}
func decodeMouseBrokerRequest(line []byte) (mouseBrokerRequest, error) {
	var request mouseBrokerRequest
	fields, err := decodeBrokerFields(line)
	if err != nil {
		return request, err
	}
	if len(fields) != 6 {
		return request, errors.New("invalid mouse broker fields")
	}
	for _, field := range []struct {
		name string
		dest any
	}{
		{"token", &request.Token}, {"sequence", &request.Sequence}, {"phase", &request.Phase},
		{"button", &request.Button}, {"x", &request.X}, {"y", &request.Y},
	} {
		value, ok := fields[field.name]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return request, errors.New("missing mouse broker field")
		}
		if err := json.Unmarshal(value, field.dest); err != nil {
			return request, err
		}
	}
	return request, nil
}
