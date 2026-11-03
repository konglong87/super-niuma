package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/config"
)

type fixtureTimeout struct{}

func (fixtureTimeout) Error() string {
	return "private-timeout https://secret.invalid?key=private-header"
}
func (fixtureTimeout) Timeout() bool   { return true }
func (fixtureTimeout) Temporary() bool { return true }

type failedBody struct {
	reader io.Reader
	err    error
}

func (b failedBody) Read(p []byte) (int, error) {
	n, err := b.reader.Read(p)
	if err == io.EOF {
		err = b.err
	}
	return n, err
}
func (failedBody) Close() error { return nil }

func TestSafeHTTPDiagnosticsThroughRealQuery(t *testing.T) {
	text := `{"role":"assistant","content":"private-response"}`
	good := chatFixture("stop", text, diagnosticUsage)
	cases := []struct {
		name     string
		response func() (*http.Response, error)
		code     ErrorCode
		stage    FailureStage
		media    MediaType
		body     BodyState
		usage    UsageState
		status   int
	}{
		{"401", func() (*http.Response, error) { return fixtureHTTP(401, "application/json", good), nil }, CodeHTTPUnauthorized, StageHTTP, MediaJSON, BodyUnobserved, UsageNotObserved, 401},
		{"403", func() (*http.Response, error) { return fixtureHTTP(403, "text/html", good), nil }, CodeHTTPForbidden, StageHTTP, MediaHTML, BodyUnobserved, UsageNotObserved, 403},
		{"429", func() (*http.Response, error) { return fixtureHTTP(429, "application/json", good), nil }, CodeHTTPRateLimited, StageHTTP, MediaJSON, BodyUnobserved, UsageNotObserved, 429},
		{"500", func() (*http.Response, error) { return fixtureHTTP(500, "application/json", good), nil }, CodeHTTPFailure, StageHTTP, MediaJSON, BodyUnobserved, UsageNotObserved, 500},
		{"header_timeout", func() (*http.Response, error) { return nil, fixtureTimeout{} }, CodeTimeout, StageNetwork, MediaUnknown, BodyUnobserved, UsageNotObserved, 0},
		{"network", func() (*http.Response, error) { return nil, errors.New("private-network-error") }, CodeNetwork, StageNetwork, MediaUnknown, BodyUnobserved, UsageNotObserved, 0},
		{"body_timeout", func() (*http.Response, error) {
			r := fixtureHTTP(200, "application/json", "")
			r.Body = failedBody{strings.NewReader(good), fixtureTimeout{}}
			return r, nil
		}, CodeTimeout, StageBody, MediaJSON, BodyPartial, UsageNotObserved, 200},
		{"body_read", func() (*http.Response, error) {
			r := fixtureHTTP(200, "application/json", "")
			r.Body = failedBody{strings.NewReader(good), io.ErrUnexpectedEOF}
			return r, nil
		}, CodeBodyRead, StageBody, MediaJSON, BodyPartial, UsageNotObserved, 200},
		{"invalid_json", func() (*http.Response, error) {
			return fixtureHTTP(200, "application/json", `{"usage":`+diagnosticUsage+`,"private-response":`), nil
		}, CodeJSONInvalid, StageJSON, MediaJSON, BodyComplete, UsageNotObserved, 200},
		{"body_limit", func() (*http.Response, error) {
			return fixtureHTTP(200, "application/json", strings.Repeat("x", maxResponseBytes+1)), nil
		}, CodeBodyTooLarge, StageBody, MediaJSON, BodySizeLimit, UsageNotObserved, 200},
		{"event_stream", func() (*http.Response, error) { return fixtureHTTP(200, "text/event-stream", good), nil }, CodeMediaType, StageMedia, MediaEventStream, BodyUnobserved, UsageNotObserved, 200},
		{"missing_media", func() (*http.Response, error) { return fixtureHTTP(200, "", good), nil }, CodeMediaType, StageMedia, MediaMissing, BodyUnobserved, UsageNotObserved, 200},
		{"invalid_media", func() (*http.Response, error) { return fixtureHTTP(200, "private-header;=", good), nil }, CodeMediaType, StageMedia, MediaInvalid, BodyUnobserved, UsageNotObserved, 200},
		{"missing_usage", func() (*http.Response, error) {
			return fixtureHTTP(200, "application/json", chatFixture("stop", text, `null`)), nil
		}, CodeUsageMissing, StageUsage, MediaJSON, BodyComplete, UsageMissing, 200},
		{"partial_usage", func() (*http.Response, error) {
			return fixtureHTTP(200, "application/json", chatFixture("tool_calls", diagnosticWrite, `{"prompt_tokens":7}`)), nil
		}, CodeUsageMissing, StageUsage, MediaJSON, BodyComplete, UsagePartial, 200},
		{"invalid_usage", func() (*http.Response, error) {
			return fixtureHTTP(200, "application/json", chatFixture("tool_calls", diagnosticWrite, `{"prompt_tokens":7,"completion_tokens":-9}`)), nil
		}, CodeUsageInvalid, StageUsage, MediaJSON, BodyComplete, UsageInvalid, 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := fixtureRequest(t)
			calls := 0
			var logs bytes.Buffer
			r := diagnosticRunner(t, config.ProviderProtocolOpenAIChatCompletions, func(*http.Request) (*http.Response, error) { calls++; return tc.response() }, &logs)
			got, err := r.Execute(context.Background(), req)
			if FailureCode(err) != string(tc.code) || calls != 1 || got.Text != "" || got.ToolCalls != 0 || len(got.Writes) != 0 || got.UsageKnown || got.Usage != (Usage{}) {
				t.Fatalf("result=%+v code=%s calls=%d", got, FailureCode(err), calls)
			}
			d := got.Diagnostic
			if d == nil || d.Code != tc.code || d.HTTPStatus != tc.status || d.FailureStage != tc.stage || d.MediaType != tc.media || d.BodyState != tc.body || d.UsageState != tc.usage || d.RequestIndex != 1 {
				t.Fatalf("diagnostic=%+v", d)
			}
			if tc.body == BodyUnobserved && d.ResponseBytes != 0 || tc.body != BodyUnobserved && d.ResponseBytes == 0 {
				t.Fatalf("response bytes=%d", d.ResponseBytes)
			}
			if tc.body == BodySizeLimit && d.ResponseBytes != maxResponseBytes+1 {
				t.Fatal("size count not bounded")
			}
			b := req.Budget.(*fakeBudget)
			if len(b.reserved) != 1 || len(b.settled) != 1 || b.settled[0].known || b.settled[0].usage != (Usage{}) {
				t.Fatalf("untrusted response refunded: %+v", b.settled)
			}
			encoded, _ := json.Marshal(got)
			for _, secret := range []string{"private-timeout", "private-network-error", "private-header", "private-response", "secret.invalid", "secret-api-key", req.Objective} {
				if strings.Contains(logs.String()+string(encoded)+err.Error(), secret) {
					t.Fatal("diagnostic leaked private data")
				}
			}
		})
	}
}

