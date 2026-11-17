package execution

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// RetryPolicy bounds how execution re-sends a model request after a
// transient failure. It covers model requests only: a model request has no
// external effect besides its cost, which the budget accounts for. Tools,
// device actions and deliveries are never repeated by execution.
//
// Failures fall in three classes:
//   - not processed (HTTP 429, 408, 503, 529 without a quota signal, or a
//     connection that never opened): the same bytes are sent again inside
//     the same budget reservation, because at most one send is processed;
//   - possibly processed (HTTP 500, 502, 504, a network error or timeout
//     after sending, a body cut off): the reservation is kept as spent and
//     a new one is made before the request is sent again;
//   - permanent (everything else, including 401/403, other 4xx, redirects,
//     an exhausted quota and every problem with a complete response): no
//     retry.
type RetryPolicy struct {
	// MaxRetries is the number of re-sends per model request; 0 disables.
	MaxRetries int
	// BaseDelay is the first wait when the provider gives no Retry-After;
	// it doubles on each retry.
	BaseDelay time.Duration
	// MaxWait is the longest single wait. A longer Retry-After ends the
	// attempt instead of holding it.
	MaxWait time.Duration
	// MaxTotal bounds all waiting in one attempt.
	MaxTotal time.Duration
	// OnRetry, when set, is told about each retry before its wait. It
	// receives only bounded numbers and a safe code.
	OnRetry func(RetryNotice)
}

// RetryNotice describes one retry.
type RetryNotice struct {
	Retry int           // 1-based retry of the current model request
	Max   int           // MaxRetries
	Wait  time.Duration // wait before re-sending
	Code  ErrorCode     // why: e.g. execution_http_rate_limited
}

// DefaultRetryPolicy applies when a Request leaves Retry nil.
var DefaultRetryPolicy = RetryPolicy{MaxRetries: 3, BaseDelay: 2 * time.Second, MaxWait: time.Minute, MaxTotal: 3 * time.Minute}

// NoRetry disables retries for a Request.
var NoRetry = &RetryPolicy{}

const (
	maxRetryCount  = 10
	maxRetryWait   = 5 * time.Minute
	maxRetryTotal  = 30 * time.Minute
	quotaPeekBytes = 4 << 10
)

func validRetryPolicy(p *RetryPolicy) bool {
	return p == nil || (p.MaxRetries >= 0 && p.MaxRetries <= maxRetryCount && p.BaseDelay >= 0 &&
		p.MaxWait >= 0 && p.MaxWait <= maxRetryWait && p.MaxTotal >= 0 && p.MaxTotal <= maxRetryTotal)
}

type retryClass int

const (
	retryNever retryClass = iota
	retryUnprocessed
	retryPossiblyProcessed
)

// retryState is shared by the meter and the transport of one attempt.
type retryState struct {
	policy  RetryPolicy
	sleep   func(context.Context, time.Duration) error
	request int           // retries of the current model request
	total   int           // retries in the attempt
	waited  time.Duration // waiting in the attempt
}

func newRetryState(p *RetryPolicy, sleep func(context.Context, time.Duration) error) *retryState {
	policy := DefaultRetryPolicy
	if p != nil {
		policy = *p
	}
	if sleep == nil {
		sleep = sleepContext
	}
	return &retryState{policy: policy, sleep: sleep}
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *retryState) canRetry() bool { return r != nil && r.request < r.policy.MaxRetries }

// begin starts a new model request.
func (r *retryState) begin() {
	if r != nil {
		r.request = 0
	}
}

// wait decides whether one more re-send is allowed and waits for it. It
// returns false, without waiting, when the policy, the attempt's deadline
// or a too long Retry-After forbids it; and false after a cancelled wait.
func (r *retryState) wait(ctx context.Context, code ErrorCode, retryAfter time.Duration, hasRetryAfter bool) bool {
	if r == nil || r.request >= r.policy.MaxRetries {
		return false
	}
	d := r.policy.BaseDelay << r.request
	if hasRetryAfter {
		d = retryAfter
	}
	if d < 0 || d > r.policy.MaxWait || r.waited+d > r.policy.MaxTotal {
		return false
	}
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= d {
		return false
	}
	r.request++
	r.total++
	if r.policy.OnRetry != nil {
		r.policy.OnRetry(RetryNotice{Retry: r.request, Max: r.policy.MaxRetries, Wait: d, Code: code})
	}
	if err := r.sleep(ctx, d); err != nil {
		return false
	}
	r.waited += d
	return true
}

// classifyResponse sorts a non-2xx response. body is at most quotaPeekBytes
// of it, read only for 429/503/529 to tell an exhausted quota, which no wait
// fixes, from a temporary limit.
func classifyResponse(status int, body []byte) retryClass {
	switch status {
	case http.StatusTooManyRequests, http.StatusRequestTimeout, http.StatusServiceUnavailable, 529:
		if quotaExhausted(body) {
			return retryNever
		}
		return retryUnprocessed
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusGatewayTimeout:
		return retryPossiblyProcessed
	}
	return retryNever
}

func quotaExhausted(body []byte) bool {
	s := strings.ToLower(string(body))
	for _, w := range []string{"insufficient_quota", "quota_exceeded", "exceeded your current quota", "billing", "credit balance"} {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

// neverSent reports a transport error from before the request was written:
// the connection did not open.
func neverSent(err error) bool {
	var op *net.OpError
	return errors.As(err, &op) && op.Op == "dial"
}

// parseRetryAfter reads delay-seconds or an HTTP date.
func parseRetryAfter(v string, now time.Time) (time.Duration, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	if n, err := strconv.Atoi(v); err == nil {
		if n < 0 {
			return 0, false
		}
		return time.Duration(n) * time.Second, true
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := t.Sub(now); d > 0 {
			return d, true
		}
		return 0, true
	}
	return 0, false
}

// possiblyProcessed classifies a failed model request the transport could
// not re-send itself.
func possiblyProcessed(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	d, ok := failureDiagnostic(err)
	if !ok {
		return false
	}
	switch d.Code {
	case CodeNetwork, CodeTimeout, CodeBodyRead:
		return true
	case CodeHTTPFailure:
		return classifyResponse(d.HTTPStatus, nil) == retryPossiblyProcessed
	}
	return false
}
