package compact

import "testing"

func TestEstimateTextTokensPreservesASCIIAndCJKAccounting(t *testing.T) {
	tests := []struct {
		name string
		text string
		want int
	}{
		{name: "empty", text: "", want: 0},
		{name: "one ASCII token", text: "abcd", want: 1},
		{name: "rounds ASCII up", text: "abcde", want: 2},
		{name: "CJK runes cost one token each", text: "abcd中文", want: 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EstimateTextTokens(tt.text); got != tt.want {
				t.Fatalf("EstimateTextTokens(%q) = %d, want %d", tt.text, got, tt.want)
			}
		})
	}
}
