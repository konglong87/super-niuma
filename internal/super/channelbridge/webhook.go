package channelbridge

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/konglong87/super-niuma/internal/super/control"
	"github.com/konglong87/super-niuma/internal/super/router"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type webhookBody struct {
	EventID   string `json:"event_id"`
	MessageID string `json:"message_id"`
	Chat      string `json:"chat"`
	Thread    string `json:"thread"`
	Sender    string `json:"sender"`
	Text      string `json:"text"`
}

func (b *Bridge) webhook(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	fail := func(status int, code string) {
		writeDecision(w, status, router.Decision{Action: router.ActionRejected, Code: code})
		b.options.Logger.DebugContext(r.Context(), "Super webhook rejected", "code", code)
	}
	if r.Method != http.MethodPost || r.URL.RawQuery != "" || r.URL.RawPath != "" || r.URL.EscapedPath() != r.URL.Path || !strings.HasPrefix(r.URL.Path, WebhookPrefix) {
		fail(http.StatusBadRequest, "invalid_request")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, WebhookPrefix)
	a, err := b.account(id, control.ProviderWebhook)
	if err != nil {
		fail(http.StatusForbidden, "account_denied")
		return
	}
	version, ok1 := singleHeader(r, HeaderVersion)
	account, ok2 := singleHeader(r, HeaderAccount)
	key, ok3 := singleHeader(r, HeaderKeyID)
	timestamp, ok4 := singleHeader(r, HeaderTimestamp)
	nonce, ok5 := singleHeader(r, HeaderNonce)
	signature, ok6 := singleHeader(r, HeaderSignature)
	if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 || !ok6 || version != ProtocolVersion || account != a.ID || key != a.ID || !safeToken(nonce, 128) || len(signature) != 64 {
		fail(http.StatusUnauthorized, "authentication_failed")
		return
	}
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || strconv.FormatInt(ts, 10) != timestamp {
		fail(http.StatusUnauthorized, "authentication_failed")
		return
	}
	now := b.options.Now().Unix()
	window := int64(SignatureWindow.Seconds())
	if ts < now-window || ts > now+window {
		fail(http.StatusUnauthorized, "authentication_failed")
		return
	}
	secret := b.options.Getenv(a.SecretEnv)
	if len(secret) < 32 {
		fail(http.StatusServiceUnavailable, "credentials_unavailable")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
	if err != nil {
		fail(http.StatusRequestEntityTooLarge, "body_rejected")
		return
	}
	supplied, err := hex.DecodeString(signature)
	if err != nil || !hmac.Equal(supplied, signatureMAC(secret, r.Method, r.URL.Path, a.ID, key, timestamp, nonce, body)) {
		fail(http.StatusUnauthorized, "authentication_failed")
		return
	}
	// Only after authentication do we parse a small string-only DTO. No trusted
	// routing flags, identity overrides, grants or system origin are body fields.
	input, err := decodeWebhookBody(body)
	if err != nil {
		fail(http.StatusBadRequest, "body_rejected")
		return
	}
	envelope := router.Envelope{Provider: control.ProviderWebhook, Account: a.ID, EventID: input.EventID, MessageID: input.MessageID, Chat: input.Chat, Thread: input.Thread, Sender: input.Sender, Text: input.Text, Nonce: nonce, Authenticated: true, ThreadKnown: true, Mentioned: strings.HasPrefix(strings.TrimSpace(input.Text), "/")}
	if a.BotIDEnv != "" {
		bot := b.options.Getenv(a.BotIDEnv)
		if !safeToken(bot, 256) {
			fail(http.StatusServiceUnavailable, "bot_identity_unknown")
			return
		}
		envelope.Self = input.Sender == bot
		envelope.Bot = envelope.Self
	}
	decision, err := b.options.Handler.Handle(r.Context(), envelope)
	status := http.StatusOK
	if err != nil {
		switch {
		case errors.Is(err, control.ErrDenied), errors.Is(err, control.ErrNotFound):
			status = http.StatusForbidden
		case errors.Is(err, control.ErrConflict):
			status = http.StatusConflict
		case errors.Is(err, control.ErrInvalid):
			status = http.StatusBadRequest
		default:
			status = http.StatusServiceUnavailable
		}
	}
	writeDecision(w, status, decision)
	b.options.Logger.DebugContext(r.Context(), "Super webhook routed", "action", decision.Action, "code", decision.Code)
}
func signatureMAC(secret, method, path, account, key, timestamp, nonce string, body []byte) []byte {
	digest := sha256.Sum256(body)
	canonical := strings.Join([]string{ProtocolVersion, strings.ToUpper(method), path, account + "/" + key, timestamp, nonce, hex.EncodeToString(digest[:])}, "\n")
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(canonical))
	return mac.Sum(nil)
}
func singleHeader(r *http.Request, name string) (string, bool) {
	values := r.Header.Values(name)
	if len(values) != 1 {
		return "", false
	}
	return values[0], true
}
func safeToken(value string, max int) bool {
	return value != "" && len(value) <= max && strings.TrimSpace(value) == value && utf8.ValidString(value) && strings.IndexFunc(value, unicode.IsControl) < 0
}
func decodeWebhookBody(data []byte) (webhookBody, error) {
	var out webhookBody
	if !utf8.Valid(data) {
		return out, control.ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(data))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return out, control.ErrInvalid
	}
	seen := map[string]bool{}
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return out, control.ErrInvalid
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			return out, control.ErrInvalid
		}
		seen[key] = true
		var value string
		if d.Decode(&value) != nil {
			return out, control.ErrInvalid
		}
		switch key {
		case "event_id":
			out.EventID = value
		case "message_id":
			out.MessageID = value
		case "chat":
			out.Chat = value
		case "thread":
			out.Thread = value
		case "sender":
			out.Sender = value
		case "text":
			out.Text = value
		default:
			return out, control.ErrInvalid
		}
	}
	if token, err = d.Token(); err != nil || token != json.Delim('}') {
		return out, control.ErrInvalid
	}
	if _, err = d.Token(); err != io.EOF {
		return out, control.ErrInvalid
	}
	for _, field := range []string{"event_id", "message_id", "chat", "thread", "sender", "text"} {
		if !seen[field] {
			return out, control.ErrInvalid
		}
	}
	return out, nil
}
func writeDecision(w http.ResponseWriter, status int, d router.Decision) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(d)
}
