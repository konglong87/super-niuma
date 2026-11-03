package imagegen

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOpenAICompatibleProviderGenerate(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/generations" || r.Method != http.MethodPost {
			t.Fatalf("request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("missing auth")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-ID", "provider-request-1")
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"` + base64.StdEncoding.EncodeToString([]byte("img")) + `"}]}`))
	}))
	defer ts.Close()
	p := NewOpenAICompatibleImagesProvider(ProviderConfig{BaseURL: ts.URL, APIKey: "secret", MaxRetries: 1})
	got, err := p.Generate(context.Background(), ProviderGenerateRequest{Prompt: "a cat", Model: "gpt-image-2", Quality: "high", Size: "1024x1024", OutputFormat: "png", Background: "auto"})
	if err != nil || string(got.Data) != "img" || got.MediaType != "image/png" || got.ProviderRequestID != "provider-request-1" {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func TestOpenAICompatibleProviderRejectsURLOnlyImageResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"url":"https://private.example/image.png"}]}`))
	}))
	defer ts.Close()
	p := NewOpenAICompatibleImagesProvider(ProviderConfig{BaseURL: ts.URL})
	_, err := p.Generate(context.Background(), ProviderGenerateRequest{Prompt: "x"})
	if err == nil || !errors.Is(err, ErrProviderResponse) || !strings.Contains(err.Error(), "URL-only") {
		t.Fatalf("err = %v, want explicit unsupported URL-only response", err)
	}
}

