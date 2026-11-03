package server

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/files"
	"github.com/konglong87/super-niuma/internal/query"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	"github.com/konglong87/super-niuma/internal/tools"
)

// file_change payload vocabulary. The Files tab renders these verbatim, so they
// are part of the event contract and must stay in sync with the TypeScript
// reader in web/src/components/WebAgentPage.tsx.
const (
	fileAccessEdited = "edited"
	fileAccessRead   = "read"

	fileChangeCreated  = "created"
	fileChangeModified = "modified"
	fileChangeDeleted  = "deleted"

	fileObjectFile    = "file"
	fileObjectDir     = "dir"
	fileObjectSymlink = "symlink"
)

// fileReadToolInputKeys lists the input fields the read-family tools declare for
// their target path, in priority order.
var fileReadToolInputKeys = []string{"file_path", "notebook_path", "path"}

// fileReadTools are the tools whose whole purpose is reading one named file, so
// their input path is a faithful record of what the run looked at. Search tools
// (Grep/Glob) are excluded: they take a pattern, not a file.
var fileReadTools = map[string]bool{
	"Read":         true,
	"NotebookRead": true,
}

// agentTaskFileChangeEvents derives the file_change payloads for one completed
// tool call.
//
// Writes come from trace.FileChanges, which every write-family tool already
// populates through tools.Context.FileChange; nothing is inferred. Reads have no
// such record, so they fall back to the path in the tool's own input — still the
// real argument, not a guessed payload key. An errored call is skipped entirely
// because a failed read read nothing and a failed write reports no changes.
func agentTaskFileChangeEvents(source string, trace query.ToolTrace) []map[string]any {
	if trace.IsError {
		return nil
	}
	if len(trace.FileChanges) > 0 {
		events := make([]map[string]any, 0, len(trace.FileChanges))
		for _, change := range trace.FileChanges {
			if payload := fileChangePayload(source, trace, change); payload != nil {
				events = append(events, payload)
			}
		}
		return events
	}
	if !fileReadTools[trace.Name] {
		return nil
	}
	path := fileReadTargetPath(trace.Input)
	if path == "" {
		return nil
	}
	return []map[string]any{{
		"source":    source,
		"tool_id":   trace.ID,
		"tool_name": trace.Name,
		"path":      path,
		"access":    fileAccessRead,
	}}
}

func fileChangePayload(source string, trace query.ToolTrace, change tools.FileChange) map[string]any {
	path := strings.TrimSpace(change.Path)
	if path == "" {
		return nil
	}
	payload := map[string]any{
		"source":        source,
		"tool_id":       trace.ID,
		"tool_name":     trace.Name,
		"path":          path,
		"access":        fileAccessEdited,
		"change":        fileChangeKind(change),
		"before_exists": change.BeforeExists,
		"after_exists":  change.AfterExists,
	}
	// Source records which tool boundary captured the change (edit/write/bash);
	// prefer it over the generic sink source when present.
	if boundary := strings.TrimSpace(change.Source); boundary != "" {
		payload["source"] = boundary
	}
	if object := fileChangeObject(change); object != fileObjectFile {
		payload["object"] = object
		// Line counts are meaningless for directories and symlinks.
		return payload
	}
	if change.ModeChanged {
		payload["mode_changed"] = true
	}
	// Content is externalized to the snapshot store once it grows past the inline
	// threshold. Reporting a zero delta in that case would present a missing
	// measurement as a real one, so the payload says so instead.
	if !fileChangeContentAvailable(change) {
		payload["content_available"] = false
		return payload
	}
	beforeLines := countLines(change.Before, change.BeforeExists)
	afterLines := countLines(change.After, change.AfterExists)
	payload["content_available"] = true
	payload["before_lines"] = beforeLines
	payload["after_lines"] = afterLines
	payload["line_delta"] = afterLines - beforeLines
	return payload
}

func fileChangeKind(change tools.FileChange) string {
	switch {
	case !change.BeforeExists && change.AfterExists:
		return fileChangeCreated
	case change.BeforeExists && !change.AfterExists:
		return fileChangeDeleted
	default:
		return fileChangeModified
	}
}

func fileChangeObject(change tools.FileChange) string {
	switch {
	case change.AfterIsSymlink || change.BeforeIsSymlink:
		return fileObjectSymlink
	case change.AfterIsDir || change.BeforeIsDir:
		return fileObjectDir
	default:
		return fileObjectFile
	}
}

// fileChangeContentAvailable reports whether both sides of the change carry
// real inline content, which is what makes a line count truthful. A snapshot
// path on either side means the body lives in the blob store; the after-side
// placeholder means the after-scan gave up on the size.
func fileChangeContentAvailable(change tools.FileChange) bool {
	if change.BeforeSnapshotPath != "" || change.AfterSnapshotPath != "" {
		return false
	}
	return change.After != files.CaptureSnapshotPlaceholder
}

// countLines counts the lines of a captured file body. A trailing newline does
// not open a new line, and a file that does not exist has no lines at all
// (distinct from an existing empty file, which has none either but is reached
// through the exists flag).
func countLines(content string, exists bool) int {
	if !exists || content == "" {
		return 0
	}
	count := strings.Count(content, "\n")
	if !strings.HasSuffix(content, "\n") {
		count++
	}
	return count
}

// fileReadTargetPath pulls the read target out of a tool's raw input JSON.
func fileReadTargetPath(input string) string {
	if strings.TrimSpace(input) == "" {
		return ""
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(input), &decoded); err != nil {
		return ""
	}
	for _, key := range fileReadToolInputKeys {
		if value, ok := decoded[key].(string); ok {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

// appendAgentTaskFileChangeEvents writes the derived file_change rows. A failure
// here is returned so the caller treats it like any other event-append failure.
func appendAgentTaskFileChangeEvents(ctx context.Context, svc TenantService, taskID uint64, traceID, source string, trace query.ToolTrace) error {
	return appendAgentTaskFileChangeEventsWithOptions(ctx, Options{TenantService: svc}, mysqlstore.AgentTask{ID: taskID}, traceID, source, trace)
}

func appendAgentTaskFileChangeEventsWithOptions(ctx context.Context, opts Options, task mysqlstore.AgentTask, traceID, source string, trace query.ToolTrace) error {
	for _, payload := range agentTaskFileChangeEvents(source, trace) {
		if _, err := appendAgentTaskEvent(ctx, opts, task, agenttasks.EventInput{
			TaskID:      task.ID,
			EventType:   agenttasks.EventFileChange,
			PayloadJSON: agentTaskEventPayload(payload),
			TraceID:     traceID,
		}); err != nil {
			return err
		}
	}
	return nil
}
