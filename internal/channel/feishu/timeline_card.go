package feishu

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	channelcontract "github.com/konglong87/super-niuma/internal/channel"
)

const (
	maxToolsPerTimelineCard  = 6
	timelineCardReserveBytes = 2048
)

type timelineRenderEntry struct {
	ID          string
	Kind        channelcontract.TimelineEntryKind
	Text        string
	Tool        channelcontract.ToolProgress
	ToolOrdinal int
}

type timelinePage struct {
	entries  []timelineRenderEntry
	tools    int
	question bool
}

// RenderTimelineCards partitions ordered output by provider byte/element limits
// and a fixed per-card tool cap while keeping tool panels independently folded.
func RenderTimelineCards(state channelcontract.CardState, options CardOptions) ([]channelcontract.RenderedCardPage, error) {
	if !state.Status.Valid() {
		return nil, errors.New("feishu timeline card: invalid status")
	}
	entries := normalizeTimelineEntries(state, options.ToolDetailsMode)
	if notice := timelineTerminalNotice(state.Status); notice != "" {
		entries = append(entries, timelineRenderEntry{ID: "timeline_terminal_notice", Kind: channelcontract.TimelineNotice, Text: notice})
	}
	pages, err := planTimelinePages(state, entries, options)
	if err != nil {
		return nil, err
	}
	if len(pages) == 0 {
		pages = []timelinePage{{entries: []timelineRenderEntry{{ID: "timeline_empty", Kind: channelcontract.TimelineText, Text: firstNonEmptyTimeline(state.Text, " ")}}}}
	}
	pages = ensureTimelinePageCount(pages, state.MinimumPageCount)
	out := make([]channelcontract.RenderedCardPage, 0, len(pages))
	for index, page := range pages {
		card, renderErr := renderTimelinePage(state, page, index, len(pages), index == len(pages)-1, options, false)
		if renderErr != nil {
			return nil, renderErr
		}
		if len(card) > maxCardBytes || timelineCardElementCount(card) > maxCardElements {
			return nil, fmt.Errorf("feishu timeline card: page %d exceeds provider limit", index)
		}
		out = append(out, channelcontract.RenderedCardPage{Index: index, Card: card})
	}
	return out, nil
}

func ensureTimelinePageCount(pages []timelinePage, minimum int) []timelinePage {
	if minimum <= len(pages) {
		return pages
	}
	question := false
	for index := range pages {
		question = question || pages[index].question
		pages[index].question = false
	}
	for len(pages) < minimum {
		index := len(pages)
		pages = append(pages, timelinePage{entries: []timelineRenderEntry{{
			ID:   fmt.Sprintf("timeline_reconciled_%d", index),
			Kind: channelcontract.TimelineNotice,
			Text: "此段内容已合并到前面的卡片。",
		}}})
	}
	if question {
		pages[len(pages)-1].question = true
	}
	return pages
}

func normalizeTimelineEntries(state channelcontract.CardState, mode channelcontract.ToolDetailsMode) []timelineRenderEntry {
	source := state.Timeline
	if len(source) == 0 {
		if state.Text != "" {
			source = append(source, channelcontract.TimelineEntry{Kind: channelcontract.TimelineText, Text: state.Text})
		}
		for _, tool := range state.Tools {
			source = append(source, channelcontract.TimelineEntry{ID: "tool:" + tool.ID, Kind: channelcontract.TimelineTool, Tool: tool})
		}
	}
	entries := make([]timelineRenderEntry, 0, len(source))
	toolOrdinal := 0
	for sourceIndex, entry := range source {
		switch entry.Kind {
		case channelcontract.TimelineText, channelcontract.TimelineNotice:
			if entry.Text != "" {
				entries = append(entries, timelineRenderEntry{ID: fmt.Sprintf("timeline_%s_%d", entry.Kind, sourceIndex), Kind: entry.Kind, Text: entry.Text})
			}
		case channelcontract.TimelineTool:
			toolOrdinal++
			if mode.Normalize() != channelcontract.ToolDetailsOff {
				entries = append(entries, timelineRenderEntry{ID: fmt.Sprintf("tool_%d", toolOrdinal-1), Kind: entry.Kind, Tool: entry.Tool, ToolOrdinal: toolOrdinal})
			}
		}
	}
	return entries
}

