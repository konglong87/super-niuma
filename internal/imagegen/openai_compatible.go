package imagegen

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	imagepkg "image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"path"
	"strings"
	"time"
)

type ProviderConfig struct {
	BaseURL          string
	APIKey           string
	AuthToken        string
	ImageProtocol    string
	AllowRemoteURLs  bool
	RemoteImageHosts []string
	HTTPClient       *http.Client
	Timeout          time.Duration
	MaxRetries       int
}

var (
	ErrProviderTimeout  = errors.New("image provider timeout")
	ErrProviderResponse = errors.New("image provider response error")
	errProviderTimer    = errors.New("image provider local timer expired")
)

// ProviderFailure carries stable scheduling semantics while preserving the
// wrapped error for errors.Is compatibility.
type ProviderFailure struct {
	Class             string
	Retryable         bool
	OutcomeUnknown    bool
	ProviderRequestID string
	Err               error
}

func (e *ProviderFailure) Error() string {
	if e == nil || e.Err == nil {
		return "image provider failure"
	}
	return e.Err.Error()
}

func (e *ProviderFailure) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func ProviderErrorClass(err error) string {
	var failure *ProviderFailure
	if errors.As(err, &failure) {
		return failure.Class
	}
	switch {
	case errors.Is(err, context.Canceled):
		return ErrorClassCallerCancelled
	case errors.Is(err, context.DeadlineExceeded):
		return ErrorClassCallerDeadlineExceeded
	case errors.Is(err, ErrProviderTimeout):
		return ErrorClassProviderTimeout
	default:
		return ""
	}
}

func ProviderErrorRequestID(err error) string {
	var failure *ProviderFailure
	if errors.As(err, &failure) {
		return failure.ProviderRequestID
	}
	return ""
}

func providerFailure(class string, err error, retryable, outcomeUnknown bool) error {
	return &ProviderFailure{Class: class, Retryable: retryable, OutcomeUnknown: outcomeUnknown, Err: err}
}

type OpenAICompatibleImagesProvider struct {
	cfg    ProviderConfig
	client *http.Client
}

func NewOpenAICompatibleImagesProvider(cfg ProviderConfig) *OpenAICompatibleImagesProvider {
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 0
	}
	if cfg.MaxRetries > 3 {
		cfg.MaxRetries = 3
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 180 * time.Second
	}
	c := cfg.HTTPClient
	if c == nil {
		c = &http.Client{Timeout: cfg.Timeout}
	}
	return &OpenAICompatibleImagesProvider{cfg: cfg, client: c}
}

func (p *OpenAICompatibleImagesProvider) endpoint(resource string) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(p.cfg.BaseURL), "/")
	if base == "" {
		return "", fmt.Errorf("%w: base URL is required", ErrProviderResponse)
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("%w: invalid base URL", ErrProviderResponse)
	}
	u.Path = path.Join(u.Path, resource)
	return u.String(), nil
}

