package feishu

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	channelcontract "github.com/konglong87/super-niuma/internal/channel"
)

func TestRenderTimelineCardsPaginatesSixToolsIntoIndividualPanels(t *testing.T) {
	entries := make([]channelcontract.TimelineEntry, 0, 7)
	for index := 0; index < 7; index++ {
		tool := channelcontract.ToolProgress{ID: fmt.Sprintf("tool-%d", index+1), Name: "Read", Status: channelcontract.ToolStatusComplete, Command: fmt.Sprintf("file-%d.go", index+1)}
		entries = append(entries, channelcontract.TimelineEntry{ID: "tool:" + tool.ID, Kind: channelcontract.TimelineTool, Tool: tool})
	}
	pages, err := RenderTimelineCards(channelcontract.CardState{Status: channelcontract.CardCompleted, Timeline: entries}, CardOptions{CallbackToken: "opaque-token"})
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 2 {
		t.Fatalf("pages = %d, want 2", len(pages))
	}
	if got := countTopLevelTag(t, pages[0].Card, cardTagCollapsiblePanel); got != 6 {
		t.Fatalf("first page tool panels = %d, want 6", got)
	}
	if got := countTopLevelTag(t, pages[1].Card, cardTagCollapsiblePanel); got != 1 {
		t.Fatalf("second page tool panels = %d, want 1", got)
	}
	assertTopLevelElement(t, pages[0].Card, "tool_details_panel_0", false)
	assertTopLevelElement(t, pages[0].Card, "tool_details_panel_5", false)
	assertTopLevelElement(t, pages[1].Card, "tool_details_panel_6", false)
	if hasTopLevelElement(t, pages[0].Card, "controls") {
		t.Fatal("non-latest page retained controls")
	}
	if !hasTopLevelElement(t, pages[1].Card, "controls") {
		t.Fatal("latest page missing controls")
	}
	if got := cardHeaderTitle(t, pages[0].Card); got != "已完成 · 1/2" {
		t.Fatalf("first title = %q", got)
	}
	if got := cardHeaderTitle(t, pages[1].Card); got != "已完成 · 2/2" {
		t.Fatalf("second title = %q", got)
	}
}

