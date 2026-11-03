package mcp

import (
	"fmt"
	"strings"

	"github.com/konglong87/super-niuma/internal/anthropic"
)

// deliverableImageMediaTypes 是能真正送进模型的图片类型。
//
// 别的（image/svg+xml、image/bmp、image/tiff…）Anthropic 的 Messages API 不收，
// 硬塞换来的是一个 400 —— 那比留一行占位说明更糟，因为整轮请求都废了。
var deliverableImageMediaTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
}

const (
	// 单张图片的 base64 字符上限。Anthropic 的单图上限是 5MB 原始字节，base64 编码
	// 是 4/3 倍，所以按 base64 长度换算过来卡这里。
	maxInlineImageBase64Chars = 5 * 1024 * 1024 * 4 / 3
	// 一次工具调用最多内联几张图，以及这些图的 base64 总长上限。
	//
	// 这两个上限不是形式主义：图片走 Result.ContextMessages，绕过了
	// toolresult.Process 的字符截断预算（那一层只看 tool_result 块里的字符串），
	// 所以截断保护必须在这里自己做。超出的图片退回占位行，不会被静默丢掉。
	maxInlineImagesPerResult      = 4
	maxInlineImageBase64PerResult = 10 * 1024 * 1024
)

// toolCallContent 是一次 tools/call 的结果摊平之后的两半：给模型读的文本，
// 以及能真正当作内容块送过去的图片。
type toolCallContent struct {
	Text  string
	Media []anthropic.ContentBlock
}

// renderToolCallContent 把 MCP 的各类 content 块拆成文本 + 可投递的图片块。
//
// 上一轮（AUDIT-P1-18）只做到「非文本块留一行占位」：模型知道有东西，但拿不到，
// 截图类工具因此从"像是没返回"变成"说得清但看不见"。这里补上剩下那一半 ——
// 支持的图片类型升级成真正的 image 内容块（TODO-061）。
//
// 不能投递的（音频、Messages API 不收的图片类型、超限的）仍旧只留占位行，
// 且占位行会写明为什么，因为「说不清为什么看不到」本身就是上一轮要修的毛病。
func renderToolCallContent(blocks []toolContentBlock) toolCallContent {
	var out toolCallContent
	var parts []string
	usedChars := 0
	for _, block := range blocks {
		switch {
		case block.Type == "text":
			parts = append(parts, block.Text)
		case block.Resource != nil && block.Resource.Text != "":
			parts = append(parts, block.Resource.Text)
		case block.Type == "image", block.Type == "audio":
			note, media := inlineImage(block.Type, block.MimeType, block.Data, len(out.Media), usedChars)
			parts = append(parts, note)
			if media != nil {
				out.Media = append(out.Media, *media)
				usedChars += len(block.Data)
			}
		case block.Resource != nil && block.Resource.Blob != "":
			// 有些 server 把截图当成内嵌资源返回，而不是 type:"image"。
			note, media := inlineImage("resource "+block.Resource.URI, block.Resource.MimeType, block.Resource.Blob, len(out.Media), usedChars)
			parts = append(parts, note)
			if media != nil {
				out.Media = append(out.Media, *media)
				usedChars += len(block.Resource.Blob)
			}
		case block.Resource != nil:
			parts = append(parts, fmt.Sprintf("[resource %s omitted: %s]",
				block.Resource.URI, orUnknown(block.Resource.MimeType)))
		default:
			parts = append(parts, fmt.Sprintf("[%s content omitted]", orUnknown(block.Type)))
		}
	}
	out.Text = strings.Join(parts, "\n")
	return out
}

// inlineImage 决定一个二进制块能不能升级成 image 内容块，并返回配套的说明行。
// media 为 nil 表示只留占位行。
func inlineImage(label, mimeType, data string, alreadyInlined, usedChars int) (string, *anthropic.ContentBlock) {
	mediaType := strings.ToLower(strings.TrimSpace(mimeType))
	reason := ""
	switch {
	case data == "":
		reason = "no data"
	case !deliverableImageMediaTypes[mediaType]:
		reason = orUnknown(mimeType) + " cannot be sent to the model as an image"
	case len(data) > maxInlineImageBase64Chars:
		reason = fmt.Sprintf("too large to inline (limit %d base64 chars)", maxInlineImageBase64Chars)
	case alreadyInlined >= maxInlineImagesPerResult:
		reason = fmt.Sprintf("only the first %d images of a result are inlined", maxInlineImagesPerResult)
	case usedChars+len(data) > maxInlineImageBase64PerResult:
		reason = fmt.Sprintf("result image budget exhausted (limit %d base64 chars)", maxInlineImageBase64PerResult)
	}
	if reason != "" {
		return fmt.Sprintf("[%s content omitted: %s, %d base64 chars; %s]",
			label, orUnknown(mimeType), len(data), reason), nil
	}
	return fmt.Sprintf("[%s content attached below: %s, %d base64 chars]", label, mediaType, len(data)),
		&anthropic.ContentBlock{
			Type: "image",
			// Text 是给不支持图片的 provider 路径留的降级说明 —— 那些路径在
			// Source 用不上时会回落到 Text，而不是把整块丢掉。
			Text:   fmt.Sprintf("[%s content: %s, %d base64 chars]", label, mediaType, len(data)),
			Source: &anthropic.ContentSource{Type: "base64", MediaType: mediaType, Data: data},
		}
}

// mediaContextMessage 把可投递的图片包成一条紧跟 tool_result 的 user 消息。
//
// 为什么不塞进 tool_result 块里：Anthropic 的 tool_result content 在本仓的
// ContentBlock 建模里是个 string（SDK 的 NewToolResultBlock 也只收 string），
// 而 Result.ContextMessages 这条通路已经存在且已经接到 query / agentruntime 两侧
// 的消息序列上，不需要动 7910 行的 query.go。
func mediaContextMessage(toolName string, media []anthropic.ContentBlock) []anthropic.MessageParam {
	if len(media) == 0 {
		return nil
	}
	blocks := make([]anthropic.ContentBlock, 0, len(media)+1)
	blocks = append(blocks, anthropic.ContentBlock{
		Type: "text",
		Text: fmt.Sprintf("Image content returned by %s (%d attachment(s)), in the order listed above:", toolName, len(media)),
	})
	blocks = append(blocks, media...)
	return []anthropic.MessageParam{{Role: "user", Content: blocks}}
}