func (p *OpenAICompatibleImagesProvider) request(ctx context.Context, method, resource, contentType string, body io.Reader) (ProviderImage, error) {
	if err := ctx.Err(); err != nil {
		return ProviderImage{}, classifyCallerContextError(err)
	}
	ep, err := p.endpoint(resource)
	if err != nil {
		return ProviderImage{}, err
	}
	payload, err := io.ReadAll(body)
	if err != nil {
		return ProviderImage{}, err
	}
	var last error
	for attempt := 0; attempt <= p.cfg.MaxRetries; attempt++ {
		reqCtx := ctx
		cancel := func() {}
		if p.cfg.Timeout > 0 {
			reqCtx, cancel = context.WithTimeoutCause(ctx, p.cfg.Timeout, errProviderTimer)
		}
		req, err := http.NewRequestWithContext(reqCtx, method, ep, bytes.NewReader(payload))
		if err != nil {
			cancel()
			return ProviderImage{}, err
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		if p.cfg.AuthToken != "" {
			req.Header.Set("Authorization", "Bearer "+p.cfg.AuthToken)
		} else if p.cfg.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
		}
		resp, err := p.client.Do(req)
		if err != nil {
			cause := context.Cause(reqCtx)
			cancel()
			switch {
			case ctx.Err() != nil:
				return ProviderImage{}, markProviderOutcomeUnknown(classifyCallerContextError(ctx.Err()))
			case errors.Is(cause, errProviderTimer):
				return ProviderImage{}, providerFailure(ErrorClassProviderTimeout, ErrProviderTimeout, false, true)
			default:
				last = providerFailure(ErrorClassProviderOutcomeUnknown, err, false, true)
			}
		} else {
			requestID := firstNonBlank(resp.Header.Get("X-Request-ID"), resp.Header.Get("OpenAI-Request-ID"), resp.Header.Get("Request-ID"))
			data, readErr := io.ReadAll(io.LimitReader(resp.Body, 60*1024*1024))
			resp.Body.Close()
			cause := context.Cause(reqCtx)
			cancel()
			if readErr != nil {
				if ctx.Err() != nil {
					return ProviderImage{}, markProviderOutcomeUnknown(classifyCallerContextError(ctx.Err()))
				}
				if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
					last = withProviderRequestID(providerResponseFailure(resp.StatusCode, fmt.Errorf("%w: status %d: response body read: %w", ErrProviderResponse, resp.StatusCode, readErr)), requestID)
				} else if errors.Is(cause, errProviderTimer) {
					return ProviderImage{}, providerFailure(ErrorClassProviderTimeout, ErrProviderTimeout, false, true)
				} else {
					last = withProviderRequestID(providerFailure(ErrorClassProviderOutcomeUnknown, readErr, false, true), requestID)
				}
			} else if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				image, decodeErr := p.decodeProviderImage(ctx, data, resp.Header.Get("Content-Type"))
				if decodeErr != nil {
					return ProviderImage{}, withProviderRequestID(providerFailure(ErrorClassProviderOutcomeUnknown, decodeErr, false, true), requestID)
				}
				image.ProviderRequestID = requestID
				return image, nil
			} else {
				responseErr := fmt.Errorf("%w: status %d: %s", ErrProviderResponse, resp.StatusCode, strings.TrimSpace(string(data)))
				last = withProviderRequestID(providerResponseFailure(resp.StatusCode, responseErr), requestID)
				if !providerFailureRetryable(last) {
					return ProviderImage{}, last
				}
			}
		}
		if attempt < p.cfg.MaxRetries && providerFailureRetryable(last) {
			select {
			case <-ctx.Done():
				return ProviderImage{}, classifyCallerContextError(ctx.Err())
			case <-time.After(time.Duration(1<<attempt) * 100 * time.Millisecond):
			}
		} else if last != nil {
			return ProviderImage{}, last
		}
	}
	return ProviderImage{}, last
}

func markProviderOutcomeUnknown(err error) error {
	var failure *ProviderFailure
	if !errors.As(err, &failure) {
		return err
	}
	copy := *failure
	copy.OutcomeUnknown = true
	return &copy
}

func withProviderRequestID(err error, requestID string) error {
	var failure *ProviderFailure
	if strings.TrimSpace(requestID) == "" || !errors.As(err, &failure) {
		return err
	}
	copy := *failure
	copy.ProviderRequestID = strings.TrimSpace(requestID)
	return &copy
}

func providerResponseFailure(status int, err error) error {
	switch {
	case status == http.StatusTooManyRequests:
		return providerFailure(ErrorClassProviderRateLimited, err, true, false)
	case status >= http.StatusInternalServerError && status <= 599:
		return providerFailure(ErrorClassProviderUnavailable, err, true, false)
	default:
		return providerFailure(ErrorClassProviderRejected, err, false, false)
	}
}

func providerFailureRetryable(err error) bool {
	var failure *ProviderFailure
	return errors.As(err, &failure) && failure.Retryable
}

func classifyCallerContextError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return providerFailure(ErrorClassCallerDeadlineExceeded, err, false, false)
	}
	return providerFailure(ErrorClassCallerCancelled, err, false, false)
}

