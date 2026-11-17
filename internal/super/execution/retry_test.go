package execution

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/config"
)

const anthropicDone = `{"role":"assistant","content":[{"type":"text","text":"Done."}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":2}}`

type sleeps struct {
	mu    sync.Mutex
	waits []time.Duration
	hook  func()
}

func (s *sleeps) sleep(ctx context.Context, d time.Duration) error {
	s.mu.Lock()
	s.waits = append(s.waits, d)
	hook := s.hook
	s.mu.Unlock()
	if hook != nil {
		hook()
	}
	return ctx.Err()
}

func (s *sleeps) got() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Duration(nil), s.waits...)
}

// scripted answers each request with the next response function.
func scripted(t *testing.T, steps ...func(http.ResponseWriter, *http.Request)) (*httptest.Server, *atomic.Int32, *[][]byte) {
	t.Helper()
	var calls atomic.Int32
	var mu sync.Mutex
	var bodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		n := int(calls.Add(1)) - 1
		if n >= len(steps) {
			n = len(steps) - 1
		}
		steps[n](w, r)
	}))
	t.Cleanup(server.Close)
	return server, &calls, &bodies
}

func status(code int, headers ...string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		for i := 0; i+1 < len(headers); i += 2 {
			w.Header().Set(headers[i], headers[i+1])
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		io.WriteString(w, `{"error":{"type":"temporary","message":"try later"}}`)
	}
}

func body(code int, text string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		io.WriteString(w, text)
	}
}

func ok(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, anthropicDone)
}

func dropConnection(w http.ResponseWriter, _ *http.Request) {
	conn, _, err := w.(http.Hijacker).Hijack()
	if err == nil {
		conn.Close()
	}
}

func retryRunner(baseURL string, s *sleeps) *Runner {
	cfg := fixtureConfig()
	cfg.BaseURL = baseURL
	r := New(func(context.Context) (config.Config, error) { return cfg, nil }, nil)
	r.sleep = s.sleep
	return r
}

func retryRequest(t *testing.T, p *RetryPolicy) Request {
	req := fixtureRequest(t)
	req.Retry = p
	return req
}

func policy(retries int) *RetryPolicy {
	return &RetryPolicy{MaxRetries: retries, BaseDelay: 2 * time.Second, MaxWait: time.Minute, MaxTotal: 3 * time.Minute}
}

func TestUnprocessedResponsesResendSameBytesInOneReservation(t *testing.T) {
	for _, code := range []int{http.StatusTooManyRequests, http.StatusRequestTimeout, http.StatusServiceUnavailable, 529} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			server, calls, bodies := scripted(t, status(code, "Retry-After", "7"), ok)
			s := &sleeps{}
			req := retryRequest(t, policy(3))
			got, err := retryRunner(server.URL, s).Execute(context.Background(), req)
			if err != nil || got.Text != "Done." || calls.Load() != 2 || got.Retries != 1 || got.RetryWaitMS != 7000 || !got.UsageKnown {
				t.Fatalf("result=%+v err=%v calls=%d", got, err, calls.Load())
			}
			if string((*bodies)[0]) != string((*bodies)[1]) {
				t.Fatal("re-send changed the request")
			}
			b := req.Budget.(*fakeBudget)
			if len(b.reserved) != 1 || len(b.settled) != 1 || !b.settled[0].known {
				t.Fatalf("reservations %+v settlements %+v", b.reserved, b.settled)
			}
			if w := s.got(); len(w) != 1 || w[0] != 7*time.Second {
				t.Fatalf("waits %v", w)
			}
		})
	}
}

func TestBackoffDoublesWithoutRetryAfterAndStopsAtTheLimit(t *testing.T) {
	server, calls, _ := scripted(t, status(http.StatusTooManyRequests))
	s := &sleeps{}
	got, err := retryRunner(server.URL, s).Execute(context.Background(), retryRequest(t, policy(3)))
	if FailureCode(err) != string(CodeHTTPRateLimited) || calls.Load() != 4 || got.Retries != 3 {
		t.Fatalf("err=%v calls=%d result=%+v", err, calls.Load(), got)
	}
	if w := s.got(); fmt.Sprint(w) != "[2s 4s 8s]" {
		t.Fatalf("waits %v", w)
	}
	if got.Diagnostic == nil || got.Diagnostic.HTTPStatus != 429 {
		t.Fatalf("diagnostic %+v", got.Diagnostic)
	}
}

