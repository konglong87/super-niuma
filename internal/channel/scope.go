package channel

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

const (
	scopeCanonicalSeparator = "\x1f"

	// MaxScopeKeyBytes matches the scope_key storage column limit.
	MaxScopeKeyBytes = 768
)

// Scope identifies a conversation within a provider account.
// ThreadID is empty for the conversation root.
type Scope struct {
	Provider  Provider
	AccountID string
	ChatID    string
	ThreadID  string
	Key       string
	Hash      [sha256.Size]byte
}

// NewScope normalizes and validates conversation identity fields, then derives
// a canonical key and stable hash from their ordered representation.
func NewScope(provider Provider, accountID, chatID, threadID string) (Scope, error) {
	accountID = strings.TrimSpace(accountID)
	chatID = strings.TrimSpace(chatID)
	threadID = strings.TrimSpace(threadID)

	if !provider.Valid() {
		return Scope{}, fmt.Errorf("invalid provider %q", provider)
	}
	if err := validateScopePart("provider", string(provider)); err != nil {
		return Scope{}, err
	}
	if accountID == "" {
		return Scope{}, fmt.Errorf("account id is required")
	}
	if chatID == "" {
		return Scope{}, fmt.Errorf("chat id is required")
	}
	if err := validateScopePart("account id", accountID); err != nil {
		return Scope{}, err
	}
	if err := validateScopePart("chat id", chatID); err != nil {
		return Scope{}, err
	}
	if err := validateScopePart("thread id", threadID); err != nil {
		return Scope{}, err
	}

	key := strings.Join([]string{string(provider), accountID, chatID, threadID}, scopeCanonicalSeparator)
	if len([]byte(key)) > MaxScopeKeyBytes {
		return Scope{}, fmt.Errorf("scope key exceeds %d bytes", MaxScopeKeyBytes)
	}
	return Scope{
		Provider:  provider,
		AccountID: accountID,
		ChatID:    chatID,
		ThreadID:  threadID,
		Key:       key,
		Hash:      sha256.Sum256([]byte(key)),
	}, nil
}

func validateScopePart(name, value string) error {
	if strings.Contains(value, scopeCanonicalSeparator) {
		return fmt.Errorf("%s contains canonical separator", name)
	}
	if strings.ContainsRune(value, '\x00') {
		return fmt.Errorf("%s contains NUL", name)
	}
	return nil
}

// HashHex returns the canonical lowercase hexadecimal hash.
func (s Scope) HashHex() string {
	return hex.EncodeToString(s.Hash[:])
}
