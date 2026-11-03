package provider

import (
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/config"
)

func TestContinuationRoundTripAndSizeLimit(t *testing.T) {
	value := Continuation{
		Version: ContinuationVersion, Protocol: config.ProviderProtocolOpenAIResponses,
		Provider: "primary", EndpointID: EndpointID("https://example.com/v1"), Model: "gpt-test",
		OpaqueItems: []OpaqueItem{{Type: "reasoning", ID: "rs_1", EncryptedContent: "ciphertext"}},
	}
	data, err := EncodeContinuation(value)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeContinuation(data)
	if err != nil {
		t.Fatal(err)
	}
	if !MatchesKey(decoded, ContinuationKey{
		Protocol: value.Protocol, Provider: value.Provider, EndpointID: value.EndpointID, Model: value.Model,
	}) || decoded.OpaqueItems[0].EncryptedContent != "ciphertext" {
		t.Fatalf("decoded = %+v", decoded)
	}

	value.OpaqueItems[0].EncryptedContent = strings.Repeat("x", MaxContinuationEncodedSize)
	if _, err := EncodeContinuation(value); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized continuation error = %v", err)
	}
}
