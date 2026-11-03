package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	channelcontract "github.com/konglong87/super-niuma/internal/channel"
)

type streamSink struct {
	adapter     channelcontract.StreamingAdapter
	renderPages func(channelcontract.CardState) ([]channelcontract.RenderedCardPage, error)
	message     channelcontract.OutboundMessage
	buffer      *channelcontract.StreamBuffer
	pages       []*streamPage
}

type streamPageDelivery struct {
	Index     int
	MessageID string
	Opened    bool
	Degraded  bool
}

type streamPage struct {
	index      int
	controller channelcontract.CardStream
	receipt    channelcontract.CardStreamReceipt
	lastCard   json.RawMessage
	opened     bool
	degraded   bool
}

func newStreamSink(cfg Config, message channelcontract.OutboundMessage) *streamSink {
	interval := cfg.StreamMinInterval
	if interval <= 0 {
		interval = 700 * time.Millisecond
	}
	minChars := cfg.StreamMinChars
	if minChars <= 0 {
		minChars = 80
	}
	adapter, _ := cfg.Adapter.(channelcontract.StreamingAdapter)
	renderPages := cfg.RenderTimelineCards
	if renderPages == nil {
		render := cfg.RenderStreamingCard
		if render == nil {
			render = cfg.RenderFinalCard
		}
		if render == nil {
			render = func(state channelcontract.CardState) ([]byte, error) {
				card := map[string]any{
					"schema": "2.0",
					"config": map[string]any{"streaming_mode": state.Status == channelcontract.CardRunning, "update_multi": true},
					"header": map[string]any{"title": map[string]string{"tag": "plain_text", "content": "niuma"}},
					"body":   map[string]any{"elements": []map[string]string{{"tag": "markdown", "element_id": "answer", "content": state.Text}}},
				}
				return json.Marshal(card)
			}
		}
		renderPages = func(state channelcontract.CardState) ([]channelcontract.RenderedCardPage, error) {
			card, err := render(state)
			if err != nil {
				return nil, err
			}
			return []channelcontract.RenderedCardPage{{Index: 0, Card: card}}, nil
		}
	}
	return &streamSink{adapter: adapter, renderPages: renderPages, message: message, buffer: channelcontract.NewStreamBuffer(message.CorrelationID, cfg.Now(), interval, minChars)}
}

func (s *streamSink) OnDelta(ctx context.Context, delta channelcontract.Delta) error {
	if s == nil || s.adapter == nil || s.renderPages == nil {
		return nil
	}
	if !s.buffer.Apply(delta) {
		return nil
	}
	state, ok := s.buffer.Flush(time.Now(), false)
	if !ok {
		return nil
	}
	state.MinimumPageCount = s.minimumPageCount()
	pages, err := s.renderPages(state)
	if err != nil {
		return nil
	}
	s.syncPages(ctx, pages)
	return nil
}

func (s *streamSink) Finalize(ctx context.Context, finalText string, status channelcontract.CardStatus) []channelcontract.RenderedCardPage {
	if s == nil || s.renderPages == nil {
		return nil
	}
	if finalText == "" {
		finalText = "模型未返回可显示内容，请稍后重试。"
	}
	state := s.buffer.FlushTerminal(time.Now(), finalText, status)
	state.MinimumPageCount = s.minimumPageCount()
	pages, err := s.renderPages(state)
	if err == nil && s.Opened() {
		s.syncPages(ctx, pages)
	} else {
		if err != nil {
			for _, page := range s.pages {
				page.degraded = true
			}
		}
	}
	s.closePages(ctx)
	return pages
}

func (s *streamSink) FinalizeQuestion(ctx context.Context, question channelcontract.InteractionQuestion) []channelcontract.RenderedCardPage {
	if s == nil || s.renderPages == nil {
		return nil
	}
	state := s.buffer.FlushInteraction(time.Now(), &question)
	state.MinimumPageCount = s.minimumPageCount()
	pages, err := s.renderPages(state)
	if err == nil && s.Opened() {
		s.syncPages(ctx, pages)
	} else {
		if err != nil {
			for _, page := range s.pages {
				page.degraded = true
			}
		}
	}
	s.closePages(ctx)
	return pages
}

func (s *streamSink) syncPages(ctx context.Context, rendered []channelcontract.RenderedCardPage) {
	for _, renderedPage := range rendered {
		if renderedPage.Index < 0 {
			continue
		}
		for len(s.pages) <= renderedPage.Index {
			s.pages = append(s.pages, &streamPage{index: len(s.pages)})
		}
		page := s.pages[renderedPage.Index]
		if page.degraded {
			if !page.opened {
				break
			}
			continue
		}
		if bytes.Equal(page.lastCard, renderedPage.Card) {
			continue
		}
		if !page.opened {
			message := s.message
			message.Card = renderedPage.Card
			message.IdempotencyKey = timelinePageKey(message.CorrelationID, renderedPage.Index)
			receipt, controller, err := s.adapter.OpenCardStream(ctx, message)
			if err != nil {
				page.degraded = true
				break
			}
			page.receipt = receipt
			page.controller = controller
			page.lastCard = append(json.RawMessage(nil), renderedPage.Card...)
			page.opened = true
			continue
		}
		if page.controller == nil || page.controller.Update(ctx, renderedPage.Card) != nil {
			page.degraded = true
			continue
		}
		page.lastCard = append(json.RawMessage(nil), renderedPage.Card...)
	}
}

func (s *streamSink) minimumPageCount() int {
	minimum := 0
	for _, page := range s.pages {
		if page.opened && page.index+1 > minimum {
			minimum = page.index + 1
		}
	}
	return minimum
}

func (s *streamSink) closePages(ctx context.Context) {
	for _, page := range s.pages {
		if page.opened && page.controller != nil {
			if err := page.controller.Close(ctx); err != nil {
				page.degraded = true
			}
		}
	}
}

func (s *streamSink) Opened() bool {
	if s == nil {
		return false
	}
	for _, page := range s.pages {
		if page.opened {
			return true
		}
	}
	return false
}

func (s *streamSink) Degraded() bool {
	if s == nil || len(s.pages) == 0 {
		return true
	}
	for _, page := range s.pages {
		if page.degraded || !page.opened {
			return true
		}
	}
	return false
}

func (s *streamSink) MessageID() string {
	if s == nil || len(s.pages) == 0 {
		return ""
	}
	return s.pages[0].receipt.MessageID
}

func (s *streamSink) PageDeliveries() []streamPageDelivery {
	if s == nil {
		return nil
	}
	out := make([]streamPageDelivery, 0, len(s.pages))
	for _, page := range s.pages {
		out = append(out, streamPageDelivery{Index: page.index, MessageID: page.receipt.MessageID, Opened: page.opened, Degraded: page.degraded})
	}
	return out
}

func timelinePageKey(runID string, pageIndex int) string {
	return fmt.Sprintf("run:%s:timeline:page:%d", runID, pageIndex)
}
