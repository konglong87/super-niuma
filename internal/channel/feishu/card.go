package feishu

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	channelcontract "github.com/konglong87/super-niuma/internal/channel"
)

const (
	cardSchema                  = "2.0"
	cardTagMarkdown             = "markdown"
	cardTagButton               = "button"
	cardTagCollapsiblePanel     = "collapsible_panel"
	cardTagPlainText            = "plain_text"
	cardTagStandardIcon         = "standard_icon"
	cardActionCallback          = "callback"
	toolDetailsPanelElementID   = "tool_details_panel"
	toolDetailsContentElementID = "tool_details_content"
	toolDetailsDisclosureIcon   = "down-small-ccm_outlined"
)

// CardOptions controls opaque callback values embedded in card action behaviors.
type CardOptions struct {
	CallbackToken   string
	ToolDetailsMode channelcontract.ToolDetailsMode
}

// RenderCard emits a complete Feishu Card JSON 2.0 document.
// The callback token is opaque; no runtime/session details are placed in the card.
func RenderCard(state channelcontract.CardState, options CardOptions) (json.RawMessage, error) {
	if !state.Status.Valid() {
		return nil, errors.New("feishu card: invalid status")
	}
	card := map[string]interface{}{
		"schema": cardSchema,
		"config": map[string]interface{}{
			"update_multi":   true,
			"streaming_mode": state.Status == channelcontract.CardRunning,
		},
		"header": map[string]interface{}{
			"template": statusTemplate(state.Status),
			"title":    map[string]interface{}{"tag": "plain_text", "content": statusTitle(state.Status)},
		},
		"body": map[string]interface{}{
			"elements": cardElements(state, options),
		},
	}
	b, err := json.Marshal(card)
	if err != nil {
		return nil, fmt.Errorf("feishu card: marshal: %w", err)
	}
	// Tool previews are optional diagnostics. Preserve the answer and controls if
	// JSON escaping pushes the otherwise bounded panel over Feishu's byte limit.
	if len(b) > maxCardBytes && len(state.Tools) > 0 && options.ToolDetailsMode.Normalize() != channelcontract.ToolDetailsOff {
		options.ToolDetailsMode = channelcontract.ToolDetailsOff
		card["body"] = map[string]interface{}{"elements": cardElements(state, options)}
		b, err = json.Marshal(card)
		if err != nil {
			return nil, fmt.Errorf("feishu card: marshal downgraded card: %w", err)
		}
	}
	if len(b) > maxCardBytes {
		return nil, errors.New("feishu card: payload exceeds provider limit")
	}
	return b, nil
}

func RenderFinalCard(state channelcontract.CardState, options CardOptions) (json.RawMessage, error) {
	return RenderCard(state, options)
}

func RenderStreamingCard(state channelcontract.CardState, options CardOptions) (json.RawMessage, error) {
	return RenderCard(state, options)
}

// FeishuCardRenderer adapts the provider-neutral CardRenderer contract.
type FeishuCardRenderer struct{ CallbackToken string }

func NewCardRenderer(callbackToken string) FeishuCardRenderer {
	return FeishuCardRenderer{CallbackToken: callbackToken}
}

func (r FeishuCardRenderer) RenderFinal(state channelcontract.CardState) (json.RawMessage, error) {
	return RenderCard(state, CardOptions{CallbackToken: r.CallbackToken})
}

func (r FeishuCardRenderer) RenderStreaming(state channelcontract.CardState) (json.RawMessage, error) {
	return RenderCard(state, CardOptions{CallbackToken: r.CallbackToken})
}

func cardElements(state channelcontract.CardState, options CardOptions) []interface{} {
	content := state.Text
	if state.Question != nil {
		content = state.Question.Question
	}
	elements := []interface{}{map[string]interface{}{"tag": cardTagMarkdown, "element_id": "answer", "content": content}}
	if state.Question != nil {
		if len(state.Question.Choices) > 0 {
			elements = append(elements, map[string]interface{}{"tag": cardTagMarkdown, "element_id": "question_hint", "content": "请选择一个选项，也可以直接发送文字回答。"})
			for index, choice := range state.Question.Choices {
				elements = append(elements, map[string]interface{}{
					"tag":        cardTagButton,
					"element_id": fmt.Sprintf("question_choice_%d", index),
					"text":       map[string]interface{}{"tag": "plain_text", "content": choice},
					"type":       "default",
					"behaviors": []interface{}{map[string]interface{}{"type": cardActionCallback, "value": map[string]string{
						"token":     state.Question.Token,
						"action":    channelcontract.InteractionActionAnswer,
						"choice_id": channelcontract.InteractionChoiceID(index),
					}}},
				})
			}
		} else {
			elements = append(elements, map[string]interface{}{"tag": cardTagMarkdown, "element_id": "question_hint", "content": "请直接发送文字回答。"})
		}
	}
	if len(state.Tools) > 0 {
		toolText := renderToolDetails(state.Tools, options.ToolDetailsMode)
		if toolText != "" {
			elements = append(elements, toolDetailsPanel(state, toolText))
		}
	}
	if options.CallbackToken != "" {
		buttonText, actionName := "停止", "stop"
		if state.Status.Terminal() {
			buttonText, actionName = "重试", "retry"
		}
		elements = append(elements, map[string]interface{}{
			"tag":        cardTagButton,
			"element_id": "controls",
			"text":       map[string]interface{}{"tag": "plain_text", "content": buttonText},
			"type":       "default",
			"behaviors":  []interface{}{map[string]interface{}{"type": cardActionCallback, "value": map[string]string{"token": options.CallbackToken, "action": actionName}}},
		})
	}
	return elements
}

