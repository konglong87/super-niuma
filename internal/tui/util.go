// util.go 是本包内的通用小工具：换行、截断、payload 取值与数值夹取。

package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

func dividerLine(width int) string {
	return strings.Repeat("─", max(20, width-2))
}

func indentBlock(text string, spaces int) string {
	if spaces <= 0 || text == "" {
		return text
	}
	prefix := strings.Repeat(" ", spaces)
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}

func wrapForViewport(text string, width int) string {
	text = strings.TrimSpace(text)
	if text == "" || width <= 0 {
		return text
	}
	return lipgloss.NewStyle().Width(width).Render(text)
}

func hardWrapRenderedText(text string, width int) string {
	if text == "" || width <= 0 {
		return text
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		visible := terminalANSIEscapeRE.ReplaceAllString(line, "")
		if runewidth.StringWidth(visible) <= width {
			continue
		}
		lines[i] = lipgloss.NewStyle().Width(width).Render(line)
	}
	return strings.Join(lines, "\n")
}

func wrapPrefixedLine(prefix, text string, width int) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return strings.TrimSpace(prefix)
	}
	if width <= 0 {
		width = 80
	}
	indent := strings.Repeat(" ", len([]rune(prefix)))
	parts := strings.Split(text, " · ")
	line := prefix + parts[0]
	var lines []string
	for _, part := range parts[1:] {
		if part == "" {
			continue
		}
		candidate := line + " · " + part
		if len([]rune(candidate)) > width {
			lines = append(lines, line)
			line = indent + part
			continue
		}
		line = candidate
	}
	lines = append(lines, line)
	return strings.Join(lines, "\n")
}

// truncate caps text at limit terminal columns, never splitting a multi-byte rune.
func truncate(text string, limit int) string {
	return truncateDisplay(text, limit)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func abbreviateHome(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Clean(path)
	}
	cleanPath := filepath.Clean(path)
	cleanHome := filepath.Clean(home)
	if cleanPath == cleanHome {
		return "~"
	}
	if strings.HasPrefix(cleanPath, cleanHome+string(filepath.Separator)) {
		return "~" + strings.TrimPrefix(cleanPath, cleanHome)
	}
	return cleanPath
}

func parsePayload(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil
	}
	return payload
}

func payloadString(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	value, ok := payload[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case float64:
		return fmt.Sprintf("%.0f", typed)
	case bool:
		return fmt.Sprintf("%t", typed)
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func payloadNumber(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	value, ok := payload[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case float64:
		return fmt.Sprintf("%.0f", typed)
	case int:
		return fmt.Sprintf("%d", typed)
	case string:
		return strings.TrimSpace(typed)
	default:
		return ""
	}
}

func formatCount(label, value string) string {
	if value == "" {
		return ""
	}
	return value + " " + label
}

func formatKV(label, value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return label + "=" + strings.TrimSpace(value)
}

func formatProgressKV(label1, value1, label2, value2 string) string {
	return strings.Trim(strings.Join([]string{formatKV(label1, value1), formatKV(label2, value2)}, " "), " ")
}

func formatDuration(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	ms, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return value + "ms"
	}
	if ms < 1000 {
		return strconv.FormatInt(ms, 10) + "ms"
	}
	return runningDurationLabel(time.Duration(ms) * time.Millisecond)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func clamp(value, low, high int) int {
	if high < low {
		return low
	}
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func lineCount(text string) int {
	if text == "" {
		return 0
	}
	return strings.Count(text, "\n") + 1
}

func visualLineCount(text string, width int) int {
	if text == "" {
		return 0
	}
	if width <= 0 {
		return lineCount(text)
	}
	count := 0
	for _, line := range strings.Split(text, "\n") {
		visible := terminalANSIEscapeRE.ReplaceAllString(line, "")
		lineWidth := runewidth.StringWidth(visible)
		if lineWidth == 0 {
			count++
			continue
		}
		count += (lineWidth + width - 1) / width
	}
	return count
}

func oneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}
