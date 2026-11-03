// markdown_table.go 单独处理表格：流式稳定化、网格渲染与窄屏改列表。

package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

type markdownTableReplacement struct {
	token string
	value string
}

func renderMarkdownTables(content string, width int) (string, []markdownTableReplacement) {
	lines := strings.Split(content, "\n")
	var out []string
	var replacements []markdownTableReplacement
	inFence := false
	for i := 0; i < len(lines); {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "```") {
			inFence = !inFence
			out = append(out, lines[i])
			i++
			continue
		}
		if inFence {
			out = append(out, lines[i])
			i++
			continue
		}
		if i+1 < len(lines) && isMarkdownTableRow(lines[i]) && isMarkdownTableSeparator(lines[i+1]) {
			start := i
			i += 2
			for i < len(lines) && isMarkdownTableRow(lines[i]) {
				i++
			}
			token := fmt.Sprintf("GOCLAUDETABLE%dPLACEHOLDER", len(replacements))
			replacements = append(replacements, markdownTableReplacement{
				token: token,
				value: renderMarkdownTableBlock(lines[start:i], width),
			})
			out = append(out, token)
			continue
		}
		out = append(out, lines[i])
		i++
	}
	return strings.Join(out, "\n"), replacements
}

func stabilizeStreamingMarkdownTables(content string) string {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		return content
	}
	complete := make([]bool, len(lines))
	for i := 0; i < len(lines)-1; i++ {
		complete[i] = true
	}
	if strings.HasSuffix(content, "\n") {
		complete[len(lines)-1] = true
	}
	var out []string
	inFence := false
	for i := 0; i < len(lines); {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			out = append(out, lines[i])
			i++
			continue
		}
		if inFence {
			out = append(out, lines[i])
			i++
			continue
		}
		if i+1 < len(lines) && isMarkdownTableRow(lines[i]) && isMarkdownTableSeparator(lines[i+1]) {
			start := i
			i += 2
			for i < len(lines) && isMarkdownTableRow(lines[i]) {
				if !complete[i] {
					break
				}
				i++
			}
			if i-start < 3 {
				for i < len(lines) && isMarkdownTableRow(lines[i]) {
					i++
				}
				continue
			}
			out = append(out, lines[start:i]...)
			if i < len(lines) && isMarkdownTableRow(lines[i]) && !complete[i] {
				i++
			}
			continue
		}
		if !complete[i] && isMarkdownTableRow(lines[i]) {
			i++
			continue
		}
		if i+1 < len(lines) && isMarkdownTableRow(lines[i]) && !complete[i+1] && looksLikeMarkdownTableSeparatorPrefix(lines[i+1]) {
			i += 2
			continue
		}
		out = append(out, lines[i])
		i++
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}

func restoreMarkdownTables(rendered string, replacements []markdownTableReplacement) string {
	if len(replacements) == 0 {
		return rendered
	}
	byToken := make(map[string]string, len(replacements))
	for _, replacement := range replacements {
		byToken[replacement.token] = replacement.value
	}
	lines := strings.Split(rendered, "\n")
	for i, line := range lines {
		visible := terminalANSIEscapeRE.ReplaceAllString(line, "")
		if table, ok := byToken[strings.TrimSpace(visible)]; ok {
			lines[i] = table
			continue
		}
		for token, table := range byToken {
			if strings.Contains(line, token) {
				line = strings.ReplaceAll(line, token, table)
			}
		}
		lines[i] = line
	}
	return strings.Join(lines, "\n")
}

func isMarkdownTableRow(line string) bool {
	trimmed := strings.TrimSpace(line)
	if !strings.Contains(trimmed, "|") || strings.HasPrefix(trimmed, "```") {
		return false
	}
	cells := splitMarkdownTableRow(trimmed)
	return len(cells) >= 2
}

func isMarkdownTableSeparator(line string) bool {
	cells := splitMarkdownTableRow(strings.TrimSpace(line))
	if len(cells) < 2 {
		return false
	}
	for _, cell := range cells {
		cell = strings.TrimSpace(cell)
		if cell == "" {
			return false
		}
		cell = strings.Trim(cell, ":")
		if len(cell) < 3 || strings.Trim(cell, "-") != "" {
			return false
		}
	}
	return true
}