func planTimelinePages(state channelcontract.CardState, entries []timelineRenderEntry, options CardOptions) ([]timelinePage, error) {
	pages := []timelinePage{{}}
	for _, entry := range entries {
		switch entry.Kind {
		case channelcontract.TimelineText, channelcontract.TimelineNotice:
			var err error
			pages, err = appendTimelineText(state, pages, entry, options)
			if err != nil {
				return nil, err
			}
		case channelcontract.TimelineTool:
			current := len(pages) - 1
			candidate := cloneTimelinePage(pages[current])
			candidate.entries = append(candidate.entries, entry)
			candidate.tools++
			if pages[current].tools >= maxToolsPerTimelineCard || !timelinePageFits(state, candidate, current, options) {
				pages = append(pages, timelinePage{entries: []timelineRenderEntry{entry}, tools: 1})
				current++
			} else {
				pages[current] = candidate
			}
			if !timelinePageFits(state, pages[current], current, options) {
				return nil, fmt.Errorf("feishu timeline card: tool %d exceeds page budget", entry.ToolOrdinal)
			}
		}
	}
	if state.Question != nil {
		last := len(pages) - 1
		candidate := cloneTimelinePage(pages[last])
		candidate.question = true
		if !timelinePageFits(state, candidate, last, options) && len(pages[last].entries) > 0 {
			pages = append(pages, timelinePage{question: true})
		} else {
			pages[last] = candidate
		}
	}
	if len(pages) == 1 && len(pages[0].entries) == 0 && !pages[0].question {
		return nil, nil
	}
	return pages, nil
}

func appendTimelineText(state channelcontract.CardState, pages []timelinePage, entry timelineRenderEntry, options CardOptions) ([]timelinePage, error) {
	remaining := entry.Text
	chunkIndex := 0
	for remaining != "" {
		pageIndex := len(pages) - 1
		candidate := cloneTimelinePage(pages[pageIndex])
		candidate.entries = append(candidate.entries, timelineRenderEntry{ID: fmt.Sprintf("%s_%d", entry.ID, chunkIndex), Kind: entry.Kind, Text: remaining})
		if timelinePageFits(state, candidate, pageIndex, options) {
			pages[pageIndex] = candidate
			break
		}
		head, tail := largestTimelineTextChunk(state, pages[pageIndex], entry, remaining, chunkIndex, pageIndex, options)
		if head == "" {
			if len(pages[pageIndex].entries) == 0 {
				return nil, errors.New("feishu timeline card: text cannot fit on an empty page")
			}
			pages = append(pages, timelinePage{})
			continue
		}
		pages[pageIndex].entries = append(pages[pageIndex].entries, timelineRenderEntry{ID: fmt.Sprintf("%s_%d", entry.ID, chunkIndex), Kind: entry.Kind, Text: head})
		remaining = tail
		chunkIndex++
		if remaining != "" {
			pages = append(pages, timelinePage{})
		}
	}
	return pages, nil
}

func largestTimelineTextChunk(state channelcontract.CardState, page timelinePage, entry timelineRenderEntry, text string, chunkIndex, pageIndex int, options CardOptions) (string, string) {
	runes := []rune(text)
	low, high, best := 1, len(runes), 0
	for low <= high {
		middle := low + (high-low)/2
		head, _ := splitTimelineMarkdown(text, middle)
		candidate := cloneTimelinePage(page)
		candidate.entries = append(candidate.entries, timelineRenderEntry{ID: fmt.Sprintf("%s_%d", entry.ID, chunkIndex), Kind: entry.Kind, Text: head})
		if timelinePageFits(state, candidate, pageIndex, options) {
			best = middle
			low = middle + 1
		} else {
			high = middle - 1
		}
	}
	if best == 0 {
		return "", text
	}
	best = preferredTimelineSplit(runes, best)
	head, tail := splitTimelineMarkdown(text, best)
	return head, tail
}

