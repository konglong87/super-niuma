package anthropic

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	sdkanthropic "github.com/anthropics/anthropic-sdk-go"
	openairesponses "github.com/openai/openai-go/v3"
	openai "github.com/sashabaranov/go-openai"
)

func TestCanFallbackAfterQuotaExhaustion(t *testing.T) {
	type build func(status int, message string) error
	protocols := map[string]build{
		"openai-chat": func(status int, message string) error {
			return fmt.Errorf("wrapped: %w", &openai.APIError{HTTPStatusCode: status, Message: message})
		},
		"openai-responses": func(status int, message string) error {
			return fmt.Errorf("wrapped: %w", &openairesponses.Error{StatusCode: status, Message: message})
		},
		"anthropic": func(status int, _ string) error {
			return fmt.Errorf("wrapped: %w", &sdkanthropic.Error{StatusCode: status})
		},
	}
	cases := []struct {
		name    string
		status  int
		message string
		want    bool
		skipSDK bool // the Anthropic SDK error carries the body only via raw JSON
	}{
		{name: "402 payment required", status: 402, message: "", want: true},
		{name: "400 insufficient_quota", status: 400, message: "Error code: insufficient_quota", want: true, skipSDK: true},
		{name: "403 exceeded current quota", status: 403, message: "You exceeded your current quota, please check your plan", want: true, skipSDK: true},
		{name: "400 insufficient balance", status: 400, message: "Insufficient Balance", want: true, skipSDK: true},
		{name: "400 insufficient credits", status: 400, message: "insufficient credits", want: true, skipSDK: true},
		{name: "403 chinese balance", status: 403, message: "账户余额不足，请充值", want: true, skipSDK: true},
		{name: "400 chinese quota used up", status: 400, message: "今日额度已用完", want: true, skipSDK: true},
		{name: "401 quota text still not switchable", status: 401, message: "insufficient_quota", want: false, skipSDK: true},
		{name: "plain 400", status: 400, message: "max_tokens must be positive", want: false},
		{name: "plain 401", status: 401, message: "invalid api key", want: false},
		{name: "plain 403", status: 403, message: "forbidden", want: false},
	}
	for protocol, newErr := range protocols {
		for _, tc := range cases {
			if protocol == "anthropic" && tc.skipSDK {
				continue
			}
			t.Run(protocol+"/"+tc.name, func(t *testing.T) {
				if got := canFallbackAfterError(context.Background(), newErr(tc.status, tc.message)); got != tc.want {
					t.Fatalf("canFallbackAfterError(%d, %q) = %v, want %v", tc.status, tc.message, got, tc.want)
				}
			})
		}
	}
}

func TestQuotaFallbackDeclinedAfterCallerCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := &openai.APIError{HTTPStatusCode: http.StatusPaymentRequired, Message: "insufficient credits"}
	if canFallbackAfterError(ctx, err) {
		t.Fatal("fallback must not trigger once the caller cancelled")
	}
	if canFallbackAfterError(context.Background(), errors.Join(context.Canceled, err)) {
		t.Fatal("a cancellation in the error chain must not trigger fallback")
	}
}
