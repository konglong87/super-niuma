package config

import (
	"encoding/json"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestComputerUseImageInputExactAssertion(t *testing.T) {
	settings := &ComputerUseSettings{ImageInputRoutes: []ComputerUseImageInputRoute{{Provider: "vision", Model: "model-v1"}, {Provider: "", Model: ""}}}
	for _, test := range []struct {
		provider, model string
		want            bool
	}{
		{"vision", "model-v1", true},
		{"Vision", "model-v1", false},
		{"vision", "Model-v1", false},
		{"vision ", "model-v1", false},
		{"vision", "model-v1 ", false},
		{"vision", "model-v2", false},
		{"openai", "gpt-4o", false},
		{"", "", false},
	} {
		if got := settings.SupportsImageInput(test.provider, test.model); got != test.want {
			t.Errorf("SupportsImageInput(%q, %q) = %v, want %v", test.provider, test.model, got, test.want)
		}
	}
	if (*ComputerUseSettings)(nil).SupportsImageInput("vision", "model-v1") {
		t.Fatal("nil settings must deny")
	}
}

func TestComputerUseSettingsMergeAndRoundTrip(t *testing.T) {
	for _, codec := range []struct {
		name      string
		unmarshal func([]byte, any) error
		marshal   func(any) ([]byte, error)
	}{
		{"json", json.Unmarshal, json.Marshal},
		// JSON is also valid YAML, exercising the YAML field tags and nil/[] distinction.
		{"yaml", yaml.Unmarshal, yaml.Marshal},
	} {
		t.Run(codec.name, func(t *testing.T) {
			for _, test := range []struct {
				name, override string
				want           string
			}{
				{"omitted", `{}`, "base"},
				{"null object", `{"computerUse":null}`, "base"},
				{"empty object", `{"computerUse":{}}`, "base"},
				{"null routes", `{"computerUse":{"imageInputRoutes":null}}`, "base"},
				{"replace", `{"computerUse":{"imageInputRoutes":[{"provider":"next","model":"m"}]}}`, "next"},
				{"revoke", `{"computerUse":{"imageInputRoutes":[]}}`, ""},
			} {
				t.Run(test.name, func(t *testing.T) {
					base := Settings{ComputerUse: &ComputerUseSettings{ImageInputRoutes: []ComputerUseImageInputRoute{{Provider: "base", Model: "m"}}}}
					var override Settings
					if err := codec.unmarshal([]byte(test.override), &override); err != nil {
						t.Fatal(err)
					}
					merged := MergeSettings(base, override)
					data, err := codec.marshal(merged)
					if err != nil {
						t.Fatal(err)
					}
					var roundtrip Settings
					if err := codec.unmarshal(data, &roundtrip); err != nil {
						t.Fatal(err)
					}
					// Re-merge after serialization: a saved [] must still revoke base.
					got := MergeSettings(base, roundtrip).ComputerUse
					if test.want == "" {
						if got.ImageInputRoutes == nil || len(got.ImageInputRoutes) != 0 {
							t.Fatalf("explicit empty override lost: %+v", got)
						}
					} else if len(got.ImageInputRoutes) != 1 || !got.SupportsImageInput(test.want, "m") {
						t.Fatalf("unexpected merged routes: %+v", got.ImageInputRoutes)
					}
					if len(merged.ComputerUse.ImageInputRoutes) > 0 {
						merged.ComputerUse.ImageInputRoutes[0].Provider = "changed"
					}
					if base.ComputerUse.ImageInputRoutes[0].Provider != "base" {
						t.Fatal("merge aliases base")
					}
					if override.ComputerUse != nil && len(override.ComputerUse.ImageInputRoutes) > 0 && override.ComputerUse.ImageInputRoutes[0].Provider != "next" {
						t.Fatal("merge aliases override")
					}
				})
			}
		})
	}
	if got := MergeSettings(Settings{}, Settings{}).ComputerUse; got != nil {
		t.Fatalf("absent settings changed: %+v", got)
	}
}