func looksLikeMarkdownTableSeparatorPrefix(line string) bool {
	trimmed := strings.TrimSpace(line)
	if !strings.Contains(trimmed, "|") || !strings.Contains(trimmed, "-") {
		return false
	}
	for _, r := range trimmed {
		switch r {
		case '|', '-', ':', ' ':
			continue
		default:
			return false
		}
	}
	return true
}

func splitMarkdownTableRow(line string) []string {
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, "|") {
		line = strings.TrimPrefix(line, "|")
	}
	if strings.HasSuffix(line, "|") {
		line = strings.TrimSuffix(line, "|")
	}
	raw := strings.Split(line, "|")
	cells := make([]string, 0, len(raw))
	for _, cell := range raw {
		cells = append(cells, strings.TrimSpace(cell))
	}
	return cells
}

func renderMarkdownTableBlock(lines []string, width int) string {
	if len(lines) < 2 {
		return strings.Join(lines, "\n")
	}
	rows := make([][]string, 0, len(lines)-1)
	header := splitMarkdownTableRow(lines[0])
	rows = append(rows, header)
	for _, line := range lines[2:] {
		row := splitMarkdownTableRow(line)
		for len(row) < len(header) {
			row = append(row, "")
		}
		if len(row) > len(header) {
			row = row[:len(header)]
		}
		rows = append(rows, row)
	}
	if shouldRenderMarkdownTableAsList(rows, width) {
		return renderMarkdownTableAsList(rows, width)
	}
	widths := markdownTableColumnWidths(rows, width)
	var b strings.Builder
	b.WriteString(markdownTableBorder(widths, "┌", "┬", "┐"))
	b.WriteString("\n")
	b.WriteString(strings.Join(markdownTableRowLines(rows[0], widths), "\n"))
	b.WriteString("\n")
	b.WriteString(markdownTableBorder(widths, "├", "┼", "┤"))
	bodyBorder := markdownTableBorder(widths, "├", "┼", "┤")
	for i, row := range rows[1:] {
		b.WriteString("\n")
		b.WriteString(strings.Join(markdownTableRowLines(row, widths), "\n"))
		if i < len(rows[1:])-1 {
			b.WriteString("\n")
			b.WriteString(bodyBorder)
		}
	}
	b.WriteString("\n")
	b.WriteString(markdownTableBorder(widths, "└", "┴", "┘"))
	return b.String()
}

func shouldRenderMarkdownTableAsList(rows [][]string, width int) bool {
	if len(rows) < 3 || len(rows[0]) < 4 {
		return false
	}
	maxCellWidth := 0
	for _, row := range rows[1:] {
		for _, cell := range row {
			maxCellWidth = max(maxCellWidth, runewidth.StringWidth(stripMarkdownCellStyles(cell)))
		}
	}
	// Keep compact data tables as grids. Only narrative tables with long cells
	// become grouped lists, matching the product UI plan without changing table
	// border styling for normal tables.
	return maxCellWidth > 24
}

func renderMarkdownTableAsList(rows [][]string, width int) string {
	if len(rows) < 2 {
		return ""
	}
	headers := rows[0]
	bodyWidth := max(16, width-2)
	var blocks []string
	for _, row := range rows[1:] {
		if len(row) == 0 {
			continue
		}
		cells := make([]string, len(headers))
		for i := range headers {
			if i < len(row) {
				cells[i] = stripMarkdownCellStyles(row[i])
			}
		}
		titleParts := []string{}
		if strings.TrimSpace(cells[0]) != "" {
			titleParts = append(titleParts, strings.TrimSpace(cells[0]))
		}
		if len(cells) > 1 && strings.TrimSpace(cells[1]) != "" {
			titleParts = append(titleParts, strings.TrimSpace(cells[1]))
		}
		if len(titleParts) == 0 {
			titleParts = append(titleParts, "row")
		}
		lines := []string{assistantMarkerStyle.Render("•") + " " + markdownListTitleStyle.Render(strings.Join(titleParts, " · "))}
		for i := 2; i < len(headers) && i < len(cells); i++ {
			label := stripMarkdownCellStyles(headers[i])
			value := strings.TrimSpace(cells[i])
			if label == "" || value == "" {
				continue
			}
			wrapped := lipgloss.NewStyle().Width(bodyWidth).Render(label + ": " + value)
			for _, line := range strings.Split(wrapped, "\n") {
				lines = append(lines, markdownListDetailStyle.Render("  "+line))
			}
		}
		blocks = append(blocks, strings.Join(lines, "\n"))
	}
	return strings.Join(blocks, "\n")
}