func preferredTimelineSplit(runes []rune, limit int) int {
	if limit >= len(runes) {
		return len(runes)
	}
	prefix := string(runes[:limit])
	if index := strings.LastIndex(prefix, "\n\n"); index >= 0 {
		cut := utf8.RuneCountInString(prefix[:index+2])
		if cut >= limit/2 {
			return cut
		}
	}
	if index := strings.LastIndex(prefix, "\n"); index >= 0 {
		cut := utf8.RuneCountInString(prefix[:index+1])
		if cut >= limit/2 {
			return cut
		}
	}
	return limit
}

func splitTimelineMarkdown(text string, runeIndex int) (string, string) {
	runes := []rune(text)
	if runeIndex >= len(runes) {
		return text, ""
	}
	head, tail := string(runes[:runeIndex]), string(runes[runeIndex:])
	if strings.Count(head, "```")%2 != 0 {
		head += "\n```"
		tail = "```\n" + tail
	}
	return head, tail
}

func timelinePageFits(state channelcontract.CardState, page timelinePage, pageIndex int, options CardOptions) bool {
	card, err := renderTimelinePage(state, page, pageIndex, pageIndex+1, true, options, true)
	return err == nil && len(card) <= maxCardBytes-timelineCardReserveBytes && timelineCardElementCount(card) <= maxCardElements
}

func timelineCardElementCount(card json.RawMessage) int {
	var decoded struct {
		Body struct {
			Elements []json.RawMessage `json:"elements"`
		} `json:"body"`
	}
	if json.Unmarshal(card, &decoded) != nil {
		return maxCardElements + 1
	}
	return len(decoded.Body.Elements)
}

func renderTimelinePage(state channelcontract.CardState, page timelinePage, pageIndex, pageCount int, latest bool, options CardOptions, planning bool) (json.RawMessage, error) {
	elements := make([]interface{}, 0, len(page.entries)+4)
	for _, entry := range page.entries {
		switch entry.Kind {
		case channelcontract.TimelineText:
			elements = append(elements, map[string]interface{}{"tag": cardTagMarkdown, "element_id": entry.ID, "content": entry.Text})
		case channelcontract.TimelineNotice:
			elements = append(elements, map[string]interface{}{"tag": cardTagMarkdown, "element_id": entry.ID, "content": "> " + entry.Text})
		case channelcontract.TimelineTool:
			tool := entry.Tool
			if planning {
				tool.Command = strings.Repeat("界", channelcontract.ToolCommandPreviewLimit)
				tool.OutputPreview = strings.Repeat("界", channelcontract.ToolOutputPreviewLimit)
				tool.OutputTruncated = true
			}
			elements = append(elements, timelineToolPanel(tool, entry.ToolOrdinal))
		}
	}
	if page.question && state.Question != nil {
		elements = append(elements, timelineQuestionElements(*state.Question)...)
	}
	if latest && options.CallbackToken != "" {
		elements = append(elements, timelineControlElement(state.Status, options.CallbackToken))
	}
	card := map[string]interface{}{
		"schema": cardSchema,
		"config": map[string]interface{}{"update_multi": true, "streaming_mode": state.Status == channelcontract.CardRunning},
		"header": map[string]interface{}{
			"template": statusTemplate(state.Status),
			"title":    map[string]interface{}{"tag": cardTagPlainText, "content": timelinePageTitle(state.Status, pageIndex, pageCount)},
		},
		"body": map[string]interface{}{"elements": elements},
	}
	return marshalTimelineCard(card)
}

func timelineToolPanel(tool channelcontract.ToolProgress, ordinal int) map[string]interface{} {
	return map[string]interface{}{
		"tag":        cardTagCollapsiblePanel,
		"element_id": fmt.Sprintf("tool_details_panel_%d", ordinal-1),
		"expanded":   false,
		"header": map[string]interface{}{
			"title":               map[string]interface{}{"tag": cardTagPlainText, "content": fmt.Sprintf("%d. %s · %s", ordinal, compactToolName(tool.Name), timelineToolStatus(tool))},
			"icon":                map[string]interface{}{"tag": cardTagStandardIcon, "token": toolDetailsDisclosureIcon, "color": "grey", "size": "16px 16px"},
			"icon_position":       "right",
			"icon_expanded_angle": -180,
		},
		"elements": []interface{}{map[string]interface{}{
			"tag":        cardTagMarkdown,
			"element_id": fmt.Sprintf("tool_details_content_%d", ordinal-1),
			"content":    renderTimelineToolContent(tool),
		}},
	}
}

