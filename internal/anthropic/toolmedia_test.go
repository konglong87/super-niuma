package anthropic

import (
	"strings"
	"testing"
)

// MCP 的图片块走 Result.ContextMessages 送到这里（TODO-061）。带 base64 的必须变成
// data URL；拿不出 URL 的以前整块消失，模型既看不到图也不知道有图。
func TestOpenAIPathCarriesImageBlocks(t *testing.T) {
	out := openAIChatCompletionMessages(MessageParam{Role: "user", Content: []ContentBlock{
		{Type: "text", Text: "screenshot follows"},
		{Type: "image", Text: "[image content: image/png, 4 base64 chars]", Source: &ContentSource{Type: "base64", MediaType: "image/png", Data: "AAAA"}},
	}})
	if len(out) != 2 {
		t.Fatalf("messages = %+v, want text + image", out)
	}
	if len(out[1].MultiContent) != 1 || out[1].MultiContent[0].ImageURL == nil {
		t.Fatalf("image block did not become a MultiContent image: %+v", out[1])
	}
	if got := out[1].MultiContent[0].ImageURL.URL; got != "data:image/png;base64,AAAA" {
		t.Fatalf("image url = %q", got)
	}
}

func TestOpenAIPathFallsBackToTextWhenAnImageHasNoSource(t *testing.T) {
	out := openAIChatCompletionMessages(MessageParam{Role: "user", Content: []ContentBlock{
		{Type: "image", Text: "[image content omitted: image/png, 0 base64 chars; no data]"},
	}})
	if len(out) != 1 {
		t.Fatalf("sourceless image block vanished: %+v", out)
	}
	if !strings.Contains(out[0].Content, "image content omitted") {
		t.Fatalf("fallback text lost: %+v", out[0])
	}
}
