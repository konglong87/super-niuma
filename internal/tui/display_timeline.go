package tui

import "time"

type displaySegmentKind string

const (
	messageRoleConfigNotice = "config_notice"
	configNoticeSeparator   = " · "
	configNoticeDefaultText = "settings updated"

	displaySegmentUser          displaySegmentKind = "user"
	displaySegmentAssistantText displaySegmentKind = "assistant_text"
	displaySegmentThinking      displaySegmentKind = "thinking"
	displaySegmentTool          displaySegmentKind = "tool"
	displaySegmentAgent         displaySegmentKind = "agent"
	displaySegmentMeta          displaySegmentKind = "meta"
	displaySegmentTodo          displaySegmentKind = "todo"
	displaySegmentStatusKind    displaySegmentKind = "status"
	displaySegmentConfigNotice  displaySegmentKind = messageRoleConfigNotice
	displaySegmentError         displaySegmentKind = "error"
	displaySegmentRecap         displaySegmentKind = "recap"
	displaySegmentBackground    displaySegmentKind = "background"
	displaySegmentLoop          displaySegmentKind = "loop"
)

type displaySegmentState string

const (
	displaySegmentStreaming displaySegmentState = "streaming"
	displaySegmentRunning   displaySegmentState = "running"
	displaySegmentDone      displaySegmentState = "done"
	displaySegmentErrorDone displaySegmentState = "error"
)

type displaySegment struct {
	id        string
	seq       int64
	kind      displaySegmentKind
	status    displaySegmentState
	role      string
	turn      int
	phase     int
	content   string
	meta      string
	toolID    string
	tool      toolActivityItem
	agents    []agentProgress
	createdAt time.Time
	updatedAt time.Time
}

type displayTimeline struct {
	segments []displaySegment
	nextSeq  int64
}

func (t displayTimeline) lastUnprintedSegment(minSeq int64) (displaySegment, bool) {
	for i := len(t.segments) - 1; i >= 0; i-- {
		if t.segments[i].seq > minSeq {
			return t.segments[i], true
		}
	}
	return displaySegment{}, false
}

func (t displayTimeline) hasSegments() bool {
	return len(t.segments) > 0
}

func (t displayTimeline) maxSeq() int64 {
	if len(t.segments) == 0 {
		return 0
	}
	return t.segments[len(t.segments)-1].seq
}

func displayKindForRole(role string) displaySegmentKind {
	switch role {
	case "user":
		return displaySegmentUser
	case "assistant":
		return displaySegmentAssistantText
	case "thinking":
		return displaySegmentThinking
	case messageRoleConfigNotice:
		return displaySegmentConfigNotice
	case "error":
		return displaySegmentError
	case "recap":
		return displaySegmentRecap
	case "background":
		return displaySegmentBackground
	case "loop":
		return displaySegmentLoop
	case "agent":
		return displaySegmentAgent
	case "tool":
		return displaySegmentTool
	default:
		return displaySegmentStatusKind
	}
}

func displayRoleForKind(kind displaySegmentKind) string {
	switch kind {
	case displaySegmentUser:
		return "user"
	case displaySegmentAssistantText:
		return "assistant"
	case displaySegmentThinking:
		return "thinking"
	case displaySegmentConfigNotice:
		return messageRoleConfigNotice
	case displaySegmentError:
		return "error"
	case displaySegmentRecap:
		return "recap"
	case displaySegmentBackground:
		return "background"
	case displaySegmentLoop:
		return "loop"
	case displaySegmentAgent:
		return "agent"
	case displaySegmentTool:
		return "tool"
	case displaySegmentTodo:
		return "status"
	default:
		return "status"
	}
}