func renderTimelineToolContent(tool channelcontract.ToolProgress) string {
	var builder strings.Builder
	if command := truncateMarkdown(escapeMarkdownInline(tool.Command), channelcontract.ToolCommandPreviewLimit); command != "" {
		builder.WriteString("命令：`")
		builder.WriteString(command)
		builder.WriteString("`\n")
	}
	if output := truncateMarkdown(escapeMarkdownOutput(tool.OutputPreview), channelcontract.ToolOutputPreviewLimit); output != "" {
		label := "输出"
		if tool.IsError || tool.Status == channelcontract.ToolStatusFailed {
			label = "错误"
		}
		builder.WriteString(label)
		builder.WriteString("：\n\n```text\n")
		builder.WriteString(output)
		builder.WriteString("\n```\n")
	}
	if tool.OutputTruncated {
		builder.WriteString("输出已截断\n")
	}
	if builder.Len() == 0 {
		return "暂无可展示详情。"
	}
	return strings.TrimSpace(builder.String())
}

func timelineToolStatus(tool channelcontract.ToolProgress) string {
	switch tool.Status {
	case channelcontract.ToolStatusRunning:
		return "运行中"
	case channelcontract.ToolStatusComplete:
		return "已完成"
	case channelcontract.ToolStatusFailed:
		return "失败"
	default:
		return firstNonEmptyTimeline(strings.TrimSpace(tool.Status), "未知")
	}
}

func timelinePageTitle(status channelcontract.CardStatus, pageIndex, pageCount int) string {
	if status.Terminal() {
		return fmt.Sprintf("%s · %d/%d", statusTitle(status), pageIndex+1, pageCount)
	}
	return fmt.Sprintf("%s · 第 %d 段", statusTitle(status), pageIndex+1)
}

func timelineTerminalNotice(status channelcontract.CardStatus) string {
	switch status {
	case channelcontract.CardFailed:
		return "执行失败，已保留此前产生的内容。"
	case channelcontract.CardCancelled:
		return "任务已停止。"
	case channelcontract.CardInterrupted:
		return "任务已中断。"
	default:
		return ""
	}
}

func timelineQuestionElements(question channelcontract.InteractionQuestion) []interface{} {
	elements := []interface{}{map[string]interface{}{"tag": cardTagMarkdown, "element_id": "question", "content": question.Question}}
	if len(question.Choices) == 0 {
		return append(elements, map[string]interface{}{"tag": cardTagMarkdown, "element_id": "question_hint", "content": "请直接发送文字回答。"})
	}
	elements = append(elements, map[string]interface{}{"tag": cardTagMarkdown, "element_id": "question_hint", "content": "请选择一个选项，也可以直接发送文字回答。"})
	for index, choice := range question.Choices {
		elements = append(elements, map[string]interface{}{
			"tag": cardTagButton, "element_id": fmt.Sprintf("question_choice_%d", index),
			"text": map[string]interface{}{"tag": cardTagPlainText, "content": choice}, "type": "default",
			"behaviors": []interface{}{map[string]interface{}{"type": cardActionCallback, "value": map[string]string{
				"token": question.Token, "action": channelcontract.InteractionActionAnswer, "choice_id": channelcontract.InteractionChoiceID(index),
			}}},
		})
	}
	return elements
}

func timelineControlElement(status channelcontract.CardStatus, token string) map[string]interface{} {
	text, action := "停止", "stop"
	if status.Terminal() {
		text, action = "重试", "retry"
	}
	return map[string]interface{}{
		"tag": cardTagButton, "element_id": "controls", "text": map[string]interface{}{"tag": cardTagPlainText, "content": text}, "type": "default",
		"behaviors": []interface{}{map[string]interface{}{"type": cardActionCallback, "value": map[string]string{"token": token, "action": action}}},
	}
}

func cloneTimelinePage(page timelinePage) timelinePage {
	page.entries = append([]timelineRenderEntry(nil), page.entries...)
	return page
}

func marshalTimelineCard(card map[string]interface{}) (json.RawMessage, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(card); err != nil {
		return nil, fmt.Errorf("feishu timeline card: marshal: %w", err)
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}

func firstNonEmptyTimeline(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
