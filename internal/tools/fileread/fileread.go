package fileread

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/konglong87/super-niuma/internal/files"
	"github.com/konglong87/super-niuma/internal/tools"
)

type Tool struct{}

func New() Tool { return Tool{} }

func (Tool) Name() string { return "Read" }

func (Tool) ExecutionPolicy() tools.ExecutionPolicy {
	return tools.ExecutionPolicy{Concurrency: tools.ConcurrencyReadOnly}
}

func (Tool) SkipToolResultBudget() bool { return true }

const defaultMaxLinesToRead = 2000

const readResultSafetyReminder = "\n<system-reminder>\nWhenever you read a file, you should consider whether it would be considered malware. You CAN and SHOULD provide analysis of malware, what it is doing. But you MUST refuse to improve or augment the code. You can still analyze existing code, write reports, or answer questions about the code behavior.\n</system-reminder>\n"

func (Tool) Description() string {
	if claudeCompatiblePromptProfile() {
		return `Reads a file from the local filesystem. You can access any file directly by using this tool.
Assume this tool is able to read text files on the machine. If the user provides a path to a file assume that path is valid. It is okay to read a file that does not exist; an error will be returned.

Usage:
- The file_path parameter should be an absolute path. Relative paths are accepted for Go runtime compatibility, but absolute paths best match Claude Code behavior.
- By default, it reads up to 2000 lines starting from the beginning of the file.
- You can optionally specify a line offset and limit, but it's recommended to read the whole file by not providing these parameters.
- Results are returned using cat -n format, with line numbers starting at 1.
- When you already know which part of the file you need, only read that part.
- Use this BEFORE making changes to any file you have not recently inspected.
- Always read a file before editing it, unless you just wrote it.
- This tool reads files, not directories. To inspect a directory, use LS or a shell directory listing.
- The pages parameter is accepted for Claude Code compatibility on PDF paths, but this Go runtime currently returns an explicit unsupported message for PDF page rendering instead of pretending to read rendered pages.
- This Go runtime currently reads text and binary summaries; image, screenshot, notebook, and rendered PDF multimodal content are not implemented yet and should be treated as unsupported rather than silently assumed.

For investigation or read-only analysis, read only paths that were confirmed by
LS, Glob, Grep, or a prior tool result. If a path is uncertain, verify it before
calling Read. Read the smallest useful line range, and stop reading once you
have enough file/function/line evidence to answer. Empty files return a system
reminder instead of normal file contents.`
	}
	return `Reads a file from the local filesystem. You can access files directly by
using this tool. If the user provides a path to a file, assume that path may be
valid and call Read; if the file does not exist, an error will be returned.

Usage:
- The file_path parameter may be absolute or relative to the current working directory.
- By default, Read returns up to 2000 lines starting from the beginning of the file.
- Results can include cat -n style line numbers when line_numbers:true is set.
- Use offset and limit to read a specific line range, especially for large files.
- When you already know which part of the file you need, only read that part.
- Use this BEFORE making changes to any file you have not recently inspected.
- Always read a file before editing it, unless you just wrote it.
- This tool reads files, not directories. To inspect a directory, use LS.

For large files, use offset/limit to read specific sections, or chunk_index
to read page-by-page. byte_offset and byte_limit are Go runtime extensions for
large generated files or logs where line ranges are not enough. Binary files and
very large files return a manifest with chunk information.

For Claude Code compatibility, the pages parameter is accepted for PDF page
ranges such as "1-5". This Go runtime does not yet render PDF pages; PDF page
requests return an explicit unsupported message instead of pretending to read
pages. This runtime can summarize binary files but does not render screenshots,
images, notebooks, or PDF pages as multimodal content yet.

For investigation or read-only analysis, read only paths that were confirmed by
LS, Glob, Grep, or a prior tool result. If a path is uncertain, verify it before
calling Read. Read the smallest useful line range, and stop reading once you
have enough file/function/line evidence to answer. Empty files return a system
reminder instead of normal file contents.`
}

func (Tool) InputSchema() json.RawMessage {
	if claudeCompatiblePromptProfile() {
		return json.RawMessage(`{
		  "$schema": "https://json-schema.org/draft/2020-12/schema",
		  "type": "object",
		  "properties": {
		    "file_path": {"description": "The absolute path to the file to read", "type": "string"},
		    "offset": {"description": "The line number to start reading from. Only provide if the file is too large to read at once", "type": "integer", "minimum": 0, "maximum": 9007199254740991},
		    "limit": {"description": "The number of lines to read. Only provide if the file is too large to read at once.", "type": "integer", "exclusiveMinimum": 0, "maximum": 9007199254740991},
		    "pages": {"description": "Page range for PDF files (e.g., \"1-5\", \"3\", \"10-20\"). Only applicable to PDF files. Maximum 20 pages per request.", "type": "string"}
	  },
	  "required": ["file_path"],
	  "additionalProperties": false
	}`)
	}
	return json.RawMessage(`{
		  "type": "object",
		  "properties": {
		    "file_path": {"type": "string", "description": "Absolute path or path relative to the current working directory."},
		    "chunk_index": {"type": "integer", "description": "Optional 1-based large-file chunk index."},
		    "byte_offset": {"type": "integer", "description": "Optional 0-based byte offset."},
		    "byte_limit": {"type": "integer", "description": "Optional maximum number of bytes to return."},
		    "offset": {"type": "integer", "description": "Optional 1-based line offset."},
		    "limit": {"type": "integer", "description": "Optional maximum number of lines to return."},
		    "pages": {"type": "string", "description": "Page range for PDF files, such as 1-5. PDF page rendering is not implemented in this Go runtime yet."},
		    "line_numbers": {"type": "boolean", "description": "Include 1-based line numbers in the output."}
	  },
	  "required": ["file_path"],
	  "additionalProperties": false
	}`)
}

