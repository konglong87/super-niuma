package imagegen

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSanitizeErrorMessageRedactsJSONAndDelimitedSecrets(t *testing.T) {
	input := `{"api_key":"sk-secret","nested":{"token" : "token-secret", "authorization":"Bearer auth-secret", "password":"password-secret", "secret":"secret-value"},"prompt":"full prompt","response body":"private body","url":"https://private.example.test/image","data":"data:image/png;base64,QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVo="}`
	got := SanitizeErrorMessage(input)
	for _, secret := range []string{"sk-secret", "token-secret", "auth-secret", "password-secret", "secret-value", "full prompt", "private body", "private.example.test", "QUJDREV"} {
		if strings.Contains(got, secret) {
			t.Fatalf("secret %q remains in %q", secret, got)
		}
	}
}

func TestSanitizeErrorMessagePreservesUTF8AtByteLimit(t *testing.T) {
	got := SanitizeErrorMessage(strings.Repeat("图", 600))
	if len(got) > MaxSafeErrorMessageBytes || !utf8.ValidString(got) {
		t.Fatalf("invalid bounded message: bytes=%d valid=%t", len(got), utf8.ValidString(got))
	}
}
