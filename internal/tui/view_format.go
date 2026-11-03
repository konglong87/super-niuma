package tui

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

var terminalANSIEscapeRE = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)

func wrapModeHintLine(line string, width int) []string {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	label, rest, ok := strings.Cut(line, " ")
	if !ok {
		return wrapModeHintParts([]string{line}, width)
	}
	parts := []string{label}
	for _, part := range strings.Split(rest, "  ") {
		if part = strings.TrimSpace(part); part != "" {
			parts = append(parts, part)
		}
	}
	return wrapModeHintParts(parts, width)
}

func wrapModeHintParts(parts []string, width int) []string {
	if len(parts) == 0 {
		return nil
	}
	width = max(24, width)
	label := strings.TrimSpace(parts[0])
	if len(parts) == 1 {
		return wrapDisplayChunks(label, width)
	}
	lines := []string{}
	line := label
	for _, raw := range parts[1:] {
		part := strings.TrimSpace(raw)
		if part == "" {
			continue
		}
		candidate := line + "  " + part
		if lipgloss.Width(candidate) <= width {
			line = candidate
			continue
		}
		if line != "" {
			lines = append(lines, line)
		}
		line = ""
		for _, chunk := range wrapDisplayChunks(part, max(8, width-2)) {
			wrapped := "  " + chunk
			if line != "" {
				lines = append(lines, line)
			}
			line = wrapped
		}
	}
	if line != "" || len(lines) == 0 {
		lines = append(lines, line)
	}
	return lines
}

func cleanANSIWhitespaceLines(text string) string {
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		visible := terminalANSIEscapeRE.ReplaceAllString(line, "")
		if strings.TrimSpace(visible) == "" {
			lines[i] = ""
		}
	}
	return strings.Join(lines, "\n")
}

func truncateDisplay(text string, limit int) string {
	if limit <= 0 || runewidth.StringWidth(text) <= limit {
		return text
	}
	if limit <= 3 {
		return strings.Repeat(".", limit)
	}
	var b strings.Builder
	width := 0
	for _, r := range text {
		rw := runewidth.RuneWidth(r)
		if width+rw > limit-3 {
			break
		}
		b.WriteRune(r)
		width += rw
	}
	return b.String() + "..."
}

func padDisplayLine(text string, width int) string {
	if width <= 0 {
		return text
	}
	visible := terminalANSIEscapeRE.ReplaceAllString(text, "")
	if pad := width - runewidth.StringWidth(visible); pad > 0 {
		return text + strings.Repeat(" ", pad)
	}
	return text
}
