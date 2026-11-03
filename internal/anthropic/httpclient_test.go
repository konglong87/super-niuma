package anthropic

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProviderTransportOverridesDefaultPoolAndHandshakeTimeouts(t *testing.T) {
	transport := newProviderTransport()
	defaultTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		t.Fatalf("http.DefaultTransport is not *http.Transport")
	}
	if transport.MaxIdleConnsPerHost <= defaultTransport.MaxIdleConnsPerHost {
		t.Fatalf("MaxIdleConnsPerHost = %d, want more than default %d", transport.MaxIdleConnsPerHost, defaultTransport.MaxIdleConnsPerHost)
	}
	if transport.MaxIdleConnsPerHost != providerMaxIdleConnsPerHost {
		t.Fatalf("MaxIdleConnsPerHost = %d, want %d", transport.MaxIdleConnsPerHost, providerMaxIdleConnsPerHost)
	}
	if transport.IdleConnTimeout != providerIdleConnTimeout {
		t.Fatalf("IdleConnTimeout = %s, want %s", transport.IdleConnTimeout, providerIdleConnTimeout)
	}
	if transport.TLSHandshakeTimeout != providerTLSHandshakeTimeout {
		t.Fatalf("TLSHandshakeTimeout = %s, want %s", transport.TLSHandshakeTimeout, providerTLSHandshakeTimeout)
	}
	if transport.ExpectContinueTimeout != providerExpectContinueTimeout {
		t.Fatalf("ExpectContinueTimeout = %s, want %s", transport.ExpectContinueTimeout, providerExpectContinueTimeout)
	}
	if transport.MaxIdleConns != providerMaxIdleConns {
		t.Fatalf("MaxIdleConns = %d, want %d", transport.MaxIdleConns, providerMaxIdleConns)
	}
	if transport.DialContext == nil {
		t.Fatalf("DialContext must be set so dials cannot hang forever")
	}
}

func TestDefaultHTTPTraceClientHasNoTotalClientTimeout(t *testing.T) {
	client := newHTTPTraceClient(nil)
	if client.client.Timeout != 0 {
		t.Fatalf("http.Client.Timeout = %s, want 0 so long streams are not truncated", client.client.Timeout)
	}
	timeouts := client.timeouts
	if timeouts.responseHeader <= 0 || timeouts.streamIdle <= 0 || timeouts.responseBody <= 0 {
		t.Fatalf("default provider timeouts must all be set, got %+v", timeouts)
	}
}

func TestHTTPTraceClientFailsWhenGatewayNeverSendsHeaders(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	t.Cleanup(func() {
		close(release)
		server.Close()
	})

	client := newHTTPTraceClientWithTimeouts(nil, providerHTTPTimeouts{
		responseHeader: 150 * time.Millisecond,
		streamIdle:     10 * time.Second,
		responseBody:   10 * time.Second,
	})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	start := time.Now()
	resp, err := client.Do(req)
	if err == nil {
		resp.Body.Close()
		t.Fatalf("expected a timeout error from a gateway that never answers")
	}
	if !strings.Contains(err.Error(), "provider sent no response headers") {
		t.Fatalf("error = %v, want it to name the response-header phase", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("Do returned after %s, want it bounded by the response-header timeout", elapsed)
	}
}

func TestHTTPTraceClientStopsStalledStreamButNotSlowOne(t *testing.T) {
	// 服务端先正常建流并推一个事件，然后既不推 chunk 也不关连接。
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "event: ping\ndata: {}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)

	client := newHTTPTraceClientWithTimeouts(nil, providerHTTPTimeouts{
		responseHeader: 100 * time.Millisecond,
		streamIdle:     300 * time.Millisecond,
		responseBody:   100 * time.Millisecond,
	})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	// 头到了之后建流超时必须解除，否则这一次读会被误杀。
	time.Sleep(250 * time.Millisecond)
	buf := make([]byte, 64)
	n, err := resp.Body.Read(buf)
	if err != nil {
		t.Fatalf("first stream read failed after the response-header timeout elapsed: %v", err)
	}
	if !strings.Contains(string(buf[:n]), "event: ping") {
		t.Fatalf("first read = %q, want the first SSE event", string(buf[:n]))
	}

	start := time.Now()
	_, err = io.ReadAll(resp.Body)
	if err == nil {
		t.Fatalf("expected the stalled stream to be aborted")
	}
	if !strings.Contains(err.Error(), "provider stream stalled") {
		t.Fatalf("error = %v, want it to name the stream idle timeout", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("stalled stream aborted after %s, want it bounded by the idle timeout", elapsed)
	}
}

func TestHTTPTraceClientKeepsLongStreamAlive(t *testing.T) {
	const events = 6
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		for i := 0; i < events; i++ {
			_, _ = io.WriteString(w, "event: delta\ndata: {}\n\n")
			flusher.Flush()
			time.Sleep(80 * time.Millisecond)
		}
	}))
	t.Cleanup(server.Close)

	// 总时长（约 480ms）远超任何单段超时；只要不停推进就不该被砍。
	client := newHTTPTraceClientWithTimeouts(nil, providerHTTPTimeouts{
		responseHeader: 200 * time.Millisecond,
		streamIdle:     300 * time.Millisecond,
		responseBody:   200 * time.Millisecond,
	})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read long stream: %v", err)
	}
	if got := strings.Count(string(body), "event: delta"); got != events {
		t.Fatalf("received %d events, want %d (body=%q)", got, events, string(body))
	}
}

func TestHTTPTraceClientBoundsNonStreamingBodyRead(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"partial":`)
		w.(http.Flusher).Flush()
		<-release
	}))
	t.Cleanup(func() {
		close(release)
		server.Close()
	})

	client := newHTTPTraceClientWithTimeouts(nil, providerHTTPTimeouts{
		responseHeader: 5 * time.Second,
		streamIdle:     5 * time.Second,
		responseBody:   200 * time.Millisecond,
	})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	start := time.Now()
	if _, err := io.ReadAll(resp.Body); err == nil {
		t.Fatalf("expected the truncated non-streaming body read to time out")
	} else if !strings.Contains(err.Error(), "provider response body timed out") {
		t.Fatalf("error = %v, want it to name the response-body timeout", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("body read aborted after %s, want it bounded by the body timeout", elapsed)
	}
}

func TestHTTPTraceClientKeepsPhaseTelemetryWiring(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{}`)
	}))
	t.Cleanup(server.Close)

	client := newHTTPTraceClient(nil)
	trace := newHTTPPhaseTrace(context.Background(), time.Now(), providerClient{name: "primary"}, MessagesRequest{Model: "m"})
	req, err := http.NewRequestWithContext(trace.context(context.Background()), http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	props := trace.properties(time.Now())
	for _, phase := range []string{"http_round_trip_ms", "http.get_conn_ms", "http.wait_first_response_byte_ms"} {
		if _, ok := props[phase]; !ok {
			t.Fatalf("phase %q missing from trace properties %v; timeout work must not break DNS/connect/TTFB telemetry", phase, props)
		}
	}
}
