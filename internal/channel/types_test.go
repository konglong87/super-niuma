package channel

import (
	"encoding/json"
	"testing"
)

func TestProviderValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		provider Provider
		want     bool
	}{
		{name: "dingtalk", provider: ProviderDingTalk, want: true},
		{name: "feishu", provider: ProviderFeishu, want: true},
		{name: "unknown", provider: Provider("unknown"), want: false},
		{name: "empty", provider: Provider(""), want: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.provider.Valid(); got != tt.want {
				t.Fatalf("Valid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCapabilitiesSupportsStreamingCardOnlyWithUpdate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cap  Capabilities
		want bool
	}{
		{
			name: "full streaming support",
			cap: Capabilities{
				FinalCard:     true,
				StreamingCard: true,
				MessageUpdate: true,
			},
			want: true,
		},
		{
			name: "provider claims streaming without update",
			cap: Capabilities{
				FinalCard:     true,
				StreamingCard: true,
			},
			want: false,
		},
		{
			name: "final card only",
			cap: Capabilities{
				FinalCard: true,
			},
			want: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.cap.SupportsStreamingCard(); got != tt.want {
				t.Fatalf("SupportsStreamingCard() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestChatTypeValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value ChatType
		want  bool
	}{
		{name: "p2p", value: ChatTypeP2P, want: true},
		{name: "group", value: ChatTypeGroup, want: true},
		{name: "empty", value: ChatType(""), want: false},
		{name: "unknown", value: ChatType("unknown"), want: false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.value.Valid(); got != tt.want {
				t.Fatalf("Valid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMessageKindValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value MessageKind
		want  bool
	}{
		{name: "text", value: MessageKindText, want: true},
		{name: "markdown", value: MessageKindMarkdown, want: true},
		{name: "card", value: MessageKindCard, want: true},
		{name: "image", value: MessageKindImage, want: true},
		{name: "empty", value: MessageKind(""), want: false},
		{name: "unknown", value: MessageKind("unknown"), want: false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.value.Valid(); got != tt.want {
				t.Fatalf("Valid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDeliveryOperationValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value DeliveryOperation
		want  bool
	}{
		{name: "create", value: DeliveryCreate, want: true},
		{name: "update", value: DeliveryUpdate, want: true},
		{name: "recall", value: DeliveryRecall, want: true},
		{name: "empty", value: DeliveryOperation(""), want: false},
		{name: "unknown", value: DeliveryOperation("unknown"), want: false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.value.Valid(); got != tt.want {
				t.Fatalf("Valid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCapabilitiesJSONUsesSnakeCaseKeys(t *testing.T) {
	t.Parallel()

	data, err := json.Marshal(Capabilities{FinalCard: true, MaxMessageBytes: 42})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	for _, key := range []string{"final_card", "max_message_bytes"} {
		if _, ok := fields[key]; !ok {
			t.Fatalf("JSON missing key %q: %s", key, data)
		}
	}
	for _, key := range []string{"FinalCard", "MaxMessageBytes"} {
		if _, ok := fields[key]; ok {
			t.Fatalf("JSON exposed Go field name %q: %s", key, data)
		}
	}
}
