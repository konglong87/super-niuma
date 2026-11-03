package macos

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"

	cu "github.com/konglong87/super-niuma/internal/computeruse"
)

const inputBatchPhase = "batch"
const inputBatchTokenField = "input_batch_token"
const inputBatchCountField = "input_batch_count"

type inputBatchRequest struct {
	Token    string `json:"token"`
	Sequence int64  `json:"sequence"`
	Phase    string `json:"phase"`
}
type inputBatchLease struct {
	token                   string
	ctx                     context.Context
	deadline                time.Time
	operations              []inputBatchOperation
	next                    int
	started, revoked, ended bool
}
type inputBatchResult struct{ started, complete bool }

func (b *mouseBroker) authorizeBatch(ctx context.Context, action cu.Action, obs cu.Observation, timeout time.Duration) (string, int, error) {
	operations, err := planInputBatches(action, obs)
	if err != nil {
		return "", 0, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || b.failure != nil || b.batchDriver == nil || (b.lease != nil && !b.lease.ended) || (b.batch != nil && !b.batch.ended) {
		return "", 0, errMouseBrokerUnavailable
	}
	if err := ctx.Err(); err != nil {
		return "", 0, err
	}
	var entropy [mouseBrokerTokenBytes]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", 0, err
	}
	deadline := time.Now().Add(timeout)
	if caller, ok := ctx.Deadline(); ok && caller.Before(deadline) {
		deadline = caller
	}
	token := hex.EncodeToString(entropy[:])
	// The unguessable one-action token binds session/action/epoch by construction:
	// Backend holds its epoch lock through authorize, and revoke uses the same
	// broker mutex as publication. No helper-supplied identity can widen the grant.
	b.batch = &inputBatchLease{token: token, ctx: ctx, deadline: deadline, operations: operations}
	return token, len(operations), nil
}
func (b *mouseBroker) handleBatch(r inputBatchRequest) mouseBrokerResponse {
	response := mouseBrokerResponse{Sequence: r.Sequence, ErrorCode: mouseBrokerInputUnavailable}
	b.mu.Lock()
	defer b.mu.Unlock()
	l := b.batch
	if b.closed || b.failure != nil || l == nil || r.Token != l.token || r.Phase != inputBatchPhase {
		return response
	}
	if l.ended || l.revoked {
		response.ErrorCode = helperInactiveCode
		return response
	}
	if l.ctx.Err() != nil || !time.Now().Before(l.deadline) || r.Sequence != int64(l.next+1) || l.next >= len(l.operations) {
		return response
	}
	prepared, err := b.batchDriver.prepareBatch(l.operations[l.next])
	if err != nil {
		l.revoked = true
		return response
	}
	defer prepared.close()
	// Preparation can take time; cancellation before the first physical event
	// rejects without posting. Once commit starts it owns every required release.
	if l.ctx.Err() != nil || !time.Now().Before(l.deadline) {
		l.revoked = true
		return response
	}
	l.started = true
	prepared.commit()
	l.next++
	response.OK, response.ErrorCode = true, ""
	return response
}
func (b *mouseBroker) finishBatch(token string) inputBatchResult {
	b.mu.Lock()
	defer b.mu.Unlock()
	l := b.batch
	if l == nil || l.token != token {
		return inputBatchResult{}
	}
	l.ended, l.revoked = true, true
	result := inputBatchResult{started: l.started, complete: l.next == len(l.operations)}
	l.operations = nil // typed text/key data must not outlive the action
	return result
}

func decodeInputBatchRequest(line []byte) (inputBatchRequest, error) {
	var request inputBatchRequest
	fields, err := decodeBrokerFields(line)
	if err != nil {
		return request, err
	}
	if len(fields) != 3 {
		return request, errMouseBrokerUnavailable
	}
	for _, f := range []struct {
		name string
		dest any
	}{{"token", &request.Token}, {"sequence", &request.Sequence}, {"phase", &request.Phase}} {
		v, ok := fields[f.name]
		if !ok || string(v) == "null" {
			return request, errMouseBrokerUnavailable
		}
		if err := json.Unmarshal(v, f.dest); err != nil {
			return request, err
		}
	}
	if request.Phase != inputBatchPhase {
		return request, errMouseBrokerUnavailable
	}
	return request, nil
}