func TestRejectedOutcomeDoesNotEmitCallbacks(t *testing.T) {
	for _, body := range []string{
		chatFixture("length", `{"role":"assistant","content":"private-partial"}`, diagnosticUsage),
		chatFixture("stop", `{"role":"assistant","content":"private-response"}`, `null`),
		chatFixture("tool_calls", diagnosticWrite, `{"prompt_tokens":7}`),
	} {
		cfg := fixtureConfig()
		cfg.Provider = "openai"
		cfg.ProviderProtocol = config.ProviderProtocolOpenAIChatCompletions
		inner, err := newHTTPStreamer(cfg)
		if err != nil {
			t.Fatal(err)
		}
		s := inner.(*httpStreamer)
		s.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return fixtureHTTP(200, "application/json", body), nil })
		callbacks := 0
		out, err := s.StreamMessages(context.Background(), anthropic.MessagesRequest{Model: "fixture-model", MaxTokens: 200}, anthropic.StreamCallbacks{OnText: func(string) error { callbacks++; return nil }, OnThinking: func(string) error { callbacks++; return nil }})
		var partial *anthropic.PartialStreamError
		if out != nil || err == nil || callbacks != 0 || errors.As(err, &partial) {
			t.Fatalf("rejected response exposed out=%v err=%v callbacks=%d", out, err, callbacks)
		}
	}
}
