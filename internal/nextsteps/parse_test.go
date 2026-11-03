package nextsteps

import (
	"strings"
	"testing"
)

func TestParseKeepsOneSuggestionPerLine(t *testing.T) {
	got := Parse("给这个改动补单元测试\n跑一遍完整测试\n提交并推送", 3)
	want := []string{"给这个改动补单元测试", "跑一遍完整测试", "提交并推送"}
	if len(got) != len(want) {
		t.Fatalf("got %d suggestions %q, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("suggestion %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// 小模型经常自己加 bullet 或编号，这些不是内容的一部分。
func TestParseStripsBulletAndNumberPrefixes(t *testing.T) {
	raw := "- 跑测试\n* 补文档\n1. 提交\n2) 推送\n• 打标签"
	want := []string{"跑测试", "补文档", "提交", "推送", "打标签"}
	got := Parse(raw, 5)
	if len(got) != len(want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("suggestion %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestParseDropsBlankAndOverlongLines(t *testing.T) {
	long := strings.Repeat("很", MaxSuggestionRunes+1)
	got := Parse("跑测试\n\n   \n"+long+"\n提交", 5)
	want := []string{"跑测试", "提交"}
	if len(got) != len(want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("suggestion %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// 长度按 rune 判定：中文提示词按字节算会被误杀。
func TestParseKeepsChineseLineAtRuneLimit(t *testing.T) {
	line := strings.Repeat("测", MaxSuggestionRunes)
	got := Parse(line, 3)
	if len(got) != 1 || got[0] != line {
		t.Fatalf("got %q, want the full %d-rune line kept", got, MaxSuggestionRunes)
	}
}

func TestParseDedupesAndTruncatesToCount(t *testing.T) {
	got := Parse("跑测试\n跑测试\n补文档\n提交\n推送", 2)
	want := []string{"跑测试", "补文档"}
	if len(got) != len(want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("suggestion %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestParseReturnsNilForUnusableOutput(t *testing.T) {
	for _, raw := range []string{"", "   \n\n  ", "-\n*\n1."} {
		if got := Parse(raw, 3); len(got) != 0 {
			t.Errorf("Parse(%q) = %q, want empty", raw, got)
		}
	}
}
