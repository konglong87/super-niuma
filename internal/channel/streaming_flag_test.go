package channel

import "testing"

func TestEvaluateStreamingCardDecisionTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		global     StreamingGlobalMode
		account    StreamingAccountMode
		supported  bool
		wantEnable bool
		wantReason StreamingDecisionReason
	}{
		{name: "global off is kill switch", global: StreamingGlobalOff, account: StreamingAccountEnabled, supported: true, wantReason: StreamingReasonGlobalOff},
		{name: "global off masks unsupported provider", global: StreamingGlobalOff, account: StreamingAccountEnabled, supported: false, wantReason: StreamingReasonGlobalOff},
		{name: "global off masks disabled account", global: StreamingGlobalOff, account: StreamingAccountDisabled, supported: true, wantReason: StreamingReasonGlobalOff},
		{name: "empty global is safe off", global: "", account: StreamingAccountEnabled, supported: true, wantReason: StreamingReasonGlobalOff},
		{name: "unknown global is safe off", global: "unexpected", account: StreamingAccountEnabled, supported: true, wantReason: StreamingReasonGlobalOff},
		{name: "allowlist enables selected account", global: StreamingGlobalAllowlist, account: StreamingAccountEnabled, supported: true, wantEnable: true, wantReason: StreamingReasonEnabled},
		{name: "allowlist rejects inherited account", global: StreamingGlobalAllowlist, account: StreamingAccountInherit, supported: true, wantReason: StreamingReasonNotAllowlisted},
		{name: "allowlist rejects disabled account", global: StreamingGlobalAllowlist, account: StreamingAccountDisabled, supported: true, wantReason: StreamingReasonAccountDisabled},
		{name: "allowlist unsupported provider precedes disabled account", global: StreamingGlobalAllowlist, account: StreamingAccountDisabled, supported: false, wantReason: StreamingReasonUnsupported},
		{name: "allowlist unsupported provider precedes inherited account", global: StreamingGlobalAllowlist, account: StreamingAccountInherit, supported: false, wantReason: StreamingReasonUnsupported},
		{name: "allowlist unknown account fails closed", global: StreamingGlobalAllowlist, account: "unexpected", supported: true, wantReason: StreamingReasonAccountDisabled},
		{name: "global on enables inherited account", global: StreamingGlobalOn, account: StreamingAccountInherit, supported: true, wantEnable: true, wantReason: StreamingReasonEnabled},
		{name: "global on enables explicitly enabled account", global: StreamingGlobalOn, account: StreamingAccountEnabled, supported: true, wantEnable: true, wantReason: StreamingReasonEnabled},
		{name: "account disable overrides global on", global: StreamingGlobalOn, account: StreamingAccountDisabled, supported: true, wantReason: StreamingReasonAccountDisabled},
		{name: "global on unsupported provider precedes disabled account", global: StreamingGlobalOn, account: StreamingAccountDisabled, supported: false, wantReason: StreamingReasonUnsupported},
		{name: "global on unsupported provider precedes enabled account", global: StreamingGlobalOn, account: StreamingAccountEnabled, supported: false, wantReason: StreamingReasonUnsupported},
		{name: "provider capability is mandatory", global: StreamingGlobalOn, account: StreamingAccountEnabled, supported: false, wantReason: StreamingReasonUnsupported},
		{name: "provider capability is mandatory for inherited account", global: StreamingGlobalOn, account: StreamingAccountInherit, supported: false, wantReason: StreamingReasonUnsupported},
		{name: "global on empty account inherits", global: StreamingGlobalOn, account: "", supported: true, wantEnable: true, wantReason: StreamingReasonEnabled},
		{name: "global on unknown account fails closed", global: StreamingGlobalOn, account: "unexpected", supported: true, wantReason: StreamingReasonAccountDisabled},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := EvaluateStreamingCard(tt.global, tt.account, tt.supported)
			if got.Enabled != tt.wantEnable || got.Reason != tt.wantReason {
				t.Fatalf("EvaluateStreamingCard() = %+v, want enabled=%v reason=%s", got, tt.wantEnable, tt.wantReason)
			}
		})
	}
}

func TestParseStreamingModesUseSafeDefaultsAndRejectInvalidValues(t *testing.T) {
	t.Parallel()

	global, err := ParseStreamingGlobalMode("")
	if err != nil || global != StreamingGlobalOff {
		t.Fatalf("ParseStreamingGlobalMode(empty) = %q, %v", global, err)
	}
	account, err := ParseStreamingAccountMode("")
	if err != nil || account != StreamingAccountInherit {
		t.Fatalf("ParseStreamingAccountMode(empty) = %q, %v", account, err)
	}

	for _, value := range []string{"invalid", " OFF "} {
		if value == " OFF " {
			if got, err := ParseStreamingGlobalMode(value); err != nil || got != StreamingGlobalOff {
				t.Fatalf("ParseStreamingGlobalMode(%q) = %q, %v", value, got, err)
			}
			continue
		}
		if _, err := ParseStreamingGlobalMode(value); err == nil {
			t.Fatalf("ParseStreamingGlobalMode(%q) accepted invalid value", value)
		}
	}
	if got, err := ParseStreamingGlobalMode("AllowList"); err != nil || got != StreamingGlobalAllowlist {
		t.Fatalf("ParseStreamingGlobalMode(normalized) = %q, %v", got, err)
	}
	if _, err := ParseStreamingAccountMode("invalid"); err == nil {
		t.Fatal("ParseStreamingAccountMode(invalid) accepted invalid value")
	}
	if got, err := ParseStreamingAccountMode("invalid"); err == nil || got != StreamingAccountDisabled {
		t.Fatalf("ParseStreamingAccountMode(invalid) = %q, %v; want disabled with error", got, err)
	}
	parsed, err := ParseStreamingAccountMode("invalid")
	if err == nil {
		t.Fatal("ParseStreamingAccountMode(invalid) unexpectedly succeeded")
	}
	if got := EvaluateStreamingCard(StreamingGlobalOn, parsed, true); got.Enabled || got.Reason != StreamingReasonAccountDisabled {
		t.Fatalf("EvaluateStreamingCard(on, invalid parsed account, supported) = %+v, want account_disabled", got)
	}
	if got, err := ParseStreamingAccountMode(" ENABLED "); err != nil || got != StreamingAccountEnabled {
		t.Fatalf("ParseStreamingAccountMode(normalized) = %q, %v", got, err)
	}
}
