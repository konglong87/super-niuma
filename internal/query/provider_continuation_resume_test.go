package query

import (
	"encoding/json"
	"testing"

	"github.com/konglong87/super-niuma/internal/config"
	providerstate "github.com/konglong87/super-niuma/internal/provider"
	"github.com/konglong87/super-niuma/internal/session"
)

func TestMessagesFromTranscriptRestoresProviderContinuation(t *testing.T) {
	continuation := providerstate.Continuation{
		Version: providerstate.ContinuationVersion, Protocol: config.ProviderProtocolOpenAIResponses,
		Provider: "primary", EndpointID: providerstate.EndpointID("https://example.com/v1"), Model: "gpt-test",
		OpaqueItems: []providerstate.OpaqueItem{{Type: "reasoning", ID: "rs_1", EncryptedContent: "cipher"}},
	}
	metadata, err := providerstate.EncodeContinuation(continuation)
	if err != nil {
		t.Fatal(err)
	}
	entries := []session.Entry{
		{Type: "message", Role: "user", Content: "start"},
		{Type: session.EntryTypeProviderContinuation, Metadata: json.RawMessage(metadata)},
		{Type: "tool_call", ToolID: "call_1", ToolName: "Read", Content: `{"file_path":"README.md"}`},
		{Type: "tool_result", ToolID: "call_1", ToolName: "Read", Content: "contents"},
	}
	messages := MessagesFromTranscript(entries)
	if len(messages) != 3 || len(messages[1].Content) != 2 {
		t.Fatalf("messages = %+v", messages)
	}
	block := messages[1].Content[0]
	if block.Type != blockTypeProviderContinuation || block.Continuation == nil || block.Continuation.OpaqueItems[0].EncryptedContent != "cipher" {
		t.Fatalf("continuation block = %+v", block)
	}
}
