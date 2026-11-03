// generator.go 是 streamer 直连路径（TUI 用）。Web 侧走 server 的 QueryRequest
// 通道，但两端共用 BuildPrompt 与 Parse。

package nextsteps

import (
	"context"
	"fmt"
	"strings"

	"github.com/konglong87/super-niuma/internal/anthropic"
)

// Streamer 按仓库惯例在本包内声明，不复用其它包的同名接口。
type Streamer interface {
	StreamMessages(ctx context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error)
}

// Generate 请求下一步候选。返回空切片且 err == nil 表示「这次没有可展示的
// 建议」，调用方静默跳过即可。
func Generate(ctx context.Context, streamer Streamer, in Input, cfg Config) ([]string, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	prompt := BuildPrompt(in, cfg.Count)
	if prompt == "" {
		return nil, nil
	}
	if streamer == nil {
		return nil, fmt.Errorf("nextsteps streamer is nil")
	}
	var streamed strings.Builder
	res, err := streamer.StreamMessages(ctx, anthropic.MessagesRequest{
		Model:     cfg.Model,
		MaxTokens: DefaultMaxTokens,
		Messages: []anthropic.MessageParam{{
			Role:    "user",
			Content: []anthropic.ContentBlock{{Type: "text", Text: prompt}},
		}},
	}, anthropic.StreamCallbacks{
		OnText: func(text string) error {
			streamed.WriteString(text)
			return nil
		},
	})
	if err != nil {
		return nil, err
	}
	raw := streamed.String()
	if strings.TrimSpace(raw) == "" && res != nil {
		var b strings.Builder
		for _, block := range res.Message.Content {
			if block.Type == "text" {
				b.WriteString(block.Text)
			}
		}
		raw = b.String()
	}
	return Parse(raw, cfg.Count), nil
}
