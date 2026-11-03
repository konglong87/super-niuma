package anthropic

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/config"
)

func TestResponsesCommentKeepaliveDoesNotBreakJSONStream(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		_, _ = fmt.Fprint(w, ":\n\n") // Exact three-byte block seen in the synthetic live probe.
		writeOpenAIResponsesTextStream(w, "kept")
	}))
	defer server.Close()
	client := NewClient(config.Config{Provider: "custom", ProviderProtocol: config.ProviderProtocolOpenAIResponses, APIKey: "test-only", BaseURL: server.URL + "/v1"})
	result, err := client.StreamMessages(context.Background(), MessagesRequest{Model: "test", Messages: []MessageParam{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}}}, StreamCallbacks{})
	if err != nil {
		t.Fatalf("comment-only keepalive broke the JSON stream: %v", err)
	}
	if calls.Load() != 1 || len(result.Message.Content) != 1 || result.Message.Content[0].Text != "kept" {
		t.Fatalf("calls=%d result=%+v", calls.Load(), result)
	}
}

func TestResponsesSSEBodyPreservesDataBlocks(t *testing.T) {
	for _, test := range []struct{ name, input, want string }{
		{"blank and comments", "\n:\n\n: keepalive\r\n\r\ndata: {}\n\n", "data: {}\n\n"},
		{"no-data fields", "event: ping\nid: 7\nretry: 1000\n\ndata: {}\n\n", "data: {}\n\n"},
		{"comments in data block", ": keep\nevent: response.created\ndata: {}\n\n", ": keep\nevent: response.created\ndata: {}\n\n"},
		{"explicit empty data", "data:\n\n", "data:\n\n"},
		{"colonless data", "data\n\n", "data\n\n"},
		{"whitespace data", "data:   \n\n", "data:   \n\n"},
		{"malformed JSON", "data: {broken\n\n", "data: {broken\n\n"},
		{"multiple lines CRLF", "data: {\r\ndata: }\r\n\r\n", "data: {\r\ndata: }\r\n\r\n"},
		{"unterminated", "data: {", "data: {"},
		{"done marker", ":\n\ndata: [DONE]\n\n", "data: [DONE]\n\n"},
		{"BOM data not discarded", "\xef\xbb\xbfdata: {}\n\n", "\xef\xbb\xbfdata: {}\n\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := newResponsesSSEBody(io.NopCloser(strings.NewReader(test.input)), 1024)
			got, err := io.ReadAll(body)
			if err != nil || string(got) != test.want {
				t.Fatalf("got %q err=%v want %q", got, err, test.want)
			}
		})
	}
}

type bytewiseSSEReader struct{ io.Reader }

func (r bytewiseSSEReader) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return r.Reader.Read(p)
}
func TestResponsesSSEFragmentationAndLimit(t *testing.T) {
	const payload = "event: message\ndata: {\n data-is-unknown\ndata: }\n\n"
	body := newResponsesSSEBody(io.NopCloser(bytewiseSSEReader{strings.NewReader(":\r\n\r\n" + payload)}), 1024)
	got, err := io.ReadAll(body)
	if err != nil || string(got) != payload {
		t.Fatalf("fragmented got=%q err=%v", got, err)
	}
	for _, input := range []string{"data: " + strings.Repeat("x", 100) + "\n\n", ":" + strings.Repeat("x", 100) + "\n\n", strings.Repeat(":a\n", 30) + "\n"} {
		_, err = io.ReadAll(newResponsesSSEBody(io.NopCloser(strings.NewReader(input)), 64))
		if !errors.Is(err, errResponsesSSEFrameTooLarge) {
			t.Fatalf("unbounded SSE block: %v", err)
		}
	}
}

type sseDoerFunc func(*http.Request) (*http.Response, error)

func (f sseDoerFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

type sseCloseSpy struct {
	io.Reader
	closed bool
}

func (s *sseCloseSpy) Close() error { s.closed = true; return nil }
func TestResponsesSSEClientScopeAndClose(t *testing.T) {
	for _, test := range []struct {
		mime     string
		status   int
		filtered bool
	}{
		{"text/event-stream; charset=utf-8", 200, true}, {"text/event-stream", 400, false},
		{"application/json", 200, false}, {"application/not-text/event-stream", 200, false},
	} {
		body := &sseCloseSpy{Reader: strings.NewReader(":\n\ndata: {}\n\n")}
		client := responsesSSEClient{inner: sseDoerFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: test.status, Header: http.Header{"Content-Type": []string{test.mime}}, Body: body}, nil
		})}
		response, err := client.Do(httptest.NewRequest("GET", "http://unused.invalid", nil))
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		want := ":\n\ndata: {}\n\n"
		if test.filtered {
			want = "data: {}\n\n"
		}
		if string(got) != want {
			t.Fatalf("scope %v got=%q", test, got)
		}
		_ = response.Body.Close()
		if !body.closed {
			t.Fatal("source was not closed")
		}
	}
}

func TestResponsesKeepalivesCountAsNetworkActivity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for i := 0; i < 10; i++ {
			_, _ = fmt.Fprint(w, ":\n\n")
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(50 * time.Millisecond):
			}
		}
		_, _ = fmt.Fprint(w, "data: {}\n\n")
	}))
	defer server.Close()
	raw := newHTTPTraceClientWithTimeouts(nil, providerHTTPTimeouts{responseHeader: time.Second, streamIdle: 300 * time.Millisecond})
	client := responsesSSEClient{inner: raw}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL, nil)
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	got, err := io.ReadAll(response.Body)
	if err != nil || string(got) != "data: {}\n\n" {
		t.Fatalf("keepalive bytes must reset raw idle timer: %q %v", got, err)
	}
}

func TestResponsesKeepaliveReadCancellation(t *testing.T) {
	opened := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, ":\n\n")
		w.(http.Flusher).Flush()
		close(opened)
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL, nil)
	response, err := (responsesSSEClient{inner: newHTTPTraceClient(nil)}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	<-opened
	done := make(chan error, 1)
	go func() { _, err := io.ReadAll(response.Body); done <- err }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation lost: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("filtered read ignored cancellation")
	}
}

func TestResponsesExplicitEmptyDataRemainsAnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data:\n\n")
		writeOpenAIResponsesTextStream(w, "must-not-succeed")
	}))
	defer server.Close()
	client := NewClient(config.Config{Provider: "custom", ProviderProtocol: config.ProviderProtocolOpenAIResponses, APIKey: "test-only", BaseURL: server.URL + "/v1"})
	if _, err := client.StreamMessages(context.Background(), MessagesRequest{Model: "test", Messages: []MessageParam{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}}}, StreamCallbacks{}); err == nil {
		t.Fatal("explicit empty JSON event was swallowed")
	}
}