func TestNoWaitWhenRetryAfterTooLongTotalSpentOrDeadlineNear(t *testing.T) {
	t.Run("retry-after over MaxWait", func(t *testing.T) {
		server, calls, _ := scripted(t, status(429, "Retry-After", "120"), ok)
		s := &sleeps{}
		if _, err := retryRunner(server.URL, s).Execute(context.Background(), retryRequest(t, policy(3))); err == nil || calls.Load() != 1 || len(s.got()) != 0 {
			t.Fatalf("err=%v calls=%d waits=%v", err, calls.Load(), s.got())
		}
	})
	t.Run("MaxTotal", func(t *testing.T) {
		server, calls, _ := scripted(t, status(429, "Retry-After", "50"), status(429, "Retry-After", "50"), ok)
		s := &sleeps{}
		p := policy(3)
		p.MaxTotal = 60 * time.Second
		if _, err := retryRunner(server.URL, s).Execute(context.Background(), retryRequest(t, p)); err == nil || calls.Load() != 2 {
			t.Fatalf("err=%v calls=%d", err, calls.Load())
		}
	})
	t.Run("deadline", func(t *testing.T) {
		server, calls, _ := scripted(t, status(429, "Retry-After", "30"), ok)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := retryRunner(server.URL, &sleeps{}).Execute(ctx, retryRequest(t, policy(3))); err == nil || calls.Load() != 1 {
			t.Fatalf("waited past the deadline: err=%v calls=%d", err, calls.Load())
		}
	})
}

func TestExhaustedQuotaIsNeverRetried(t *testing.T) {
	for _, text := range []string{`{"error":{"code":"insufficient_quota"}}`, `{"error":{"message":"You exceeded your current quota"}}`} {
		server, calls, _ := scripted(t, body(429, text), ok)
		if _, err := retryRunner(server.URL, &sleeps{}).Execute(context.Background(), retryRequest(t, policy(3))); FailureCode(err) != string(CodeHTTPRateLimited) || calls.Load() != 1 {
			t.Fatalf("%s: err=%v calls=%d", text, err, calls.Load())
		}
	}
}

func TestPossiblyProcessedFailuresResendWithANewReservation(t *testing.T) {
	cases := map[string]func(http.ResponseWriter, *http.Request){
		"500": status(500), "502": status(502), "504": status(504), "dropped connection": dropConnection,
	}
	for name, fail := range cases {
		t.Run(name, func(t *testing.T) {
			server, calls, bodies := scripted(t, fail, ok)
			s := &sleeps{}
			req := retryRequest(t, policy(3))
			got, err := retryRunner(server.URL, s).Execute(context.Background(), req)
			if err != nil || got.Text != "Done." || calls.Load() != 2 || got.Retries != 1 {
				t.Fatalf("result=%+v err=%v calls=%d", got, err, calls.Load())
			}
			if name != "dropped connection" && string((*bodies)[0]) != string((*bodies)[1]) {
				t.Fatal("re-send changed the request")
			}
			b := req.Budget.(*fakeBudget)
			if len(b.reserved) != 2 || b.reserved[1].id != b.reserved[0].id+".retry.1" || len(b.settled) != 2 || b.settled[0].known || !b.settled[1].known {
				t.Fatalf("the possibly processed request was refunded: reserved %+v settled %+v", b.reserved, b.settled)
			}
			if got.UsageKnown {
				t.Fatal("usage of a possibly processed request reported as known")
			}
			if w := s.got(); fmt.Sprint(w) != "[2s]" {
				t.Fatalf("waits %v", w)
			}
		})
	}
}

func TestPermanentFailuresAreNeverRetried(t *testing.T) {
	for _, code := range []int{400, 401, 403, 404, 413, 422, http.StatusTemporaryRedirect, 501} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			server, calls, _ := scripted(t, status(code, "Retry-After", "1", "Location", "http://127.0.0.1:1/"), ok)
			s := &sleeps{}
			if _, err := retryRunner(server.URL, s).Execute(context.Background(), retryRequest(t, policy(3))); err == nil || calls.Load() != 1 || len(s.got()) != 0 {
				t.Fatalf("err=%v calls=%d waits=%v", err, calls.Load(), s.got())
			}
		})
	}
	t.Run("invalid JSON in a 200", func(t *testing.T) {
		server, calls, _ := scripted(t, body(200, `{"role":`), ok)
		if _, err := retryRunner(server.URL, &sleeps{}).Execute(context.Background(), retryRequest(t, policy(3))); err == nil || calls.Load() != 1 {
			t.Fatalf("err=%v calls=%d", err, calls.Load())
		}
	})
}

