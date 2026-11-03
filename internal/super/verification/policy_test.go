package verification

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func textPolicy(path, expected string) Policy {
	return Policy{Version: "v1", Rules: []Rule{{Path: path, Kind: KindTextContains, Expected: expected}}}
}

func TestValidatePolicyRejectsInvalidAndVacuousPolicies(t *testing.T) {
	cases := []struct {
		name   string
		policy Policy
	}{
		{"empty", Policy{}},
		{"no rules", Policy{Version: "v1"}},
		{"version whitespace", Policy{Version: " v1", Rules: textPolicy("a", "ok").Rules}},
		{"version control", Policy{Version: "v1\n", Rules: textPolicy("a", "ok").Rules}},
		{"version oversized", Policy{Version: strings.Repeat("v", MaxVersionBytes+1), Rules: textPolicy("a", "ok").Rules}},
		{"only nonempty", Policy{Version: "v1", Rules: []Rule{{Path: "a", Kind: KindNonempty}}}},
		{"blank expected", textPolicy("a", " \n")},
		{"bad utf8 expected", textPolicy("a", string([]byte{0xff}))},
		{"long expected", textPolicy("a", strings.Repeat("x", MaxExpectedBytes+1))},
		{"unknown kind", Policy{Version: "v1", Rules: []Rule{{Path: "a", Kind: "shell", Expected: "true"}}}},
		{"negative budget", Policy{Version: "v1", Rules: []Rule{{Path: "a", Kind: KindTextContains, Expected: "a", MaxBytes: -1}}}},
		{"huge budget", Policy{Version: "v1", Rules: []Rule{{Path: "a", Kind: KindTextContains, Expected: "a", MaxBytes: MaxFileBytes + 1}}}},
		{"impossible text", Policy{Version: "v1", Rules: []Rule{{Path: "a", Kind: KindTextContains, Expected: "abcd", MaxBytes: 3}}}},
		{"bad hash", Policy{Version: "v1", Rules: []Rule{{Path: "a", Kind: KindSHA256, Expected: strings.Repeat("z", 64)}}}},
		{"short hash", Policy{Version: "v1", Rules: []Rule{{Path: "a", Kind: KindSHA256, Expected: "ff"}}}},
		{"hash prefix", Policy{Version: "v1", Rules: []Rule{{Path: "a", Kind: KindSHA256, Expected: "0x" + strings.Repeat("a", 64)}}}},
		{"no json keys", Policy{Version: "v1", Rules: []Rule{{Path: "a", Kind: KindJSONKeys}}}},
		{"duplicate json keys", Policy{Version: "v1", Rules: []Rule{{Path: "a", Kind: KindJSONKeys, Keys: []string{"a", "a"}}}}},
		{"empty json key", Policy{Version: "v1", Rules: []Rule{{Path: "a", Kind: KindJSONKeys, Keys: []string{" "}}}}},
		{"extraneous keys", Policy{Version: "v1", Rules: []Rule{{Path: "a", Kind: KindTextContains, Expected: "ok", Keys: []string{"a"}}}}},
		{"extraneous expected", Policy{Version: "v1", Rules: []Rule{{Path: "a", Kind: KindJSONKeys, Expected: "ok", Keys: []string{"a"}}}}},
		{"duplicate path", Policy{Version: "v1", Rules: []Rule{{Path: "a", Kind: KindTextContains, Expected: "a"}, {Path: "a", Kind: KindSHA256, Expected: strings.Repeat("0", 64)}}}},
		{"case alias", Policy{Version: "v1", Rules: []Rule{{Path: "A", Kind: KindTextContains, Expected: "a"}, {Path: "a", Kind: KindNonempty}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidatePolicy(tc.policy); !errors.Is(err, ErrInvalidPolicy) {
				t.Fatalf("error=%v", err)
			}
			if got := PolicyHash(tc.policy); got != "" {
				t.Fatalf("invalid policy hash=%q", got)
			}
		})
	}
}

func TestPathsMustBeCanonicalRelativeAndNonReserved(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../a", "a/../b", "/etc/passwd", "./a", "a//b", "a/", `a\b`, `C:/a`, "a\x00b", " a", "a/ b", "a.", ".git/config", "nested/.GIT/config", ".super-niuma/a", "work-orders.db", "nested/work-orders.db-wal", "work-orders.db-shm", "work-orders.db.journal", strings.Repeat("a", MaxPathBytes+1), strings.Repeat("a/", MaxPathDepth) + "x"} {
		t.Run(fmt.Sprintf("%q", name), func(t *testing.T) {
			if err := ValidatePolicy(textPolicy(name, "ok")); !errors.Is(err, ErrInvalidPolicy) {
				t.Fatalf("accepted %q: %v", name, err)
			}
		})
	}
	if err := ValidatePolicy(textPolicy("artifacts/result.txt", "ok")); err != nil {
		t.Fatal(err)
	}
}

func TestPolicyLimitsAndCanonicalHash(t *testing.T) {
	p := textPolicy("b.txt", "ok")
	p.Rules = append(p.Rules, Rule{Path: "a.json", Kind: KindJSONKeys, Keys: []string{"z", "a"}})
	original := append([]string(nil), p.Rules[1].Keys...)
	q := Policy{Version: "v1", Rules: []Rule{{Path: "a.json", Kind: KindJSONKeys, Keys: []string{"a", "z"}, MaxBytes: DefaultMaxBytes}, {Path: "b.txt", Kind: KindTextContains, Expected: "ok", MaxBytes: DefaultMaxBytes}}}
	if hash := PolicyHash(p); len(hash) != 64 || hash != PolicyHash(q) {
		t.Fatalf("hashes %q %q", hash, PolicyHash(q))
	}
	if !reflect.DeepEqual(p.Rules[1].Keys, original) || p.Rules[0].Path != "b.txt" || p.Rules[0].MaxBytes != 0 {
		t.Fatal("caller policy mutated")
	}
	q.Version = "v2"
	if PolicyHash(p) == PolicyHash(q) {
		t.Fatal("version not committed")
	}
	q.Version = "v1"
	q.Rules[1].Expected = "different"
	if PolicyHash(p) == PolicyHash(q) {
		t.Fatal("expected content not committed")
	}
	sha := Policy{Version: "v1", Rules: []Rule{{Path: "a", Kind: KindSHA256, Expected: strings.Repeat("AB", 32)}}}
	lower := Policy{Version: "v1", Rules: []Rule{{Path: "a", Kind: KindSHA256, Expected: strings.Repeat("ab", 32)}}}
	if PolicyHash(sha) != PolicyHash(lower) {
		t.Fatal("sha case normalization")
	}
	for _, count := range []int{MaxRules + 1, 5} {
		p := Policy{Version: "v1"}
		for i := 0; i < count; i++ {
			p.Rules = append(p.Rules, Rule{Path: fmt.Sprintf("%d.txt", i), Kind: KindTextContains, Expected: "ok", MaxBytes: MaxFileBytes})
		}
		if ValidatePolicy(p) == nil {
			t.Fatalf("accepted excessive policy count=%d", count)
		}
	}
	keys := make([]string, MaxKeys+1)
	for i := range keys {
		keys[i] = fmt.Sprint(i)
	}
	if ValidatePolicy(Policy{Version: "v1", Rules: []Rule{{Path: "a", Kind: KindJSONKeys, Keys: keys}}}) == nil {
		t.Fatal("too many keys accepted")
	}
}