func markdownTableColumnWidths(rows [][]string, maxWidth int) []int {
	cols := 0
	for _, row := range rows {
		if len(row) > cols {
			cols = len(row)
		}
	}
	if cols == 0 {
		return nil
	}
	widths := make([]int, cols)
	for _, row := range rows {
		for i, cell := range row {
			widths[i] = max(widths[i], runewidth.StringWidth(stripMarkdownCellStyles(cell)))
		}
	}
	for i := range widths {
		widths[i] = clamp(widths[i], 3, 32)
	}
	available := maxWidth - (cols + 1) - cols*2
	if available < cols*3 {
		available = cols * 3
	}
	for sumInts(widths) > available {
		largest := 0
		for i := range widths {
			if widths[i] > widths[largest] {
				largest = i
			}
		}
		if widths[largest] <= 3 {
			break
		}
		widths[largest]--
	}
	return widths
}

func markdownTableBorder(widths []int, left, middle, right string) string {
	var b strings.Builder
	b.WriteString(left)
	for i, width := range widths {
		if i > 0 {
			b.WriteString(middle)
		}
		b.WriteString(strings.Repeat("─", width+2))
	}
	b.WriteString(right)
	return b.String()
}

func markdownTableRowLines(cells []string, widths []int) []string {
	wrapped := make([][]string, len(widths))
	height := 1
	for i, width := range widths {
		cell := ""
		if i < len(cells) {
			cell = stripMarkdownCellStyles(cells[i])
		}
		wrapped[i] = wrapMarkdownTableCell(cell, width)
		height = max(height, len(wrapped[i]))
	}
	lines := make([]string, 0, height)
	for row := 0; row < height; row++ {
		lines = append(lines, markdownTableRowLine(wrapped, widths, row))
	}
	return lines
}

func markdownTableRowLine(cells [][]string, widths []int, row int) string {
	var b strings.Builder
	b.WriteString("│")
	for i, width := range widths {
		cell := ""
		if row < len(cells[i]) {
			cell = cells[i][row]
		}
		b.WriteString(" ")
		b.WriteString(cell)
		b.WriteString(strings.Repeat(" ", max(0, width-runewidth.StringWidth(cell))))
		b.WriteString(" │")
	}
	return b.String()
}

func wrapMarkdownTableCell(cell string, width int) []string {
	cell = strings.TrimSpace(cell)
	if cell == "" {
		return []string{""}
	}
	var lines []string
	var b strings.Builder
	current := 0
	for _, r := range cell {
		rw := runewidth.RuneWidth(r)
		if current > 0 && current+rw > width {
			lines = append(lines, strings.TrimSpace(b.String()))
			b.Reset()
			current = 0
		}
		b.WriteRune(r)
		current += rw
	}
	if b.Len() > 0 {
		lines = append(lines, strings.TrimSpace(b.String()))
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func stripMarkdownCellStyles(cell string) string {
	cell = strings.TrimSpace(cell)
	if strings.HasPrefix(cell, "`") && strings.HasSuffix(cell, "`") && len(cell) >= 2 {
		cell = strings.Trim(cell, "`")
	}
	cell = strings.ReplaceAll(cell, "**", "")
	return cell
}

func sumInts(values []int) int {
	total := 0
	for _, value := range values {
		total += value
	}
	return total
}
