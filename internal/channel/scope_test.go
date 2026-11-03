package channel

import (
	"fmt"
	"strings"
	"testing"
)

func TestNewScope(t *testing.T) {
	t.Parallel()

	t.Run("normalizes fields and root thread", func(t *testing.T) {
		scope, err := NewScope(ProviderDingTalk, " account ", " chat ", "   ")
		if err != nil {
			t.Fatalf("NewScope() error = %v", err)
		}
		if scope.Provider != ProviderDingTalk || scope.AccountID != "account" || scope.ChatID != "chat" || scope.ThreadID != "" {
			t.Fatalf("unexpected normalized scope: %#v", scope)
		}
		if scope.Key != "dingtalk\x1faccount\x1fchat\x1f" {
			t.Fatalf("Key = %q, want canonical key", scope.Key)
		}
		if len(scope.Hash) != 32 || scope.HashHex() == "" {
			t.Fatalf("unexpected hash: %x (%q)", scope.Hash, scope.HashHex())
		}
	})

	t.Run("root and topic are distinct", func(t *testing.T) {
		root, err := NewScope(ProviderDingTalk, "account", "chat", "")
		if err != nil {
			t.Fatal(err)
		}
		topic, err := NewScope(ProviderDingTalk, "account", "chat", "topic")
		if err != nil {
			t.Fatal(err)
		}
		if root.Key == topic.Key || root.Hash == topic.Hash {
			t.Fatalf("root and topic collide: %#v vs %#v", root, topic)
		}
	})

	t.Run("same input is stable", func(t *testing.T) {
		first, err := NewScope(ProviderFeishu, "account", "chat", "thread")
		if err != nil {
			t.Fatal(err)
		}
		second, err := NewScope(ProviderFeishu, "account", "chat", "thread")
		if err != nil {
			t.Fatal(err)
		}
		if first != second || first.HashHex() != second.HashHex() {
			t.Fatalf("same input is not stable: %#v vs %#v", first, second)
		}
		if len(first.HashHex()) != 64 || strings.ToLower(first.HashHex()) != first.HashHex() {
			t.Fatalf("HashHex() = %q, want lowercase 64-char hex", first.HashHex())
		}
	})

	t.Run("enforces scope key byte limit", func(t *testing.T) {
		accountID := strings.Repeat("a", MaxScopeKeyBytes-len(string(ProviderDingTalk))-len("c")-3)
		exact, err := NewScope(ProviderDingTalk, accountID, "c", "")
		if err != nil {
			t.Fatalf("NewScope() exact limit error = %v", err)
		}
		if got := len([]byte(exact.Key)); got != MaxScopeKeyBytes {
			t.Fatalf("exact Key byte length = %d, want %d", got, MaxScopeKeyBytes)
		}

		_, err = NewScope(ProviderDingTalk, accountID+"a", "c", "")
		if err == nil {
			t.Fatal("NewScope() over limit error = nil, want validation error")
		}
		if !strings.Contains(err.Error(), fmt.Sprintf("scope key exceeds %d bytes", MaxScopeKeyBytes)) {
			t.Fatalf("NewScope() over limit error = %q, want scope key byte limit", err)
		}
	})

	invalid := []struct {
		name      string
		provider  Provider
		accountID string
		chatID    string
		threadID  string
		errorPart string
	}{
		{name: "invalid provider", provider: Provider("unknown"), accountID: "a", chatID: "b", errorPart: "provider"},
		{name: "provider whitespace", provider: Provider(" dingtalk "), accountID: "a", chatID: "b", errorPart: "provider"},
		{name: "missing account", provider: ProviderDingTalk, chatID: "b", errorPart: "account id"},
		{name: "missing chat", provider: ProviderDingTalk, accountID: "a", errorPart: "chat id"},
		{name: "separator in account", provider: ProviderDingTalk, accountID: "a\x1fb", chatID: "c", errorPart: "account id contains canonical separator"},
		{name: "separator in chat", provider: ProviderDingTalk, accountID: "a", chatID: "b\x1fc", errorPart: "chat id contains canonical separator"},
		{name: "separator in thread", provider: ProviderDingTalk, accountID: "a", chatID: "b", threadID: "t\x1fu", errorPart: "thread id contains canonical separator"},
		{name: "nul in account", provider: ProviderDingTalk, accountID: "a\x00b", chatID: "c", errorPart: "account id contains NUL"},
		{name: "nul in chat", provider: ProviderDingTalk, accountID: "a", chatID: "b\x00c", errorPart: "chat id contains NUL"},
		{name: "nul in thread", provider: ProviderDingTalk, accountID: "a", chatID: "b", threadID: "t\x00u", errorPart: "thread id contains NUL"},
	}
	for _, tt := range invalid {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewScope(tt.provider, tt.accountID, tt.chatID, tt.threadID); err == nil {
				t.Fatal("NewScope() error = nil, want validation error")
			} else if !strings.Contains(err.Error(), tt.errorPart) {
				t.Fatalf("NewScope() error = %q, want it to contain %q", err, tt.errorPart)
			}
		})
	}
}
