// parse.go 把模型输出解析成候选列表。小模型经常夹带编号、bullet 或解释性
// 长句，这里一律丢弃而不尝试修正——一条错误的引导比没有引导更糟。

package nextsteps

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Parse 按行提取候选，返回至多 count 条。无可用内容时返回 nil，调用方据此
// 静默不展示。
func Parse(raw string, count int) []string {
	if count <= 0 {
		count = DefaultCount
	}
	var out []string
	seen := make(map[string]bool)
	for _, line := range strings.Split(raw, "\n") {
		item := stripListMarker(strings.TrimSpace(line))
		if item == "" || utf8.RuneCountInString(item) > MaxSuggestionRunes {
			continue
		}
		if seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
		if len(out) == count {
			break
		}
	}
	return out
}

// stripListMarker 去掉行首的 bullet（-、*、•）或编号（1.、2)），这些是模型
// 自己加的格式，不属于提示词内容。
func stripListMarker(line string) string {
	trimmed := strings.TrimLeft(line, "-*•·")
	if trimmed != line {
		return strings.TrimSpace(trimmed)
	}
	digits := 0
	for _, r := range line {
		if !unicode.IsDigit(r) {
			break
		}
		digits++
	}
	if digits == 0 || digits >= len(line) {
		return line
	}
	rest := line[digits:]
	// 按分隔符自身的字节长度切:「、」是三字节,按单字节切会切出乱码。
	for _, sep := range []string{".", ")", "、", "："} {
		if strings.HasPrefix(rest, sep) {
			return strings.TrimSpace(rest[len(sep):])
		}
	}
	return line
}