func (p *OpenAICompatibleImagesProvider) Generate(ctx context.Context, in ProviderGenerateRequest) (ProviderImage, error) {
	if strings.EqualFold(strings.TrimSpace(p.cfg.ImageProtocol), ImageProtocolAgnes) {
		size := firstNonBlank(in.Resolution, in.Size, "1K")
		body, _ := json.Marshal(map[string]any{"prompt": in.Prompt, "model": in.Model, "size": size, "ratio": in.AspectRatio, "return_base64": true})
		return p.request(ctx, http.MethodPost, "images/generations", "application/json", bytes.NewReader(body))
	}
	payload := map[string]any{"prompt": in.Prompt, "model": in.Model, "quality": in.Quality, "size": in.Size, "output_format": in.OutputFormat, "background": in.Background, "response_format": "b64_json", "n": 1}
	if strings.EqualFold(strings.TrimSpace(p.cfg.ImageProtocol), ImageProtocolSenseNova) && in.Watermark != nil {
		payload["watermark"] = *in.Watermark
	}
	body, _ := json.Marshal(payload)
	return p.request(ctx, http.MethodPost, "images/generations", "application/json", bytes.NewReader(body))
}

func (p *OpenAICompatibleImagesProvider) Edit(ctx context.Context, in ProviderEditRequest) (ProviderImage, error) {
	if strings.EqualFold(strings.TrimSpace(p.cfg.ImageProtocol), ImageProtocolAgnes) {
		data, err := io.ReadAll(in.Image)
		if err != nil {
			return ProviderImage{}, err
		}
		mediaType := strings.TrimSpace(in.ImageType)
		if mediaType == "" {
			mediaType = "image/png"
		}
		size := firstNonBlank(in.Resolution, in.Size, "1K")
		extra := map[string]any{"image": []string{"data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(data)}, "response_format": "b64_json"}
		body, _ := json.Marshal(map[string]any{"prompt": in.Prompt, "model": in.Model, "size": size, "ratio": in.AspectRatio, "extra_body": extra})
		return p.request(ctx, http.MethodPost, "images/generations", "application/json", bytes.NewReader(body))
	}
	buf := bytes.NewBuffer(nil)
	w := multipart.NewWriter(buf)
	fields := map[string]string{"prompt": in.Prompt, "model": in.Model, "quality": in.Quality, "size": in.Size, "output_format": in.OutputFormat, "background": in.Background, "response_format": "b64_json"}
	for k, v := range fields {
		if v != "" {
			_ = w.WriteField(k, v)
		}
	}
	name := in.ImageName
	if name == "" {
		name = "image.png"
	}
	part, err := createImagePart(w, "image", name, in.ImageType)
	if err != nil {
		return ProviderImage{}, err
	}
	if _, err = io.Copy(part, in.Image); err != nil {
		return ProviderImage{}, err
	}
	if in.Mask != nil {
		mn := in.MaskName
		if mn == "" {
			mn = "mask.png"
		}
		mp, e := createImagePart(w, "mask", mn, in.MaskType)
		if e != nil {
			return ProviderImage{}, e
		}
		if _, e = io.Copy(mp, in.Mask); e != nil {
			return ProviderImage{}, e
		}
	}
	if err = w.Close(); err != nil {
		return ProviderImage{}, err
	}
	return p.request(ctx, http.MethodPost, "images/edits", w.FormDataContentType(), bytes.NewReader(buf.Bytes()))
}

func createImagePart(w *multipart.Writer, field, name, mediaType string) (io.Writer, error) {
	name = strings.ReplaceAll(strings.TrimSpace(name), "\"", "")
	if name == "" {
		name = field + ".png"
	}
	mediaType = strings.TrimSpace(mediaType)
	if mediaType == "" {
		mediaType = "image/png"
	}
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, field, name))
	header.Set("Content-Type", mediaType)
	return w.CreatePart(header)
}

