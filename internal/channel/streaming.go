package channel

import (
	"strings"
	"sync"
	"time"
)

// StreamBuffer aggregates model deltas into complete card render states. It
// owns only volatile preview state; the final state is persisted by runtime.
type StreamBuffer struct {
	mu            sync.Mutex
	runID         string
	now           func() time.Time
	minInterval   time.Duration
	minChars      int
	text          string
	tools         map[string]ToolProgress
	toolOrder     []string
	timeline      []TimelineEntry
	toolEntries   map[string]int
	lastText      string
	lastTools     []ToolProgress
	lastTimeline  []TimelineEntry
	lastFlushAt   time.Time
	renderVersion uint64
	terminal      bool
	urgent        bool
}

func NewStreamBuffer(runID string, start time.Time, minInterval time.Duration, minChars int) *StreamBuffer {
	if minChars < 0 {
		minChars = 0
	}
	return &StreamBuffer{runID: runID, now: func() time.Time { return start }, minInterval: minInterval, minChars: minChars, tools: map[string]ToolProgress{}, toolEntries: map[string]int{}, lastFlushAt: start}
}

func (b *StreamBuffer) Apply(delta Delta) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.terminal || !delta.Kind.Valid() {
		return false
	}
	switch delta.Kind {
	case DeltaText:
		if delta.Text == "" {
			return false
		}
		b.text += delta.Text
		b.appendText(delta.Text)
	case DeltaTextAmend:
		changed := b.amendText(delta.PreviousText, delta.Text)
		b.urgent = b.urgent || changed
		return changed
	case DeltaTool:
		if delta.ToolName == "" {
			return false
		}
		id := delta.ToolID
		if id == "" {
			id = delta.ToolName
		}
		previous, exists := b.tools[id]
		progress := ToolProgress{ID: id, Name: delta.ToolName, Status: delta.ToolStatus, Command: delta.ToolCommand, OutputPreview: delta.ToolOutput, IsError: delta.ToolIsError, OutputTruncated: delta.ToolTruncated}
		if exists {
			progress = mergeToolProgress(previous, progress)
		} else {
			b.toolOrder = append(b.toolOrder, id)
			b.toolEntries[id] = len(b.timeline)
			b.timeline = append(b.timeline, TimelineEntry{ID: "tool:" + id, Kind: TimelineTool, Tool: progress})
		}
		if exists && previous == progress {
			return false
		}
		b.tools[id] = progress
		if entryIndex, ok := b.toolEntries[id]; ok {
			b.timeline[entryIndex].Tool = progress
		}
		b.urgent = true
	case DeltaStatus:
		if delta.Text == "" {
			return false
		}
		b.timeline = append(b.timeline, TimelineEntry{Kind: TimelineNotice, Text: delta.Text})
		b.urgent = true
	}
	return true
}

func (b *StreamBuffer) Flush(now time.Time, terminal bool) (CardState, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	currentTools := b.toolProgress()
	currentTimeline := cloneTimeline(b.timeline)
	if b.renderVersion > 0 && b.text == b.lastText && toolsEqual(currentTools, b.lastTools) && timelineEqual(currentTimeline, b.lastTimeline) {
		return CardState{}, false
	}
	if b.renderVersion > 0 && !terminal && !b.urgent && now.Sub(b.lastFlushAt) < b.minInterval && len([]rune(b.text))-len([]rune(b.lastText)) < b.minChars {
		return CardState{}, false
	}
	b.renderVersion++
	b.lastText = b.text
	b.lastTools = cloneTools(currentTools)
	b.lastTimeline = currentTimeline
	b.lastFlushAt = now
	b.urgent = false
	if terminal {
		b.terminal = true
	}
	status := CardRunning
	if terminal {
		status = CardCompleted
	}
	return CardState{RunID: b.runID, Status: status, Text: b.text, Tools: currentTools, Timeline: currentTimeline, RenderVersion: b.renderVersion}, true
}

