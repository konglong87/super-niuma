package workflow

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func testAgent(ref string) Agent {
	return Agent{Ref: ref, ProfileKey: ref, ProfileVersion: 1, Kind: "text", SystemPrompt: "You are a careful assistant.", MaxOutputTokens: 100}
}
func testDefinition() Definition {
	return Definition{SchemaVersion: 2, Nodes: []Node{{ID: "plan", AgentRef: "a", Retry: RetryPolicy{MaxAttempts: 2, BackoffMS: 1}}, {ID: "work", AgentRef: "a", DependsOn: []string{"plan"}, Retry: RetryPolicy{MaxAttempts: 2, BackoffMS: 1}}, {ID: "review", AgentRef: "a", DependsOn: []string{"work"}, Retry: RetryPolicy{MaxAttempts: 2, BackoffMS: 1}}}}
}
func TestCompileStableAndIsolated(t *testing.T) {
	d := testDefinition()
	p, err := Compile(d, []Agent{testAgent("a")})
	if err != nil {
		t.Fatal(err)
	}
	p2, err := Compile(d, []Agent{testAgent("a")})
	if err != nil || p.Hash != p2.Hash {
		t.Fatal("hash unstable", err)
	}
	p.Definition.Nodes[1].DependsOn[0] = "changed"
	if !reflect.DeepEqual(d.Nodes[1].DependsOn, []string{"plan"}) {
		t.Fatal("input mutated")
	}
}
func TestCompileRejectsInvalidGraphsAndAuthority(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Definition, *[]Agent)
	}{
		{"unknown schema", func(d *Definition, a *[]Agent) { d.SchemaVersion = 3 }},
		{"cycle", func(d *Definition, a *[]Agent) { d.Nodes[0].DependsOn = []string{"review"} }},
		{"missing dependency", func(d *Definition, a *[]Agent) { d.Nodes[1].DependsOn = []string{"missing"} }},
		{"duplicate", func(d *Definition, a *[]Agent) { d.Nodes[1].ID = "plan" }},
		{"missing agent", func(d *Definition, a *[]Agent) { d.Nodes[1].AgentRef = "missing" }},
		{"tools", func(d *Definition, a *[]Agent) { (*a)[0].Tools = []string{"*"} }},
		{"computer", func(d *Definition, a *[]Agent) { (*a)[0].Kind = "computer" }},
		{"too parallel", func(d *Definition, a *[]Agent) { d.Limits.MaxParallel = 5 }},
		{"too many attempts", func(d *Definition, a *[]Agent) { d.Nodes[0].Retry.MaxAttempts = 4 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := testDefinition()
			a := []Agent{testAgent("a")}
			tc.change(&d, &a)
			if _, err := Compile(d, a); err == nil {
				t.Fatal("accepted invalid plan")
			}
		})
	}
}

func TestCompileEmptyDependenciesHaveArrayJSON(t *testing.T) {
	p, err := Compile(testDefinition(), []Agent{testAgent("a")})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(p)
	if strings.Contains(string(raw), `"depends_on":null`) {
		t.Fatal("wire contract needs [] for root steps", string(raw))
	}
}