func TestOpenAICompatibleProviderEditMultipart(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			t.Fatalf("content type %s", r.Header.Get("Content-Type"))
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		if r.FormValue("prompt") != "redraw" {
			t.Fatalf("prompt %q", r.FormValue("prompt"))
		}
		f, header, err := r.FormFile("image")
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if got := header.Header.Get("Content-Type"); got != "image/png" {
			t.Fatalf("image content type %q", got)
		}
		b, _ := io.ReadAll(f)
		if string(b) != "src" {
			t.Fatalf("image %q", b)
		}
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"aW1nMg=="}]}`))
	}))
	defer ts.Close()
	p := NewOpenAICompatibleImagesProvider(ProviderConfig{BaseURL: ts.URL})
	got, err := p.Edit(context.Background(), ProviderEditRequest{Prompt: "redraw", Model: "gpt-image-2", Image: strings.NewReader("src"), ImageName: "source.png", ImageType: "image/png"})
	if err != nil || string(got.Data) != "img2" {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func TestAgnesProviderUsesTierRatioAndDataURIForSingleImageEdit(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/generations" || r.Method != http.MethodPost {
			t.Fatalf("request %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["size"] != "2K" || body["ratio"] != "16:9" {
			t.Fatalf("body=%v", body)
		}
		extra, ok := body["extra_body"].(map[string]any)
		if !ok || extra["response_format"] != "b64_json" {
			t.Fatalf("extra_body=%v", body["extra_body"])
		}
		images, ok := extra["image"].([]any)
		if !ok || len(images) != 1 || !strings.HasPrefix(images[0].(string), "data:image/png;base64,") {
			t.Fatalf("images=%v", extra["image"])
		}
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"aW1n"}]}`))
	}))
	defer ts.Close()
	provider := NewOpenAICompatibleImagesProvider(ProviderConfig{BaseURL: ts.URL, ImageProtocol: "agnes-images"})
	got, err := provider.Edit(context.Background(), ProviderEditRequest{Prompt: "redraw", Model: "agnes-image-2.5-flash", Resolution: "2K", AspectRatio: "16:9", Image: strings.NewReader("src"), ImageType: "image/png"})
	if err != nil || string(got.Data) != "img" {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func TestProviderRegistryRejectsUnsupportedModelCapability(t *testing.T) {
	registry := NewProviderRegistry("agnes", map[string]ImagesProvider{"agnes": &stubProvider{}}).WithCapabilities([]ProviderModelCapability{{Provider: "agnes", Model: "agnes-image-2.5-flash", Operations: []string{"generate"}, Resolutions: []string{"2K"}}})
	_, err := registry.Generate(context.Background(), ProviderGenerateRequest{Provider: "agnes", Model: "unknown", Prompt: "cat", Resolution: "2K"})
	if err == nil || !strings.Contains(err.Error(), "provider/model") {
		t.Fatalf("err=%v", err)
	}
	_, err = registry.Generate(context.Background(), ProviderGenerateRequest{Provider: "agnes", Model: "agnes-image-2.5-flash", Prompt: "cat", Resolution: "4K"})
	if err == nil || !strings.Contains(err.Error(), "resolution") {
		t.Fatalf("err=%v", err)
	}
}

func TestAgnesProviderDownloadsURLResultFromConfiguredHost(t *testing.T) {
	pngBytes, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	apiServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/image.png" {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(pngBytes)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"url":"` + "https://" + r.Host + `/image.png"}]}`))
	}))
	defer apiServer.Close()
	provider := NewOpenAICompatibleImagesProvider(ProviderConfig{BaseURL: apiServer.URL, ImageProtocol: "agnes-images", AllowRemoteURLs: true, HTTPClient: apiServer.Client()})
	got, err := provider.Generate(context.Background(), ProviderGenerateRequest{Prompt: "cat", Model: "agnes-image-2.5-flash"})
	if err != nil || !bytes.Equal(got.Data, pngBytes) {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func TestProviderRetriesTransientFailure(t *testing.T) {
	count := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		if count == 1 {
			http.Error(w, "busy", 503)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"aW1n"}]}`))
	}))
	defer ts.Close()
	p := NewOpenAICompatibleImagesProvider(ProviderConfig{BaseURL: ts.URL, MaxRetries: 1})
	if _, err := p.Generate(context.Background(), ProviderGenerateRequest{Prompt: "x"}); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("requests=%d", count)
	}
}

func TestOpenAICompatibleProviderDistinguishesCallerDeadlineFromProviderTimeout(t *testing.T) {
	newBlockingServer := func(t *testing.T) *httptest.Server {
		t.Helper()
		release := make(chan struct{})
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-release:
			}
		}))
		t.Cleanup(func() {
			close(release)
			ts.Close()
		})
		return ts
	}

	t.Run("caller deadline", func(t *testing.T) {
		ts := newBlockingServer(t)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		provider := NewOpenAICompatibleImagesProvider(ProviderConfig{BaseURL: ts.URL, Timeout: time.Second})
		_, err := provider.Generate(ctx, ProviderGenerateRequest{Prompt: "cat"})
		if !errors.Is(err, context.DeadlineExceeded) || ProviderErrorClass(err) != ErrorClassCallerDeadlineExceeded {
			t.Fatalf("err=%v class=%q", err, ProviderErrorClass(err))
		}
	})

	t.Run("provider timeout", func(t *testing.T) {
		ts := newBlockingServer(t)
		provider := NewOpenAICompatibleImagesProvider(ProviderConfig{BaseURL: ts.URL, Timeout: 20 * time.Millisecond})
		_, err := provider.Generate(context.Background(), ProviderGenerateRequest{Prompt: "cat"})
		if !errors.Is(err, ErrProviderTimeout) || ProviderErrorClass(err) != ErrorClassProviderTimeout {
			t.Fatalf("err=%v class=%q", err, ProviderErrorClass(err))
		}
	})
}

func TestOpenAICompatibleProviderClassifiesTimeoutWhileReadingResponseBody(t *testing.T) {
	release := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(func() {
		close(release)
		ts.Close()
	})

	provider := NewOpenAICompatibleImagesProvider(ProviderConfig{BaseURL: ts.URL, Timeout: 20 * time.Millisecond})
	_, err := provider.Generate(context.Background(), ProviderGenerateRequest{Prompt: "cat"})
	if !errors.Is(err, ErrProviderTimeout) || ProviderErrorClass(err) != ErrorClassProviderTimeout {
		t.Fatalf("err=%v class=%q", err, ProviderErrorClass(err))
	}
}

func TestOpenAICompatibleProviderClassifiesCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	provider := NewOpenAICompatibleImagesProvider(ProviderConfig{BaseURL: "https://example.test"})
	_, err := provider.Generate(ctx, ProviderGenerateRequest{Prompt: "cat"})
	if !errors.Is(err, context.Canceled) || ProviderErrorClass(err) != ErrorClassCallerCancelled {
		t.Fatalf("err=%v class=%q", err, ProviderErrorClass(err))
	}
}

func TestOpenAICompatibleProviderMarksInFlightLifecycleCancellationOutcomeUnknown(t *testing.T) {
	started := make(chan struct{}, 1)
	provider := NewOpenAICompatibleImagesProvider(ProviderConfig{
		BaseURL: "https://example.test",
		HTTPClient: &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
			started <- struct{}{}
			<-request.Context().Done()
			return nil, request.Context().Err()
		})},
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := provider.Generate(ctx, ProviderGenerateRequest{Prompt: "cat"})
		done <- err
	}()
	<-started
	cancel()
	err := <-done
	var failure *ProviderFailure
	if !errors.As(err, &failure) || failure.Class != ErrorClassCallerCancelled || !failure.OutcomeUnknown {
		t.Fatalf("failure=%+v err=%v", failure, err)
	}
}

func TestOpenAICompatibleProviderClassifiesHTTPFailures(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    int
		class     string
		retryable bool
		uncertain bool
	}{
		{name: "rate limited", status: http.StatusTooManyRequests, class: ErrorClassProviderRateLimited, retryable: true},
		{name: "unavailable", status: http.StatusServiceUnavailable, class: ErrorClassProviderUnavailable, retryable: true},
		{name: "rejected", status: http.StatusBadRequest, class: ErrorClassProviderRejected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, tc.name, tc.status)
			}))
			defer ts.Close()
			provider := NewOpenAICompatibleImagesProvider(ProviderConfig{BaseURL: ts.URL})
			_, err := provider.Generate(context.Background(), ProviderGenerateRequest{Prompt: "cat"})
			var failure *ProviderFailure
			if !errors.As(err, &failure) || failure.Class != tc.class || failure.Retryable != tc.retryable || failure.OutcomeUnknown != tc.uncertain {
				t.Fatalf("failure=%+v err=%v", failure, err)
			}
		})
	}
}

type failingRoundTripper struct{ err error }

func (r failingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) { return nil, r.err }

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type responseRoundTripper struct{ response *http.Response }

func (r responseRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return r.response, nil
}

type failingReadCloser struct{ err error }

func (r failingReadCloser) Read([]byte) (int, error) { return 0, r.err }
func (r failingReadCloser) Close() error             { return nil }

type contextReadCloser struct {
	ctx     context.Context
	started chan struct{}
}

func (r contextReadCloser) Read([]byte) (int, error) {
	select {
	case r.started <- struct{}{}:
	default:
	}
	<-r.ctx.Done()
	return 0, r.ctx.Err()
}
func (r contextReadCloser) Close() error { return nil }

func TestOpenAICompatibleProviderMarksResponseBodyCancellationOutcomeUnknown(t *testing.T) {
	started := make(chan struct{}, 1)
	provider := NewOpenAICompatibleImagesProvider(ProviderConfig{
		BaseURL: "https://example.test",
		HTTPClient: &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: contextReadCloser{ctx: request.Context(), started: started}}, nil
		})},
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := provider.Generate(ctx, ProviderGenerateRequest{Prompt: "cat"})
		done <- err
	}()
	<-started
	cancel()
	err := <-done
	var failure *ProviderFailure
	if !errors.As(err, &failure) || failure.Class != ErrorClassCallerCancelled || !failure.OutcomeUnknown {
		t.Fatalf("failure=%+v err=%v", failure, err)
	}
}

func TestOpenAICompatibleProviderMarksTransportFailureOutcomeUnknown(t *testing.T) {
	want := errors.New("connection reset")
	provider := NewOpenAICompatibleImagesProvider(ProviderConfig{
		BaseURL: "https://example.test", HTTPClient: &http.Client{Transport: failingRoundTripper{err: want}},
	})
	_, err := provider.Generate(context.Background(), ProviderGenerateRequest{Prompt: "cat"})
	var failure *ProviderFailure
	if !errors.Is(err, want) || !errors.As(err, &failure) || failure.Class != ErrorClassProviderOutcomeUnknown || failure.Retryable || !failure.OutcomeUnknown {
		t.Fatalf("failure=%+v err=%v", failure, err)
	}
}

func TestOpenAICompatibleProviderDoesNotRetryOutcomeUnknown(t *testing.T) {
	calls := 0
	provider := NewOpenAICompatibleImagesProvider(ProviderConfig{
		BaseURL:    "https://example.test",
		MaxRetries: 3,
		HTTPClient: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return nil, errors.New("connection reset")
		})},
	})
	_, err := provider.Generate(context.Background(), ProviderGenerateRequest{Prompt: "cat"})
	if ProviderErrorClass(err) != ErrorClassProviderOutcomeUnknown || calls != 1 {
		t.Fatalf("err=%v class=%q calls=%d", err, ProviderErrorClass(err), calls)
	}
}

func TestOpenAICompatibleProviderMarksMalformedSuccessOutcomeUnknown(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer ts.Close()
	provider := NewOpenAICompatibleImagesProvider(ProviderConfig{BaseURL: ts.URL})
	_, err := provider.Generate(context.Background(), ProviderGenerateRequest{Prompt: "cat"})
	var failure *ProviderFailure
	if !errors.As(err, &failure) || failure.Class != ErrorClassProviderOutcomeUnknown || failure.Retryable || !failure.OutcomeUnknown {
		t.Fatalf("failure=%+v err=%v", failure, err)
	}
}

func TestOpenAICompatibleProviderPreservesReceivedFailureStatusWhenBodyReadFails(t *testing.T) {
	provider := NewOpenAICompatibleImagesProvider(ProviderConfig{
		BaseURL: "https://example.test",
		HTTPClient: &http.Client{Transport: responseRoundTripper{response: &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Body:       failingReadCloser{err: io.ErrUnexpectedEOF},
			Header:     make(http.Header),
		}}},
	})
	_, err := provider.Generate(context.Background(), ProviderGenerateRequest{Prompt: "cat"})
	var failure *ProviderFailure
	if !errors.Is(err, ErrProviderResponse) || !errors.Is(err, io.ErrUnexpectedEOF) || !errors.As(err, &failure) || failure.Class != ErrorClassProviderRateLimited || !failure.Retryable || failure.OutcomeUnknown {
		t.Fatalf("failure=%+v err=%v", failure, err)
	}
}

func TestOpenAICompatibleProviderDoesNotRetryInvalidSixHundredsStatus(t *testing.T) {
	calls := 0
	provider := NewOpenAICompatibleImagesProvider(ProviderConfig{
		BaseURL:    "https://example.test",
		MaxRetries: 3,
		HTTPClient: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: 600, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("invalid status"))}, nil
		})},
	})
	_, err := provider.Generate(context.Background(), ProviderGenerateRequest{Prompt: "cat"})
	var failure *ProviderFailure
	if !errors.As(err, &failure) || failure.Class != ErrorClassProviderRejected || failure.Retryable || calls != 1 {
		t.Fatalf("failure=%+v err=%v calls=%d", failure, err, calls)
	}
}

func TestOpenAICompatibleProviderDoesNotClassifyLocalConfigurationAsProviderRejection(t *testing.T) {
	provider := NewOpenAICompatibleImagesProvider(ProviderConfig{BaseURL: "not a URL"})
	_, err := provider.Generate(context.Background(), ProviderGenerateRequest{Prompt: "cat"})
	if !errors.Is(err, ErrProviderResponse) || ProviderErrorClass(err) != "" {
		t.Fatalf("err=%v class=%q", err, ProviderErrorClass(err))
	}
}