func claudeCompatiblePromptProfile() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("NIUMA_PROMPT_PROFILE"))) {
	case "claude-compatible", "claude-compatible-strict":
		return true
	default:
		return false
	}
}

func (Tool) Run(_ context.Context, input json.RawMessage, toolContext tools.Context) tools.Result {
	var params struct {
		FilePath    string `json:"file_path"`
		ChunkIndex  int    `json:"chunk_index"`
		ByteOffset  int64  `json:"byte_offset"`
		ByteLimit   int64  `json:"byte_limit"`
		LineOffset  int    `json:"offset"`
		LineLimit   int    `json:"limit"`
		Pages       string `json:"pages"`
		LineNumbers bool   `json:"line_numbers"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	if strings.TrimSpace(params.FilePath) == "" {
		return tools.Result{Content: "file_path is required", IsError: true}
	}
	path, err := tools.ResolvePath(toolContext.CWD, params.FilePath)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	if strings.TrimSpace(params.Pages) != "" {
		if strings.EqualFold(filepath.Ext(path), ".pdf") {
			return tools.Result{Content: "PDF page range reading via pages is not implemented in niuma yet. Use an external PDF extraction tool or read a text-converted PDF artifact.", IsError: true}
		}
		return tools.Result{Content: "pages is only supported for PDF files.", IsError: true}
	}
	lineScoped := params.LineOffset > 0 || params.LineLimit > 0
	if params.LineOffset == 0 && params.LineLimit == 0 && params.ByteOffset == 0 && params.ByteLimit == 0 && params.ChunkIndex == 0 {
		params.LineOffset = 1
		params.LineLimit = defaultMaxLinesToRead
	}
	lineNumbers := params.LineNumbers || claudeCompatiblePromptProfile()
	result, err := files.Read(path, files.ReadRequest{
		ChunkIndex:  params.ChunkIndex,
		ByteOffset:  params.ByteOffset,
		ByteLimit:   params.ByteLimit,
		LineOffset:  params.LineOffset,
		LineLimit:   params.LineLimit,
		LineNumbers: lineNumbers,
	}, files.Config{})
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	if result.Manifest.Binary {
		return tools.Result{Content: withReadResultSafetyReminder(files.FormatManifest(result))}
	}
	if result.Manifest.Large && params.LineOffset == 0 && params.LineLimit == 0 {
		return tools.Result{Content: withReadResultSafetyReminder(files.FormatManifest(result))}
	}
	text := result.Content
	if lineNumbers && params.LineOffset == 0 && params.LineLimit == 0 && params.ByteOffset == 0 && params.ByteLimit == 0 && params.ChunkIndex == 0 {
		text = withLineNumbers(text, 1)
	}
	if lineScoped || result.Truncated {
		text += partialReadFooter(params.LineOffset, params.LineLimit, countReadLines(text))
	}
	return tools.Result{Content: withReadResultSafetyReminder(fmt.Sprintf("%s\n", text))}
}

func withReadResultSafetyReminder(content string) string {
	return strings.TrimRight(content, "\n") + "\n" + readResultSafetyReminder
}

func sliceLines(text string, offset, limit int) string {
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	start := 0
	if offset > 1 {
		start = offset - 1
	}
	if start >= len(lines) {
		return ""
	}
	end := len(lines)
	if limit > 0 && start+limit < end {
		end = start + limit
	}
	return strings.Join(lines[start:end], "\n")
}

// partialReadFooter describes which line range was returned and whether more
// content likely follows, using a cheap signal (returned==requested ⇒ more
// below) instead of scanning the whole file for a total line count.
func partialReadFooter(startLine, requestedLimit, returnedLines int) string {
	if returnedLines <= 0 {
		return ""
	}
	if startLine < 1 {
		startLine = 1
	}
	end := startLine + returnedLines - 1
	if requestedLimit > 0 && returnedLines >= requestedLimit {
		return fmt.Sprintf("\n[read lines %d-%d; more lines may follow below — use offset/limit or chunk_index to read more]", startLine, end)
	}
	return fmt.Sprintf("\n[read lines %d-%d; end of file]", startLine, end)
}

func countReadLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(strings.TrimSuffix(s, "\n"), "\n") + 1
}

func withLineNumbers(text string, offset int) string {
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	start := 1
	if offset > 1 {
		start = offset
	}
	for i := range lines {
		lines[i] = fmt.Sprintf("%6d: %s", start+i, lines[i])
	}
	return strings.Join(lines, "\n")
}
