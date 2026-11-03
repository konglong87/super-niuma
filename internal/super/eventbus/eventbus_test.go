package eventbus

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestCanonicalEventAndBoundedValidation(t *testing.T) {
	good := Event{ID: "e", Scope: LocalScope, Topic: "test.ready", AggregateID: "root", Version: EventVersion, OccurredAtMS: 1, Payload: json.RawMessage(`{ "b": 2, "a": 1 }`)}
	got, err := NormalizeEvent(good)
	if err != nil || string(got.Payload) != `{"a":1,"b":2}` {
		t.Fatalf("canonical: %s %v", got.Payload, err)
	}
	cases := []Event{good, good, good, good, good, good}
	cases[0].Scope = ""
	cases[1].Version = 0
	cases[2].OccurredAtMS = 0
	cases[3].Sequence = 1
	cases[4].ID = strings.Repeat("a", MaxKeyBytes+1)
	cases[5].Payload = json.RawMessage("{\"x\":\"\xff\"}")
	for _, e := range cases {
		if _, err := NormalizeEvent(e); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid accepted: %#v %v", e, err)
		}
	}
}
func TestSubscriptionNormalizationDoesNotMutateCaller(t *testing.T) {
	input := Subscription{Scope: LocalScope, Name: "consumer", Start: StartBeginning, Topics: []string{"z", "a"}, MaxAttempts: 2}
	got, err := NormalizeSubscription(input)
	if err != nil || got.Topics[0] != "a" || input.Topics[0] != "z" {
		t.Fatalf("normalize: %v %v", got, err)
	}
	input.Start = "latest"
	if _, err = NormalizeSubscription(input); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("unknown start accepted")
	}
}
