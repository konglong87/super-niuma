package agentbudget

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestBudgetTripsOnTokenCeiling(t *testing.T) {
	budget := New(Limits{MaxTotalTokens: 100})
	if err := budget.Check(); err != nil {
		t.Fatalf("fresh budget already exhausted: %v", err)
	}
	budget.Add(60, 30, 0)
	if err := budget.Check(); err != nil {
		t.Fatalf("budget tripped at 90/100 tokens: %v", err)
	}
	budget.Add(0, 10, 0)
	err := budget.Check()
	if !errors.Is(err, ErrExhausted) {
		t.Fatalf("Check() at 100/100 = %v, want ErrExhausted", err)
	}
	for _, want := range []string{"100 tokens", "100-token session budget", EnvMaxTotalTokens} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err, want)
		}
	}
}

func TestBudgetTripsOnCostCeiling(t *testing.T) {
	budget := New(Limits{MaxCostUSD: 1.5})
	budget.Add(10, 10, 1.0)
	if err := budget.Check(); err != nil {
		t.Fatalf("budget tripped at $1.00/$1.50: %v", err)
	}
	budget.Add(10, 10, 0.6)
	err := budget.Check()
	if !errors.Is(err, ErrExhausted) {
		t.Fatalf("Check() at $1.60/$1.50 = %v, want ErrExhausted", err)
	}
	if !strings.Contains(err.Error(), EnvMaxCostUSD) {
		t.Fatalf("cost error %q does not name %s", err, EnvMaxCostUSD)
	}
}

func TestBudgetZeroLimitsNeverTrip(t *testing.T) {
	budget := New(Limits{})
	budget.Add(1<<40, 1<<40, 1e9)
	if err := budget.Check(); err != nil {
		t.Fatalf("a budget with no limits tripped: %v", err)
	}
}

func TestNilBudgetIsUnlimitedAndSafe(t *testing.T) {
	var budget *Budget
	budget.Add(100, 100, 100)
	if err := budget.Check(); err != nil {
		t.Fatalf("nil budget Check() = %v, want nil", err)
	}
	if snapshot := budget.Snapshot(); snapshot.TotalTokens != 0 {
		t.Fatalf("nil budget Snapshot() = %+v, want zero", snapshot)
	}
}

func TestDefaultLimitsHonourEnvOverrides(t *testing.T) {
	t.Setenv(EnvMaxTotalTokens, "1234")
	t.Setenv(EnvMaxCostUSD, "2.5")
	if got := DefaultLimits(); got.MaxTotalTokens != 1234 || got.MaxCostUSD != 2.5 {
		t.Fatalf("DefaultLimits() = %+v", got)
	}
	// 0 is a meaningful override (disable the ceiling), unlike garbage.
	t.Setenv(EnvMaxTotalTokens, "0")
	if got := DefaultLimits(); got.MaxTotalTokens != 0 {
		t.Fatalf("DefaultLimits() with an explicit 0 = %+v, want the limit disabled", got)
	}
	for _, raw := range []string{"-5", "nope"} {
		t.Setenv(EnvMaxTotalTokens, raw)
		if got := DefaultLimits(); got.MaxTotalTokens != DefaultMaxTotalTokens {
			t.Fatalf("DefaultLimits() with %q = %+v, want the %d default", raw, got, DefaultMaxTotalTokens)
		}
	}
}

func TestBudgetAddIsConcurrencySafe(t *testing.T) {
	budget := New(Limits{MaxTotalTokens: 0})
	var wg sync.WaitGroup
	for worker := 0; worker < 32; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				budget.Add(1, 1, 0.01)
				_ = budget.Check()
				_ = budget.Snapshot()
			}
		}()
	}
	wg.Wait()
	if snapshot := budget.Snapshot(); snapshot.TotalTokens != 6400 {
		t.Fatalf("Snapshot() = %+v, want 6400 tokens", snapshot)
	}
}

// AUDIT-P0-15 made the cost figure ~10x smaller for cache-heavy runs, which
// would make a cost ceiling trip later. That is only safe because no cost
// ceiling is armed by default — pin that, and pin that the token ceiling (the
// one that does guard a default install) was not weakened.
func TestDefaultLimitsArmOnlyTheTokenCeiling(t *testing.T) {
	t.Setenv(EnvMaxTotalTokens, "")
	t.Setenv(EnvMaxCostUSD, "")
	limits := DefaultLimits()
	if limits.MaxCostUSD != 0 {
		t.Fatalf("MaxCostUSD = %v, want 0; a non-zero default would now trip ~10x later than before AUDIT-P0-15", limits.MaxCostUSD)
	}
	if limits.MaxTotalTokens != 20_000_000 {
		t.Fatalf("MaxTotalTokens = %d, want 20000000 (unchanged by AUDIT-P0-15)", limits.MaxTotalTokens)
	}
}
