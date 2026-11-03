package sessioncontrol

import (
	"errors"
	"testing"
)

func TestParseRef(t *testing.T) {
	tests := []struct {
		raw  string
		want Source
		key  string
		ok   bool
	}{
		{raw: "tenant:webui-v2", want: SourceTenant, key: "webui-v2", ok: true},
		{raw: "tenant:channel:abc123", want: SourceTenant, key: "channel:abc123", ok: true},
		{raw: "local:019a-e74", want: SourceLocal, key: "019a-e74", ok: true},
		{raw: "webui-v2"}, {raw: "tenant:"}, {raw: "remote:abc"},
		{raw: "tenant:bad/key"}, {raw: "tenant:bad key"},
	}
	for _, tt := range tests {
		got, err := ParseRef(tt.raw)
		if tt.ok {
			if err != nil || got.Source != tt.want || got.Key != tt.key {
				t.Fatalf("ParseRef(%q) = %#v, %v", tt.raw, got, err)
			}
			continue
		}
		var refErr *RefError
		if err == nil || !errors.As(err, &refErr) || refErr.Code != CodeInvalidRef {
			t.Fatalf("ParseRef(%q) error = %v, want CodeInvalidRef", tt.raw, err)
		}
	}
}

func TestSessionRefString(t *testing.T) {
	for _, raw := range []string{"tenant:ABC", "LOCAL:019a"} {
		ref, err := ParseRef(raw)
		if err != nil || ref.String() != "tenant:ABC" && ref.String() != "local:019a" {
			t.Fatalf("round-trip %q: %#v, %v", raw, ref, err)
		}
	}
}
