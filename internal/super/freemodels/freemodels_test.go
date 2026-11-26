package freemodels

import (
	"strings"
	"testing"
)

func TestEmbeddedSnapshotIsValid(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatalf("embedded snapshot: %v", err)
	}
	if len(c.Providers) != 7 {
		t.Fatalf("want 7 providers, got %d", len(c.Providers))
	}
	hosts, err := SignupHosts()
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"bigmodel.cn", "openrouter.ai", "aistudio.google.com"} {
		if _, ok := hosts[h]; !ok {
			t.Errorf("signup hosts missing %s: %v", h, hosts)
		}
	}
}

const validProvider = `{"slug":"a","name":"A","base_url":"https://a.example/v1","openai_compatible":"supported",
"free":{"type":"daily"},"requirements":{"credit_card":"none","real_name":"none","region":"none"},
"signup_url":"https://a.example/","status":"ok","verified_at":"2026-01-01","sources":[],
"recommended_model":{"id":"m","capabilities":[],"pending_capabilities":[],"tool_calling":"supported"},
"models":[],"usage_notes":[]}`

func catalog(version string, providers ...string) []byte {
	return []byte(`{"schema_version":` + version + `,"generated_at":"x","providers":[` + strings.Join(providers, ",") + `]}`)
}

func TestParseValidatesStrictly(t *testing.T) {
	if _, err := Parse(catalog("1", validProvider)); err != nil {
		t.Fatalf("valid catalog rejected: %v", err)
	}
	replace := func(old, new string) string { return strings.Replace(validProvider, old, new, 1) }
	cases := map[string][]byte{
		"schema version":      catalog("2", validProvider),
		"no providers":        catalog("1"),
		"missing slug":        catalog("1", replace(`"slug":"a"`, `"slug":""`)),
		"missing name":        catalog("1", replace(`"name":"A"`, `"name":""`)),
		"http base_url":       catalog("1", replace(`https://a.example/v1`, `http://a.example/v1`)),
		"http signup_url":     catalog("1", replace(`https://a.example/"`, `http://a.example/"`)),
		"empty signup_url":    catalog("1", replace(`"signup_url":"https://a.example/"`, `"signup_url":""`)),
		"bad compat enum":     catalog("1", replace(`"openai_compatible":"supported"`, `"openai_compatible":"maybe"`)),
		"bad free type":       catalog("1", replace(`"daily"`, `"forever"`)),
		"bad requirement":     catalog("1", replace(`"credit_card":"none"`, `"credit_card":"yes"`)),
		"bad status":          catalog("1", replace(`"status":"ok"`, `"status":"done"`)),
		"bad tool_calling":    catalog("1", replace(`"tool_calling":"supported"`, `"tool_calling":"yes"`)),
		"duplicate slug":      catalog("1", validProvider, validProvider),
		"http source":         catalog("1", replace(`"sources":[]`, `"sources":[{"label":"l","url":"http://a.example"}]`)),
		"not json":            []byte("{"),
		"signup url userinfo": catalog("1", replace(`https://a.example/"`, `https://u:p@a.example/"`)),
	}
	for name, data := range cases {
		if _, err := Parse(data); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestUnverified(t *testing.T) {
	ok := Model{ToolCalling: "supported"}
	cases := []struct {
		name string
		p    Provider
		want bool
	}{
		{"all confirmed", Provider{Status: "ok", OpenAICompatible: "supported", RecommendedModel: &ok}, false},
		{"pending status", Provider{Status: "pending", OpenAICompatible: "supported", RecommendedModel: &ok}, true},
		{"compat unverified", Provider{Status: "ok", OpenAICompatible: "unverified", RecommendedModel: &ok}, true},
		{"no recommended model", Provider{Status: "ok", OpenAICompatible: "supported"}, true},
		{"tool calling pending", Provider{Status: "ok", OpenAICompatible: "supported", RecommendedModel: &Model{ToolCalling: "pending"}}, true},
	}
	for _, tc := range cases {
		if got := tc.p.Unverified(); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}
