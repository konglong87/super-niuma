package quota

import (
	"errors"
	"testing"
	"time"
)

func TestNormalizeConfigAppliesDefaults(t *testing.T) {
	cfg, err := NormalizeConfig(ConfigInput{TenantID: 5})
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if cfg.Timezone != "UTC" {
		t.Fatalf("timezone = %q, want UTC", cfg.Timezone)
	}
	if cfg.ReserveOutputTokens != 4096 {
		t.Fatalf("reserveOutputTokens = %d, want 4096", cfg.ReserveOutputTokens)
	}
	if cfg.Status != "active" {
		t.Fatalf("status = %q, want active", cfg.Status)
	}
}

func TestNormalizeConfigRejectsInvalidInput(t *testing.T) {
	cases := map[string]ConfigInput{
		"missing tenant id": {TenantID: 0},
		"zero limit":        {TenantID: 1, QPSLimit: uint64Ptr(0)},
		"bad timezone":      {TenantID: 1, Timezone: "Not/AZone"},
		"bad status":        {TenantID: 1, Status: "weird"},
	}
	for name, in := range cases {
		if _, err := NormalizeConfig(in); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("%s: err = %v, want ErrInvalidConfig", name, err)
		}
	}
}

func TestNormalizeConfigClonesLimitPointers(t *testing.T) {
	limit := uint64(42)
	cfg, err := NormalizeConfig(ConfigInput{TenantID: 1, QPSLimit: &limit})
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	limit = 99 // 改原始变量不应影响已归一化的配置
	if cfg.QPSLimit == nil || *cfg.QPSLimit != 42 {
		t.Fatalf("qps limit = %v, want cloned 42", cfg.QPSLimit)
	}
}

func TestUsageDateTruncatesToLocalMidnight(t *testing.T) {
	// UTC 2026-06-26 20:00 在 UTC+8 是本地 06-27 04:00,当日应归到 06-27 本地午夜
	at := time.Date(2026, 6, 26, 20, 0, 0, 0, time.UTC)
	date, err := UsageDate(at, "Asia/Shanghai")
	if err != nil {
		t.Fatalf("usage date: %v", err)
	}
	if date.Year() != 2026 || date.Month() != time.June || date.Day() != 27 {
		t.Fatalf("date = %v, want 2026-06-27", date)
	}
	if date.Hour() != 0 || date.Minute() != 0 || date.Second() != 0 {
		t.Fatalf("date not truncated to midnight: %v", date)
	}
}

func TestUsageDateRejectsBadTimezone(t *testing.T) {
	if _, err := UsageDate(time.Now(), "Not/AZone"); err == nil {
		t.Fatal("expected error for invalid timezone")
	}
}

func TestDefaultConfigIsDisabled(t *testing.T) {
	cfg := DefaultConfig(9)
	if cfg.TenantID != 9 || cfg.QuotaEnabled || cfg.Status != "active" || cfg.ReserveOutputTokens != 4096 {
		t.Fatalf("default config = %+v", cfg)
	}
}

func TestRejectionEventAndLimitType(t *testing.T) {
	cases := []struct {
		err   error
		event string
		limit string
	}{
		{ErrRateLimited, "quota.rejected.qps", "qps"},
		{ErrDailyTokenLimitExceeded, "quota.rejected.daily_tokens", "daily_tokens"},
		{ErrDailyMessageLimitExceeded, "quota.rejected.daily_messages", "daily_messages"},
		{ErrConcurrentLimitExceeded, "quota.rejected.concurrent", "concurrent"},
		{errors.New("other"), "quota.rejected", "unknown"},
	}
	for _, c := range cases {
		if got := RejectionEventType(c.err); got != c.event {
			t.Fatalf("event(%v) = %q, want %q", c.err, got, c.event)
		}
		if got := RejectionLimitType(c.err); got != c.limit {
			t.Fatalf("limit(%v) = %q, want %q", c.err, got, c.limit)
		}
	}
}
