package notebook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/konglong87/super-niuma/internal/tools"
)

// A notebook is kept as the raw decoded document rather than a struct with a
// fixed field set. Anything the struct did not name — the nbformat 4.5 mandatory
// cell `id`, attachments, vendor extensions — used to be dropped on every edit,
// silently producing a schema-invalid notebook (AUDIT-P1-15).
type rawNotebook struct {
	Doc   map[string]any
	Cells []map[string]any
}

type ReadTool struct{}
type EditTool struct{}

func NewRead() ReadTool { return ReadTool{} }
func NewEdit() EditTool { return EditTool{} }

func (ReadTool) Name() string { return "NotebookRead" }

func (ReadTool) MaxResultSizeChars() int { return tools.ClaudeCodeDefaultDeclaredMaxResultSizeChars }

func (ReadTool) Description() string {
	return `Read a Jupyter .ipynb notebook and return cell indexes, types, source, and output counts.

Use NotebookRead to inspect notebook structure before editing. Returns cell type
(code/markdown/raw), source content, and output count for each cell.
Use cell_index to read a specific cell instead of the entire notebook.`
}

func (ReadTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
	  "type": "object",
	  "properties": {
	    "file_path": {"type": "string", "description": "Path to the .ipynb file."},
	    "cell_index": {"type": "integer", "description": "Optional zero-based cell index to read."}
	  },
	  "required": ["file_path"],
	  "additionalProperties": false
	}`)
}

func (ReadTool) Run(_ context.Context, input json.RawMessage, toolContext tools.Context) tools.Result {
	var params struct {
		FilePath  string `json:"file_path"`
		CellIndex *int   `json:"cell_index"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	path, err := tools.ResolvePath(toolContext.CWD, params.FilePath)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	nb, err := load(path)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	var indexes []int
	if params.CellIndex != nil {
		if *params.CellIndex < 0 || *params.CellIndex >= len(nb.Cells) {
			return tools.Result{Content: fmt.Sprintf("cell_index %d out of range", *params.CellIndex), IsError: true}
		}
		indexes = []int{*params.CellIndex}
	} else {
		for i := range nb.Cells {
			indexes = append(indexes, i)
		}
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Notebook: %s (%d cells)\n", path, len(nb.Cells))
	for _, index := range indexes {
		cell := nb.Cells[index]
		fmt.Fprintf(&out, "\nCell %d [%s]\n", index, cellType(cell))
		out.WriteString(strings.TrimRight(sourceText(cell["source"]), "\n"))
		out.WriteString("\n")
		if count := outputCount(cell); count > 0 {
			fmt.Fprintf(&out, "Outputs: %d\n", count)
		}
	}
	return tools.Result{Content: out.String()}
}

func (EditTool) Name() string { return "NotebookEdit" }

func (EditTool) MaxResultSizeChars() int { return tools.ClaudeCodeDefaultDeclaredMaxResultSizeChars }

func (EditTool) Description() string {
	return `Edit a Jupyter .ipynb notebook cell by zero-based index.

Use NotebookEdit to modify a specific cell's source or type. The cell_index is
zero-based. Read the notebook first to identify the correct cell index.
You can change the cell_type (code/markdown/raw) along with the source content.`
}

func (EditTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
	  "type": "object",
	  "properties": {
	    "file_path": {"type": "string", "description": "Path to the .ipynb file."},
	    "cell_index": {"type": "integer", "description": "Zero-based cell index to edit."},
	    "new_source": {"type": "string", "description": "Replacement cell source."},
	    "cell_type": {"type": "string", "enum": ["code", "markdown", "raw"], "description": "Optional replacement cell type."}
	  },
	  "required": ["file_path", "cell_index", "new_source"],
	  "additionalProperties": false
	}`)
}

func (EditTool) Run(_ context.Context, input json.RawMessage, toolContext tools.Context) tools.Result {
	var params struct {
		FilePath  string `json:"file_path"`
		CellIndex int    `json:"cell_index"`
		NewSource string `json:"new_source"`
		CellType  string `json:"cell_type"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	path, err := tools.ResolvePath(toolContext.CWD, params.FilePath)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	if err := tools.EnsureWritablePathWithSandbox(toolContext.CWD, toolContext.WritableRoots, path, toolContext.Sandbox); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	nb, err := load(path)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	if params.CellIndex < 0 || params.CellIndex >= len(nb.Cells) {
		return tools.Result{Content: fmt.Sprintf("cell_index %d out of range", params.CellIndex), IsError: true}
	}
	if params.CellType != "" {
		if params.CellType != "code" && params.CellType != "markdown" && params.CellType != "raw" {
			return tools.Result{Content: fmt.Sprintf("unsupported cell_type: %s", params.CellType), IsError: true}
		}
		nb.Cells[params.CellIndex]["cell_type"] = params.CellType
	}
	nb.Cells[params.CellIndex]["source"] = splitSource(params.NewSource)
	if err := save(path, nb); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	return tools.Result{Content: fmt.Sprintf("Updated notebook cell %d in %s", params.CellIndex, path)}
}

func load(path string) (rawNotebook, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return rawNotebook{}, err
	}
	// UseNumber keeps numeric literals byte-identical instead of routing them
	// through float64, which would rewrite large or precise numbers on save.
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var doc map[string]any
	if err := decoder.Decode(&doc); err != nil {
		return rawNotebook{}, err
	}
	rawCells, _ := doc["cells"].([]any)
	cells := make([]map[string]any, 0, len(rawCells))
	for i, item := range rawCells {
		cell, ok := item.(map[string]any)
		if !ok {
			return rawNotebook{}, fmt.Errorf("cell %d is not an object", i)
		}
		cells = append(cells, cell)
	}
	return rawNotebook{Doc: doc, Cells: cells}, nil
}

// save writes the document back with the (possibly mutated) cells reattached.
// Keys land in sorted order, which is what nbformat itself writes.
func save(path string, nb rawNotebook) error {
	cells := make([]any, 0, len(nb.Cells))
	for _, cell := range nb.Cells {
		cells = append(cells, cell)
	}
	nb.Doc["cells"] = cells
	data, err := json.MarshalIndent(nb.Doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0600)
}

func cellType(cell map[string]any) string {
	text, _ := cell["cell_type"].(string)
	return text
}

func outputCount(cell map[string]any) int {
	outputs, _ := cell["outputs"].([]any)
	return len(outputs)
}

func sourceText(source any) string {
	switch value := source.(type) {
	case string:
		return value
	case []any:
		var b strings.Builder
		for _, part := range value {
			b.WriteString(fmt.Sprint(part))
		}
		return b.String()
	case []string:
		return strings.Join(value, "")
	default:
		return ""
	}
}

func splitSource(source string) []string {
	if source == "" {
		return []string{""}
	}
	return strings.SplitAfter(source, "\n")
}