const (
	maxVisibleToolDetails = 6
	maxToolDetailsRunes   = 6000
	maxToolSummaryRunes   = 48
)

func toolDetailsPanel(state channelcontract.CardState, content string) map[string]interface{} {
	return map[string]interface{}{
		"tag":        cardTagCollapsiblePanel,
		"element_id": toolDetailsPanelElementID,
		"expanded":   false,
		"header": map[string]interface{}{
			"title":               map[string]interface{}{"tag": cardTagPlainText, "content": toolDetailsSummary(state)},
			"icon":                map[string]interface{}{"tag": cardTagStandardIcon, "token": toolDetailsDisclosureIcon, "color": "grey", "size": "16px 16px"},
			"icon_position":       "right",
			"icon_expanded_angle": -180,
		},
		"elements": []interface{}{
			map[string]interface{}{"tag": cardTagMarkdown, "element_id": toolDetailsContentElementID, "content": content},
		},
	}
}

func toolDetailsSummary(state channelcontract.CardState) string {
	failed := 0
	current := ""
	for _, tool := range state.Tools {
		if tool.IsError || tool.Status == channelcontract.ToolStatusFailed {
			failed++
		}
		if state.Status == channelcontract.CardRunning && tool.Status == channelcontract.ToolStatusRunning {
			current = compactToolName(tool.Name)
		}
	}
	summary := fmt.Sprintf("%s · %d 个工具 · %d 个失败", statusTitle(state.Status), len(state.Tools), failed)
	if current != "" {
		summary += " · 当前 " + current
	}
	return summary
}

func compactToolName(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if utf8.RuneCountInString(value) <= maxToolSummaryRunes {
		return value
	}
	return string([]rune(value)[:maxToolSummaryRunes-3]) + "..."
}

func renderToolDetails(tools []channelcontract.ToolProgress, mode channelcontract.ToolDetailsMode) string {
	if mode.Normalize() == channelcontract.ToolDetailsOff || len(tools) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("**工具详情**\n")
	for index, tool := range tools {
		if index >= maxVisibleToolDetails {
			builder.WriteString(fmt.Sprintf("- 其余 %d 个工具已折叠\n", len(tools)-index))
			break
		}
		name := escapeMarkdownInline(tool.Name)
		status := escapeMarkdownInline(tool.Status)
		builder.WriteString(fmt.Sprintf("%d. `%s` · %s\n", index+1, name, status))
		if command := escapeMarkdownInline(tool.Command); command != "" {
			builder.WriteString("   命令：`")
			builder.WriteString(command)
			builder.WriteString("`\n")
		}
		if output := escapeMarkdownOutput(tool.OutputPreview); output != "" {
			label := "输出"
			if tool.IsError || tool.Status == channelcontract.ToolStatusFailed {
				label = "错误"
			}
			builder.WriteString("   ")
			builder.WriteString(label)
			builder.WriteString("：\n\n```text\n")
			builder.WriteString(output)
			builder.WriteString("\n```\n")
		}
		if tool.OutputTruncated {
			builder.WriteString("   输出已截断\n")
		}
	}
	return truncateMarkdown(builder.String(), maxToolDetailsRunes)
}

func escapeMarkdownInline(value string) string {
	return strings.ReplaceAll(strings.TrimSpace(value), "`", "'")
}

func escapeMarkdownOutput(value string) string {
	return strings.ReplaceAll(strings.TrimSpace(value), "```", "` ` `")
}

func truncateMarkdown(value string, limit int) string {
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	marker := "\n输出已截断"
	keep := limit - utf8.RuneCountInString(marker)
	if keep <= 0 {
		return string(runes[:limit])
	}
	return string(runes[:keep]) + marker
}

func statusTemplate(status channelcontract.CardStatus) string {
	switch status {
	case channelcontract.CardCompleted:
		return "green"
	case channelcontract.CardFailed:
		return "red"
	case channelcontract.CardCancelled, channelcontract.CardInterrupted:
		return "orange"
	case channelcontract.CardWaitingInput:
		return "blue"
	default:
		return "blue"
	}
}

func statusTitle(status channelcontract.CardStatus) string {
	switch status {
	case channelcontract.CardCompleted:
		return "已完成"
	case channelcontract.CardFailed:
		return "执行失败"
	case channelcontract.CardCancelled:
		return "已取消"
	case channelcontract.CardInterrupted:
		return "已中断"
	case channelcontract.CardWaitingInput:
		return "等待回答"
	default:
		return "处理中"
	}
}