// FlushTerminal forces the terminal transition even when the final text and
// tool records are unchanged from the last streaming update.
func (b *StreamBuffer) FlushTerminal(now time.Time, text string, status CardStatus) CardState {
	b.mu.Lock()
	defer b.mu.Unlock()
	if text != "" {
		b.reconcileFinalText(text)
	}
	b.renderVersion++
	b.lastText = b.text
	b.lastTools = cloneTools(b.toolProgress())
	b.lastTimeline = cloneTimeline(b.timeline)
	b.lastFlushAt = now
	b.terminal = true
	b.urgent = false
	return CardState{RunID: b.runID, Status: status, Text: b.text, Tools: b.toolProgress(), Timeline: cloneTimeline(b.timeline), RenderVersion: b.renderVersion}
}

func (b *StreamBuffer) FlushInteraction(now time.Time, question *InteractionQuestion) CardState {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.renderVersion++
	b.lastText = b.text
	b.lastTools = cloneTools(b.toolProgress())
	b.lastTimeline = cloneTimeline(b.timeline)
	b.lastFlushAt = now
	b.urgent = false
	return CardState{RunID: b.runID, Status: CardWaitingInput, Text: b.text, Tools: b.toolProgress(), Timeline: cloneTimeline(b.timeline), Question: question, RenderVersion: b.renderVersion}
}

func (b *StreamBuffer) appendText(text string) {
	if len(b.timeline) > 0 && b.timeline[len(b.timeline)-1].Kind == TimelineText {
		b.timeline[len(b.timeline)-1].Text += text
		return
	}
	b.timeline = append(b.timeline, TimelineEntry{Kind: TimelineText, Text: text})
}

func (b *StreamBuffer) amendText(previous, final string) bool {
	if previous == final || previous == "" || !strings.HasSuffix(b.text, previous) || len(b.timeline) == 0 {
		return false
	}
	last := &b.timeline[len(b.timeline)-1]
	if last.Kind != TimelineText || !strings.HasSuffix(last.Text, previous) {
		return false
	}
	b.text = strings.TrimSuffix(b.text, previous) + final
	last.Text = strings.TrimSuffix(last.Text, previous) + final
	if last.Text == "" {
		b.timeline = b.timeline[:len(b.timeline)-1]
	}
	return true
}

func (b *StreamBuffer) reconcileFinalText(final string) {
	if final == b.text {
		return
	}
	if strings.HasPrefix(final, b.text) {
		b.appendText(strings.TrimPrefix(final, b.text))
		b.text = final
		return
	}
	entries := make([]TimelineEntry, 0, len(b.timeline)+1)
	for _, entry := range b.timeline {
		if entry.Kind != TimelineText {
			entries = append(entries, entry)
		}
	}
	b.timeline = entries
	b.text = final
	if final != "" {
		b.timeline = append(b.timeline, TimelineEntry{Kind: TimelineText, Text: final})
	}
}

func mergeToolProgress(previous, update ToolProgress) ToolProgress {
	if update.ID == "" {
		update.ID = previous.ID
	}
	if update.Name == "" {
		update.Name = previous.Name
	}
	if update.Status == "" {
		update.Status = previous.Status
	}
	if update.Command == "" {
		update.Command = previous.Command
	}
	if update.OutputPreview == "" {
		update.OutputPreview = previous.OutputPreview
	}
	if !update.IsError {
		update.IsError = previous.IsError
	}
	if !update.OutputTruncated {
		update.OutputTruncated = previous.OutputTruncated
	}
	return update
}

func toolsEqual(left, right []ToolProgress) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func cloneTools(value []ToolProgress) []ToolProgress {
	return append([]ToolProgress(nil), value...)
}

func timelineEqual(left, right []TimelineEntry) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func cloneTimeline(value []TimelineEntry) []TimelineEntry {
	return append([]TimelineEntry(nil), value...)
}

func (b *StreamBuffer) toolProgress() []ToolProgress {
	out := make([]ToolProgress, 0, len(b.toolOrder))
	for _, id := range b.toolOrder {
		if tool, ok := b.tools[id]; ok {
			out = append(out, tool)
		}
	}
	return out
}
