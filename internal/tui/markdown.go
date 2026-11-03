// markdown.go 是助手正文的 markdown 渲染管线，含 rich inline 富文本标签。

package tui

import (
	"fmt"
	"html"
	"regexp"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
)

func (m Model) renderMarkdown(content string) string {
	if !looksLikeMarkdown(content) {
		return wrapForViewport(content, m.contentWidth())
	}
	return renderMarkdownForWidth(content, m.contentWidth())
}

func (m Model) renderStreamingMarkdown(content string) string {
	content = stabilizeStreamingMarkdownTables(content)
	if strings.TrimSpace(content) == "" {
		return ""
	}
	return m.renderMarkdown(content)
}

func looksLikeMarkdown(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		switch {
		case strings.HasPrefix(trimmed, "# "),
			strings.HasPrefix(trimmed, "## "),
			strings.HasPrefix(trimmed, "### "),
			strings.HasPrefix(trimmed, "#### "),
			strings.HasPrefix(trimmed, "##### "),
			strings.HasPrefix(trimmed, "###### "),
			strings.HasPrefix(trimmed, "- "),
			strings.HasPrefix(trimmed, "* "),
			strings.HasPrefix(trimmed, "> "),
			strings.HasPrefix(trimmed, "```"),
			strings.Contains(trimmed, "|---"),
			strings.Contains(trimmed, "---|"),
			strings.Contains(trimmed, "**"),
			strings.Contains(trimmed, "`"),
			strings.Contains(strings.ToLower(trimmed), "<span"),
			strings.Contains(strings.ToLower(trimmed), "<font"):
			return true
		}
	}
	return false
}

// headingMissingSpaceRE 匹配行首缺少空格的 ATX 标题：0-3 个前导空格 + 2~6 个 #
// + 一个非 # 非空格字符。只处理 2~6 级，不碰单个 #，避免把 "#1" "#hashtag"
// "#FF0000" 这类行首单井号的普通文本误判成一级标题。
var headingMissingSpaceRE = regexp.MustCompile(`^( {0,3})(#{2,6})([^#\s])`)

// normalizeMarkdownHeadings 为缺少空格的 ATX 标题补上 CommonMark 要求的空格
// （如 "###总结" -> "### 总结"），使模型漏写空格的标题仍能被 glamour 识别并渲染。
// 代码围栏内的内容保持原样，避免破坏示例代码或展示用的 markdown 源码。
func normalizeMarkdownHeadings(content string) string {
	if !strings.Contains(content, "#") {
		return content
	}
	lines := strings.Split(content, "\n")
	inFence := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		lines[i] = headingMissingSpaceRE.ReplaceAllString(line, "${1}${2} ${3}")
	}
	return strings.Join(lines, "\n")
}

// markdownRenderHook, when non-nil, fires on every full markdown render.
// Test-only seam for asserting that View() does not re-render the live markdown
// on every frame (Bubble Tea calls View() far more often than content changes).
var markdownRenderHook func()

func renderMarkdownForWidth(content string, width int) string {
	return renderMarkdownForWidthWithStyle(content, width, assistantMarkdownStyle)
}

func renderMarkdownForWidthWithStyle(content string, width int, style ansi.StyleConfig) string {
	if markdownRenderHook != nil {
		markdownRenderHook()
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	if width <= 0 {
		width = 80
	}
	content = normalizeMarkdownHeadings(content)
	content, tables := renderMarkdownTables(content, width)
	preprocessed, rich := extractRichMarkdownInlineStyles(content)
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStyles(style),
		glamour.WithWordWrap(width),
		glamour.WithPreservedNewLines(),
	)
	if err != nil {
		return restoreMarkdownTables(renderRichMarkdownFallback(content, width), tables)
	}
	rendered, err := renderer.Render(preprocessed)
	if err != nil {
		return restoreMarkdownTables(renderRichMarkdownFallback(content, width), tables)
	}
	rendered = restoreRichMarkdownInlineStyles(rendered, rich)
	rendered = restoreMarkdownTables(rendered, tables)
	return strings.TrimSpace(cleanANSIWhitespaceLines(rendered))
}

func newThinkingMarkdownStyle() ansi.StyleConfig {
	style := newAssistantMarkdownStyle()
	bodyColor := "245"
	mutedColor := "242"
	codeBackground := "234"
	style.Document.Color = stringPtr(bodyColor)
	style.Paragraph.Color = stringPtr(bodyColor)
	style.Text.Color = stringPtr(bodyColor)
	style.BlockQuote.Color = stringPtr(mutedColor)
	style.Heading.Color = stringPtr(string(tuiSecondaryColor))
	style.Strong.Color = stringPtr("248")
	style.Emph.Color = stringPtr(bodyColor)
	style.Item.Color = stringPtr(mutedColor)
	style.Enumeration.Color = stringPtr(mutedColor)
	style.Link.Color = stringPtr(string(tuiSecondaryColor))
	style.LinkText.Color = stringPtr(string(tuiSecondaryColor))
	style.Code.Color = stringPtr("248")
	style.Code.BackgroundColor = stringPtr(codeBackground)
	style.CodeBlock.Color = stringPtr(bodyColor)
	style.CodeBlock.BackgroundColor = stringPtr(codeBackground)
	style.Table.Color = stringPtr(bodyColor)
	return style
}

