package mcp

import (
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/tools"
)

// TODO-061 的正题：一个只返回图片的 MCP 工具，模型必须真的看得见图片，
// 而不是只看到一行"有个图但拿不到"。
func TestToolAdapterDeliversImageContentToTheModel(t *testing.T) {
	shortTimeouts(t, testBudget())
	client := startStub(t, "imager", "image-tool")

	adapter := ToolAdapter{ServerName: "imager", ToolInfo: ToolInfo{Name: "screenshot"}, Client: client}
	result := adapter.Run(t.Context(), nil, tools.Context{})
	if result.IsError {
		t.Fatalf("tool errored: %s", result.Content)
	}
	if !strings.Contains(result.Content, "here it is") {
		t.Fatalf("text content was lost: %q", result.Content)
	}
	if len(result.ContextMessages) != 1 {
		t.Fatalf("ContextMessages = %d, want 1 message carrying the image", len(result.ContextMessages))
	}
	blocks := result.ContextMessages[0].Content
	if result.ContextMessages[0].Role != "user" {
		t.Fatalf("role = %q, want user", result.ContextMessages[0].Role)
	}
	if len(blocks) != 2 || blocks[0].Type != "text" || blocks[1].Type != "image" {
		t.Fatalf("blocks = %+v, want [text image]", blocks)
	}
	image := blocks[1]
	if image.Source == nil || image.Source.Type != "base64" || image.Source.MediaType != "image/png" || image.Source.Data != "AAAA" {
		t.Fatalf("image source = %+v, want base64 image/png AAAA", image.Source)
	}
	// 文本那半边不能再说"omitted" —— 图现在真的送到了。
	if strings.Contains(result.Content, "omitted") {
		t.Fatalf("text still claims the image was omitted: %q", result.Content)
	}
	if !strings.Contains(result.Content, "attached below") {
		t.Fatalf("text does not tell the model the image is attached: %q", result.Content)
	}
}

func TestUndeliverableImageTypeStaysAPlaceholder(t *testing.T) {
	out := renderToolCallContent([]toolContentBlock{
		{Type: "image", MimeType: "image/svg+xml", Data: "PHN2Zy8+"},
	})
	if len(out.Media) != 0 {
		t.Fatalf("svg was inlined as an image block: %+v", out.Media)
	}
	if !strings.Contains(out.Text, "image/svg+xml") || !strings.Contains(out.Text, "cannot be sent to the model") {
		t.Fatalf("placeholder does not explain why: %q", out.Text)
	}
}

func TestAudioContentStaysAPlaceholder(t *testing.T) {
	out := renderToolCallContent([]toolContentBlock{
		{Type: "audio", MimeType: "audio/wav", Data: "UklGRg=="},
	})
	if len(out.Media) != 0 {
		t.Fatalf("audio was inlined: %+v", out.Media)
	}
	if !strings.Contains(out.Text, "audio/wav") {
		t.Fatalf("audio placeholder lost the mime type: %q", out.Text)
	}
}

// 图片走 ContextMessages，绕过了 toolresult.Process 的字符截断预算，所以上限必须
// 在这一层自己守住 —— 否则一张 50MB 的截图能顶爆整个请求。
func TestOversizedImageIsNotInlined(t *testing.T) {
	out := renderToolCallContent([]toolContentBlock{
		{Type: "image", MimeType: "image/png", Data: strings.Repeat("A", maxInlineImageBase64Chars+1)},
	})
	if len(out.Media) != 0 {
		t.Fatalf("oversized image was inlined: %d blocks", len(out.Media))
	}
	if !strings.Contains(out.Text, "too large to inline") {
		t.Fatalf("placeholder does not explain the size limit: %q", out.Text[:min(len(out.Text), 200)])
	}
}

func TestOnlyTheFirstFewImagesAreInlined(t *testing.T) {
	blocks := make([]toolContentBlock, maxInlineImagesPerResult+2)
	for i := range blocks {
		blocks[i] = toolContentBlock{Type: "image", MimeType: "image/png", Data: "AAAA"}
	}
	out := renderToolCallContent(blocks)
	if len(out.Media) != maxInlineImagesPerResult {
		t.Fatalf("inlined %d images, want %d", len(out.Media), maxInlineImagesPerResult)
	}
	// 超额的必须留痕，不能静默丢掉。
	if got := strings.Count(out.Text, "only the first"); got != 2 {
		t.Fatalf("overflow notes = %d, want 2; text=%q", got, out.Text)
	}
}

func TestImageReturnedAsAnEmbeddedResourceIsInlined(t *testing.T) {
	out := renderToolCallContent([]toolContentBlock{
		{Type: "resource", Resource: &ReadResourceContent{URI: "file://shot.png", MimeType: "image/png", Blob: "AAAA"}},
	})
	if len(out.Media) != 1 || out.Media[0].Source == nil || out.Media[0].Source.Data != "AAAA" {
		t.Fatalf("embedded resource image was not inlined: %+v", out.Media)
	}
	if !strings.Contains(out.Text, "file://shot.png") {
		t.Fatalf("text lost the resource uri: %q", out.Text)
	}
}

// 每个内联的图片块都带一行 Text 降级说明：不支持图片的 provider 路径在 Source
// 用不上时会回落到 Text，而不是把整块丢掉。
func TestInlinedImageBlockCarriesAPlaceholderText(t *testing.T) {
	out := renderToolCallContent([]toolContentBlock{
		{Type: "image", MimeType: "image/png", Data: "AAAA"},
	})
	if len(out.Media) != 1 {
		t.Fatalf("media = %+v", out.Media)
	}
	if out.Media[0].Text == "" {
		t.Fatal("inlined image block has no text fallback")
	}
}

func TestNoMediaMeansNoContextMessage(t *testing.T) {
	if msgs := mediaContextMessage("mcp__x__y", nil); msgs != nil {
		t.Fatalf("mediaContextMessage returned %+v for zero images", msgs)
	}
}

// 错误结果不带图：错误路径上的 content 大概率残缺，且 ContextMessages 在
// query / agentruntime 两侧本来就只在非错误时回灌。
func TestErrorResultCarriesNoImages(t *testing.T) {
	shortTimeouts(t, testBudget())
	client := startStub(t, "imager", "image-error-tool")

	adapter := ToolAdapter{ServerName: "imager", ToolInfo: ToolInfo{Name: "screenshot"}, Client: client}
	result := adapter.Run(t.Context(), nil, tools.Context{})
	if !result.IsError {
		t.Fatalf("expected an error result, got %+v", result)
	}
	if len(result.ContextMessages) != 0 {
		t.Fatalf("error result carried %d context messages", len(result.ContextMessages))
	}
}
