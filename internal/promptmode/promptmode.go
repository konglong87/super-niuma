package promptmode

import "strings"

type Mode string

const (
	Code Mode = "code"
	Chat Mode = "chat"
)

func Parse(value string, fallback Mode) Mode {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(Code), "code-development", "development", "dev":
		return Code
	case string(Chat), "tenant-chat", "normal", "default":
		return Chat
	default:
		if fallback != "" {
			return fallback
		}
		return Code
	}
}

func Valid(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case string(Code), "code-development", "development", "dev", string(Chat), "tenant-chat", "normal", "default":
		return true
	default:
		return false
	}
}

func (m Mode) String() string {
	if m == Chat {
		return string(Chat)
	}
	return string(Code)
}

func (m Mode) IsChat() bool {
	return m == Chat
}

func (m Mode) IsCode() bool {
	return !m.IsChat()
}