type richMarkdownInline struct {
	token string
	value string
}

type richMarkdownStyle struct {
	foreground    string
	background    string
	bold          bool
	italic        bool
	underline     bool
	strikethrough bool
}

// Rich markdown HTML support is intentionally narrow: only inline span/font
// color and text-style attributes are converted to ANSI terminal styling.
var (
	richSpanRE       = regexp.MustCompile(`(?is)<span\s+([^>]*)>(.*?)</span>`)
	richFontRE       = regexp.MustCompile(`(?is)<font\s+([^>]*)>(.*?)</font>`)
	richDoubleAttrRE = regexp.MustCompile(`(?is)(style|color)\s*=\s*"([^"]*)"`)
	richSingleAttrRE = regexp.MustCompile(`(?is)(style|color)\s*=\s*'([^']*)'`)
	richStyleSplitRE = regexp.MustCompile(`\s*;\s*`)
)

func extractRichMarkdownInlineStyles(content string) (string, []richMarkdownInline) {
	replacements := []richMarkdownInline{}
	content = replaceRichInlineTags(content, richSpanRE, &replacements)
	content = replaceRichInlineTags(content, richFontRE, &replacements)
	return content, replacements
}

func replaceRichInlineTags(content string, re *regexp.Regexp, replacements *[]richMarkdownInline) string {
	return re.ReplaceAllStringFunc(content, func(match string) string {
		parts := re.FindStringSubmatch(match)
		if len(parts) != 3 {
			return match
		}
		style, ok := parseRichInlineStyle(parts[1])
		if !ok {
			return html.UnescapeString(parts[2])
		}
		token := fmt.Sprintf("GCCMARKDOWNRICH%dX", len(*replacements))
		*replacements = append(*replacements, richMarkdownInline{
			token: token,
			value: renderRichInlineText(html.UnescapeString(parts[2]), style),
		})
		return token
	})
}

func parseRichInlineStyle(attrs string) (richMarkdownStyle, bool) {
	var style richMarkdownStyle
	for _, match := range richAttributeMatches(attrs) {
		name := strings.ToLower(strings.TrimSpace(match[0]))
		value := strings.TrimSpace(match[1])
		if name == "color" {
			if color, ok := normalizeRichMarkdownColor(value); ok {
				style.foreground = color
			}
			continue
		}
		for _, declaration := range richStyleSplitRE.Split(value, -1) {
			key, val, ok := strings.Cut(declaration, ":")
			if !ok {
				continue
			}
			key = strings.ToLower(strings.TrimSpace(key))
			val = strings.ToLower(strings.TrimSpace(val))
			switch key {
			case "color":
				if color, ok := normalizeRichMarkdownColor(val); ok {
					style.foreground = color
				}
			case "background-color":
				if color, ok := normalizeRichMarkdownColor(val); ok {
					style.background = color
				}
			case "font-weight":
				style.bold = val == "bold" || val == "bolder" || val == "600" || val == "700" || val == "800" || val == "900"
			case "font-style":
				style.italic = val == "italic" || val == "oblique"
			case "text-decoration":
				style.underline = strings.Contains(val, "underline")
				style.strikethrough = strings.Contains(val, "line-through")
			}
		}
	}
	return style, style.foreground != "" || style.background != "" || style.bold || style.italic || style.underline || style.strikethrough
}

func richAttributeMatches(attrs string) [][2]string {
	matches := [][2]string{}
	for _, match := range richDoubleAttrRE.FindAllStringSubmatch(attrs, -1) {
		if len(match) == 3 {
			matches = append(matches, [2]string{match[1], match[2]})
		}
	}
	for _, match := range richSingleAttrRE.FindAllStringSubmatch(attrs, -1) {
		if len(match) == 3 {
			matches = append(matches, [2]string{match[1], match[2]})
		}
	}
	return matches
}

func normalizeRichMarkdownColor(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.Trim(value, `"'`)
	if value == "" {
		return "", false
	}
	if strings.HasPrefix(value, "#") {
		if len(value) == 4 {
			return "#" + strings.Repeat(value[1:2], 2) + strings.Repeat(value[2:3], 2) + strings.Repeat(value[3:4], 2), true
		}
		if len(value) == 7 {
			for _, r := range value[1:] {
				if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
					return "", false
				}
			}
			return strings.ToUpper(value), true
		}
		return "", false
	}
	named := map[string]string{
		"black":   "0",
		"red":     "196",
		"green":   "46",
		"yellow":  "226",
		"blue":    "33",
		"magenta": "201",
		"purple":  "141",
		"cyan":    "51",
		"orange":  "208",
		"gray":    "245",
		"grey":    "245",
		"white":   "15",
	}
	color, ok := named[value]
	return color, ok
}