func decodeProviderImage(data []byte, contentType string) (ProviderImage, error) {
	var resp struct {
		Data []struct {
			B64JSON string `json:"b64_json"`
			URL     string `json:"url"`
		} `json:"data"`
	}
	if json.Unmarshal(data, &resp) == nil && len(resp.Data) > 0 {
		item := resp.Data[0]
		if item.B64JSON != "" {
			b, err := base64.StdEncoding.DecodeString(item.B64JSON)
			if err != nil {
				return ProviderImage{}, fmt.Errorf("%w: invalid base64", ErrProviderResponse)
			}
			return ProviderImage{Data: b, MediaType: formatMediaType(b, "image/png"), URL: item.URL}, nil
		}
		if item.URL != "" {
			return ProviderImage{}, fmt.Errorf("%w: URL-only image responses are unsupported", ErrProviderResponse)
		}
	}
	if len(data) == 0 {
		return ProviderImage{}, fmt.Errorf("%w: empty response", ErrProviderResponse)
	}
	if strings.HasPrefix(contentType, "image/") {
		return ProviderImage{Data: data, MediaType: formatMediaType(data, contentType)}, nil
	}
	return ProviderImage{}, fmt.Errorf("%w: missing image data", ErrProviderResponse)
}

func (p *OpenAICompatibleImagesProvider) decodeProviderImage(ctx context.Context, data []byte, contentType string) (ProviderImage, error) {
	image, err := decodeProviderImage(data, contentType)
	if err == nil || !p.cfg.AllowRemoteURLs {
		return image, err
	}
	var resp struct {
		Data []struct {
			URL string `json:"url"`
		} `json:"data"`
	}
	if json.Unmarshal(data, &resp) != nil || len(resp.Data) == 0 || strings.TrimSpace(resp.Data[0].URL) == "" {
		return image, err
	}
	return p.fetchRemoteImage(ctx, resp.Data[0].URL)
}

func (p *OpenAICompatibleImagesProvider) fetchRemoteImage(ctx context.Context, rawURL string) (ProviderImage, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Hostname() == "" {
		return ProviderImage{}, fmt.Errorf("%w: remote image URL must use https", ErrProviderResponse)
	}
	if !p.remoteImageHostAllowed(u) {
		return ProviderImage{}, fmt.Errorf("%w: remote image host is not allowed", ErrProviderResponse)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return ProviderImage{}, err
	}
	client := *p.client
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 3 || !p.remoteImageHostAllowed(request.URL) {
			return fmt.Errorf("%w: remote image redirect is not allowed", ErrProviderResponse)
		}
		return nil
	}
	response, err := client.Do(request)
	if err != nil {
		return ProviderImage{}, providerFailure(ErrorClassProviderOutcomeUnknown, err, false, true)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ProviderImage{}, fmt.Errorf("%w: remote image status %d", ErrProviderResponse, response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 60*1024*1024+1))
	if err != nil {
		return ProviderImage{}, err
	}
	if len(data) > 60*1024*1024 {
		return ProviderImage{}, fmt.Errorf("%w: remote image too large", ErrProviderResponse)
	}
	image, err := decodeProviderImage(data, response.Header.Get("Content-Type"))
	if err != nil {
		return ProviderImage{}, err
	}
	if _, _, err := imagepkg.DecodeConfig(bytes.NewReader(image.Data)); err != nil {
		return ProviderImage{}, fmt.Errorf("%w: remote response is not a valid image", ErrProviderResponse)
	}
	return image, nil
}

func (p *OpenAICompatibleImagesProvider) remoteImageHostAllowed(u *url.URL) bool {
	if u == nil || !strings.EqualFold(u.Scheme, "https") || u.Hostname() == "" {
		return false
	}
	base, err := url.Parse(strings.TrimSpace(p.cfg.BaseURL))
	if err == nil && strings.EqualFold(u.Hostname(), base.Hostname()) {
		return true
	}
	for _, host := range p.cfg.RemoteImageHosts {
		if strings.EqualFold(strings.TrimSpace(host), u.Hostname()) {
			return true
		}
	}
	return false
}

func formatMediaType(data []byte, fallback string) string {
	if len(data) >= 8 && bytes.Equal(data[:8], []byte{137, 80, 78, 71, 13, 10, 26, 10}) {
		return "image/png"
	}
	if len(data) >= 2 && data[0] == 0xff && data[1] == 0xd8 {
		return "image/jpeg"
	}
	if len(data) >= 4 && string(data[:4]) == "RIFF" {
		return "image/webp"
	}
	return fallback
}
