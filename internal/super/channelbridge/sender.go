package channelbridge

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	ch "github.com/konglong87/super-niuma/internal/channel"
	"github.com/konglong87/super-niuma/internal/super/control"
	"io"
	"net/http"
	"strconv"
	"unicode/utf8"
)

func (b *Bridge) Send(ctx context.Context, target control.Target, id, text string) (control.Receipt, error) {
	if !control.ValidID(target.ID) || !safeToken(target.Chat, 256) || len(target.Thread) > 256 || len(target.Recipient) > 256 || len(target.ReplyTo) > 256 || ctx == nil || !control.ValidID(id) || len(text) == 0 || len(text) > MaxNoticeBytes || !utf8.ValidString(text) {
		return control.Receipt{}, notAccepted("invalid_notice")
	}
	if err := ctx.Err(); err != nil {
		return control.Receipt{}, notAccepted("cancelled_before_send")
	}
	if b.options.AuthorizeTarget == nil || b.options.AuthorizeTarget(ctx, target) != nil {
		return control.Receipt{}, notAccepted("target_denied")
	}
	a, err := b.account(target.Account, target.Provider)
	if err != nil {
		return control.Receipt{}, notAccepted("account_denied")
	}
	b.options.Logger.DebugContext(ctx, "Super notice dispatch", "provider", a.Provider)
	if a.Provider == control.ProviderWebhook {
		return b.sendWebhook(ctx, a, target, id, text)
	}
	b.mu.RLock()
	adapter := b.adapters[a.ID]
	activeConfig := b.activeConfig[a.ID]
	b.mu.RUnlock()
	if adapter == nil || activeConfig != b.configurationFingerprint(a) {
		return control.Receipt{}, notAccepted("transport_unavailable")
	}
	result, err := adapter.Deliver(ctx, ch.OutboundOperation{Operation: ch.DeliveryCreate, Message: ch.OutboundMessage{Provider: ch.ProviderFeishu, AccountID: a.ID, ExternalChatID: target.Chat, ExternalThreadID: target.Thread, ReplyToMessageID: target.ReplyTo, Kind: ch.MessageKindText, Text: text, IdempotencyKey: id}})
	if err != nil {
		var typed *control.SendError
		if errors.As(err, &typed) {
			return control.Receipt{}, &control.SendError{Code: typed.Code, Retryable: typed.Retryable, DefinitelyNotAccepted: typed.DefinitelyNotAccepted}
		}
		return control.Receipt{}, unknown("provider_delivery_unknown")
	}
	if result.MessageID == "" || result.ChatID != target.Chat {
		return control.Receipt{}, unknown("receipt_invalid")
	}
	return control.Receipt{MessageID: result.MessageID, Chat: result.ChatID, RequestID: result.RequestID, DeliveredAtMS: b.options.Now().UnixMilli()}, nil
}
func (b *Bridge) sendWebhook(ctx context.Context, a Account, t control.Target, id, text string) (control.Receipt, error) {
	u, err := b.authorizedURL(a.WebhookURL)
	if err != nil {
		return control.Receipt{}, notAccepted("target_url_denied")
	}
	secret := b.options.Getenv(a.SecretEnv)
	if len(secret) < 32 {
		return control.Receipt{}, notAccepted("credentials_unavailable")
	}
	payload, _ := json.Marshal(struct {
		ID        string `json:"message_id"`
		Text      string `json:"text"`
		Chat      string `json:"chat"`
		Thread    string `json:"thread"`
		Recipient string `json:"recipient"`
	}{id, text, t.Chat, t.Thread, t.Recipient})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(payload))
	if err != nil {
		return control.Receipt{}, notAccepted("request_invalid")
	}
	nonceBytes := make([]byte, 16)
	if _, err = rand.Read(nonceBytes); err != nil {
		return control.Receipt{}, notAccepted("nonce_unavailable")
	}
	nonce := hex.EncodeToString(nonceBytes)
	timestamp := strconv.FormatInt(b.options.Now().Unix(), 10)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", id)
	request.Header.Set(HeaderVersion, ProtocolVersion)
	request.Header.Set(HeaderAccount, a.ID)
	request.Header.Set(HeaderKeyID, a.ID)
	request.Header.Set(HeaderTimestamp, timestamp)
	request.Header.Set(HeaderNonce, nonce)
	request.Header.Set(HeaderSignature, hex.EncodeToString(signatureMAC(secret, http.MethodPost, u.Path, a.ID, a.ID, timestamp, nonce, payload)))
	response, err := b.client.Do(request)
	if err != nil {
		return control.Receipt{}, unknown("delivery_unknown")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return control.Receipt{}, unknown("provider_response_unknown")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, MaxBodyBytes+1))
	if err != nil || len(data) > MaxBodyBytes {
		return control.Receipt{}, unknown("receipt_invalid")
	}
	if !uniqueReceiptKeys(data) {
		return control.Receipt{}, unknown("receipt_invalid")
	}
	var receipt control.Receipt
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&receipt) != nil {
		return control.Receipt{}, unknown("receipt_invalid")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || !safeToken(receipt.MessageID, 256) || receipt.Chat != t.Chat || receipt.DeliveredAtMS <= 0 {
		return control.Receipt{}, unknown("receipt_invalid")
	}
	return receipt, nil
}
func notAccepted(code string) error {
	return &control.SendError{Code: code, DefinitelyNotAccepted: true}
}
func unknown(code string) error { return &control.SendError{Code: code} }

func uniqueReceiptKeys(data []byte) bool {
	d := json.NewDecoder(bytes.NewReader(data))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return false
	}
	seen := map[string]bool{}
	for d.More() {
		token, err = d.Token()
		if err != nil {
			return false
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			return false
		}
		seen[key] = true
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return false
		}
	}
	token, err = d.Token()
	if err != nil || token != json.Delim('}') {
		return false
	}
	_, err = d.Token()
	return err == io.EOF
}