func renderRichInlineText(text string, style richMarkdownStyle) string {
	renderer := lipgloss.NewStyle()
	if style.foreground != "" {
		renderer = renderer.Foreground(lipgloss.Color(style.foreground))
	}
	if style.background != "" {
		renderer = renderer.Background(lipgloss.Color(style.background))
	}
	if style.bold {
		renderer = renderer.Bold(true)
	}
	if style.italic {
		renderer = renderer.Italic(true)
	}
	if style.underline {
		renderer = renderer.Underline(true)
	}
	if style.strikethrough {
		renderer = renderer.Strikethrough(true)
	}
	return renderer.Render(text)
}

func restoreRichMarkdownInlineStyles(rendered string, replacements []richMarkdownInline) string {
	for _, replacement := range replacements {
		rendered = strings.ReplaceAll(rendered, replacement.token, replacement.value)
	}
	return rendered
}

func renderRichMarkdownFallback(content string, width int) string {
	preprocessed, rich := extractRichMarkdownInlineStyles(content)
	return restoreRichMarkdownInlineStyles(wrapForViewport(preprocessed, width), rich)
}

func newAssistantMarkdownStyle() ansi.StyleConfig {
	style := styles.DarkStyleConfig
	textColor := string(tuiBodyColor)
	mutedColor := string(tuiSecondaryColor)
	faintColor := "246"
	accentColor := string(tuiLinkPathColor)
	codeColor := string(tuiBodyColor)
	codeBackground := "235"
	inlineCodeColor := string(tuiInlineCodeColor)
	inlineCodeBackground := string(tuiInlineCodeBackground)
	style.Document.Color = stringPtr(textColor)
	style.Paragraph.Color = stringPtr(textColor)
	style.Text.Color = stringPtr(textColor)
	style.BlockQuote.Color = stringPtr(mutedColor)
	style.BlockQuote.IndentToken = stringPtr("│ ")
	style.Heading.Color = stringPtr(accentColor)
	style.Strong.Color = stringPtr("255")
	style.Strong.Bold = boolPtr(true)
	style.Emph.Color = stringPtr(textColor)
	style.Emph.Italic = boolPtr(true)
	style.HorizontalRule.Color = stringPtr(faintColor)
	// glamour 默认把水平线渲染成 8 个 ASCII 连字符 "--------"，视觉上是断续的
	// 短横，容易和减号列表、表格边框混淆。改用 box-drawing 实线字符 ─ 更清晰。
	style.HorizontalRule.Format = "\n────────\n"
	style.Item.Color = stringPtr(mutedColor)
	style.Enumeration.Color = stringPtr(mutedColor)
	style.Link.Color = stringPtr(string(tuiLinkPathColor))
	style.LinkText.Color = stringPtr(string(tuiLinkPathColor))
	style.Code.Color = stringPtr(inlineCodeColor)
	style.Code.BackgroundColor = stringPtr(inlineCodeBackground)
	style.CodeBlock.Color = stringPtr(codeColor)
	style.CodeBlock.BackgroundColor = stringPtr(codeBackground)
	style.Table.Color = stringPtr(textColor)
	style.Table.CenterSeparator = stringPtr("─")
	style.Table.ColumnSeparator = stringPtr("│")
	style.Table.RowSeparator = stringPtr("─")
	if style.CodeBlock.Chroma != nil {
		style.CodeBlock.Chroma.Text.Color = stringPtr("#E0E0E0")
		style.CodeBlock.Chroma.Error.Color = stringPtr("#E0E0E0")
		style.CodeBlock.Chroma.Error.BackgroundColor = stringPtr("#30343B")
		style.CodeBlock.Chroma.Comment.Color = stringPtr("#9A9A9A")
		style.CodeBlock.Chroma.Keyword.Color = stringPtr("#00B7FF")
		style.CodeBlock.Chroma.KeywordType.Color = stringPtr("#8EA8FF")
		style.CodeBlock.Chroma.Operator.Color = stringPtr("#E0E0E0")
		style.CodeBlock.Chroma.Punctuation.Color = stringPtr("#E0E0E0")
		style.CodeBlock.Chroma.Name.Color = stringPtr("#E0E0E0")
		style.CodeBlock.Chroma.NameFunction.Color = stringPtr("#00D787")
		style.CodeBlock.Chroma.LiteralString.Color = stringPtr("#E3A371")
		style.CodeBlock.Chroma.GenericDeleted.Color = stringPtr("#E0E0E0")
		style.CodeBlock.Chroma.Background.BackgroundColor = stringPtr("#30343B")
	}
	style.H2.StylePrimitive.Prefix = ""
	style.H3.StylePrimitive.Prefix = ""
	style.H4.StylePrimitive.Prefix = ""
	style.H5.StylePrimitive.Prefix = ""
	style.H6.StylePrimitive.Prefix = ""
	return style
}

func stringPtr(value string) *string {
	return &value
}

func boolPtr(value bool) *bool {
	return &value
}

func indentLines(text, prefix string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}