func TestConnectionThatNeverOpensIsRetriedInOneReservation(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	s := &sleeps{}
	req := retryRequest(t, policy(2))
	got, err := retryRunner("http://"+addr, s).Execute(context.Background(), req)
	if FailureCode(err) != string(CodeNetwork) || got.Retries != 2 {
		t.Fatalf("err=%v result=%+v", err, got)
	}
	if b := req.Budget.(*fakeBudget); len(b.reserved) != 1 {
		t.Fatalf("a request that never left took %d reservations", len(b.reserved))
	}
}

func TestCancelOrRevocationDuringAWaitSendsNothingMore(t *testing.T) {
	t.Run("cancel", func(t *testing.T) {
		server, calls, _ := scripted(t, status(429, "Retry-After", "5"), ok)
		ctx, cancel := context.WithCancel(context.Background())
		s := &sleeps{hook: cancel}
		if _, err := retryRunner(server.URL, s).Execute(ctx, retryRequest(t, policy(3))); err == nil || calls.Load() != 1 {
			t.Fatalf("err=%v calls=%d", err, calls.Load())
		}
	})
	t.Run("revoked", func(t *testing.T) {
		server, calls, _ := scripted(t, status(503), ok)
		var revoked atomic.Bool
		s := &sleeps{hook: func() { revoked.Store(true) }}
		req := retryRequest(t, policy(3))
		req.CheckAuthority = func(context.Context) error {
			if revoked.Load() {
				return errors.New("revoked")
			}
			return nil
		}
		if _, err := retryRunner(server.URL, s).Execute(context.Background(), req); !errors.Is(err, ErrAuthority) || calls.Load() != 1 {
			t.Fatalf("err=%v calls=%d", err, calls.Load())
		}
	})
}

func TestDefaultPolicyAppliesAndNoticesAreBounded(t *testing.T) {
	server, calls, _ := scripted(t, status(429, "Retry-After", "3"), status(500), ok)
	s := &sleeps{}
	req := fixtureRequest(t)
	req.Retry = nil
	if _, err := retryRunner(server.URL, s).Execute(context.Background(), req); err != nil || calls.Load() != 3 {
		t.Fatalf("default policy not applied: err=%v calls=%d", err, calls.Load())
	}
	var notices []RetryNotice
	p := policy(2)
	p.OnRetry = func(n RetryNotice) { notices = append(notices, n) }
	server2, _, _ := scripted(t, status(429, "Retry-After", "3"), ok)
	if _, err := retryRunner(server2.URL, &sleeps{}).Execute(context.Background(), retryRequest(t, p)); err != nil {
		t.Fatal(err)
	}
	if len(notices) != 1 || notices[0] != (RetryNotice{Retry: 1, Max: 2, Wait: 3 * time.Second, Code: CodeHTTPRateLimited}) {
		t.Fatalf("notices %+v", notices)
	}
}

func TestInvalidRetryPolicyRejected(t *testing.T) {
	for _, p := range []*RetryPolicy{{MaxRetries: -1}, {MaxRetries: 11}, {MaxRetries: 1, MaxWait: time.Hour}, {MaxRetries: 1, BaseDelay: -1}} {
		if _, _, err := validateRequest(retryRequest(t, p)); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%+v accepted", p)
		}
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	for v, want := range map[string]time.Duration{"7": 7 * time.Second, "0": 0, now.Add(30 * time.Second).Format(http.TimeFormat): 30 * time.Second} {
		if got, ok := parseRetryAfter(v, now); !ok || got != want {
			t.Fatalf("%q: %v %v", v, got, ok)
		}
	}
	for _, v := range []string{"", "-1", "soon"} {
		if _, ok := parseRetryAfter(v, now); ok {
			t.Fatalf("%q parsed", v)
		}
	}
}