func TestRenderTimelineCardsPreservesMixedEntryOrder(t *testing.T) {
	state := channelcontract.CardState{Status: channelcontract.CardRunning, Timeline: []channelcontract.TimelineEntry{
		{Kind: channelcontract.TimelineText, Text: "before"},
		{ID: "tool:tool-1", Kind: channelcontract.TimelineTool, Tool: channelcontract.ToolProgress{ID: "tool-1", Name: "Bash", Status: channelcontract.ToolStatusRunning, Command: "go test ./..."}},
		{Kind: channelcontract.TimelineText, Text: "after"},
	}}
	pages, err := RenderTimelineCards(state, CardOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 {
		t.Fatalf("pages = %d, want 1", len(pages))
	}
	elements := topLevelElements(t, pages[0].Card)
	if len(elements) != 3 || elements[0]["tag"] != cardTagMarkdown || elements[1]["tag"] != cardTagCollapsiblePanel || elements[2]["tag"] != cardTagMarkdown {
		t.Fatalf("mixed timeline order = %+v", elements)
	}
	if elements[0]["content"] != "before" || elements[2]["content"] != "after" {
		t.Fatalf("mixed timeline text = %+v", elements)
	}
	if got := elements[1]["header"].(map[string]interface{})["title"].(map[string]interface{})["content"]; got != "1. Bash · 运行中" {
		t.Fatalf("tool panel title = %q", got)
	}
}

func TestRenderTimelineCardsSplitsLongUTF8TextWithinCardLimit(t *testing.T) {
	text := strings.Repeat("第一段内容。\n\n", 5000)
	pages, err := RenderTimelineCards(channelcontract.CardState{Status: channelcontract.CardCompleted, Timeline: []channelcontract.TimelineEntry{{Kind: channelcontract.TimelineText, Text: text}}}, CardOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) < 2 {
		t.Fatalf("pages = %d, want long text split", len(pages))
	}
	var joined strings.Builder
	for _, page := range pages {
		if len(page.Card) > maxCardBytes {
			t.Fatalf("page %d bytes = %d, limit = %d", page.Index, len(page.Card), maxCardBytes)
		}
		for _, element := range topLevelElements(t, page.Card) {
			if element["tag"] == cardTagMarkdown {
				content := element["content"].(string)
				if !utf8.ValidString(content) {
					t.Fatalf("page %d contains invalid UTF-8", page.Index)
				}
				joined.WriteString(content)
			}
		}
	}
	if joined.String() != text {
		t.Fatalf("split text did not round trip: got %d bytes, want %d", joined.Len(), len(text))
	}
}

func TestRenderTimelineCardsBalancesFencedCodeAcrossPages(t *testing.T) {
	text := "```text\n" + strings.Repeat("x", 70000) + "\n```"
	pages, err := RenderTimelineCards(channelcontract.CardState{Status: channelcontract.CardCompleted, Timeline: []channelcontract.TimelineEntry{{Kind: channelcontract.TimelineText, Text: text}}}, CardOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) < 2 {
		t.Fatalf("pages = %d, want fenced code split", len(pages))
	}
	for _, page := range pages {
		for _, element := range topLevelElements(t, page.Card) {
			if content, ok := element["content"].(string); ok && strings.Count(content, "```")%2 != 0 {
				t.Fatalf("page %d has unbalanced fenced code: %q", page.Index, content)
			}
		}
	}
}

func TestRenderTimelineCardsPaginatesBeforeElementLimit(t *testing.T) {
	entries := make([]channelcontract.TimelineEntry, 0, maxCardElements+10)
	for index := 0; index < maxCardElements+10; index++ {
		entries = append(entries, channelcontract.TimelineEntry{Kind: channelcontract.TimelineNotice, Text: fmt.Sprintf("notice-%d", index)})
	}
	pages, err := RenderTimelineCards(channelcontract.CardState{Status: channelcontract.CardRunning, Timeline: entries}, CardOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) < 2 {
		t.Fatalf("pages = %d, want element-limit pagination", len(pages))
	}
	for _, page := range pages {
		if got := len(topLevelElements(t, page.Card)); got > maxCardElements {
			t.Fatalf("page %d elements = %d, limit = %d", page.Index, got, maxCardElements)
		}
	}
}

func TestRenderTimelineCardsAppendsTerminalFailureNotice(t *testing.T) {
	pages, err := RenderTimelineCards(channelcontract.CardState{Status: channelcontract.CardFailed, Timeline: []channelcontract.TimelineEntry{{Kind: channelcontract.TimelineText, Text: "partial result"}}}, CardOptions{})
	if err != nil {
		t.Fatal(err)
	}
	elements := topLevelElements(t, pages[len(pages)-1].Card)
	if got := elements[len(elements)-1]["content"]; got != "> 执行失败，已保留此前产生的内容。" {
		t.Fatalf("terminal failure notice = %q", got)
	}
}

func topLevelElements(t *testing.T, card json.RawMessage) []map[string]interface{} {
	t.Helper()
	var decoded struct {
		Body struct {
			Elements []map[string]interface{} `json:"elements"`
		} `json:"body"`
	}
	if err := json.Unmarshal(card, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded.Body.Elements
}

func countTopLevelTag(t *testing.T, card json.RawMessage, tag string) int {
	t.Helper()
	count := 0
	for _, element := range topLevelElements(t, card) {
		if element["tag"] == tag {
			count++
		}
	}
	return count
}

func assertTopLevelElement(t *testing.T, card json.RawMessage, elementID string, expanded bool) {
	t.Helper()
	for _, element := range topLevelElements(t, card) {
		if element["element_id"] == elementID {
			if element["expanded"] != expanded {
				t.Fatalf("element %s expanded = %v", elementID, element["expanded"])
			}
			return
		}
	}
	t.Fatalf("element %s missing", elementID)
}

func hasTopLevelElement(t *testing.T, card json.RawMessage, elementID string) bool {
	t.Helper()
	for _, element := range topLevelElements(t, card) {
		if element["element_id"] == elementID {
			return true
		}
	}
	return false
}

func cardHeaderTitle(t *testing.T, card json.RawMessage) string {
	t.Helper()
	var decoded struct {
		Header struct {
			Title struct {
				Content string `json:"content"`
			} `json:"title"`
		} `json:"header"`
	}
	if err := json.Unmarshal(card, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded.Header.Title.Content
}
