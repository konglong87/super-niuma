package query

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/defaults"
	"github.com/konglong87/super-niuma/internal/tools"
)

type computerRequestTool struct {
	calls  int
	images []string
}

func (*computerRequestTool) Name() string { return "ComputerUse" }
func (*computerRequestTool) Description() string {
	return "synthetic desktop observation; no native input"
}
func (*computerRequestTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"action":{"type":"string"}},"required":["action"]}`)
}
func (tool *computerRequestTool) Run(_ context.Context, _ json.RawMessage, tc tools.Context) tools.Result {
	if !tc.ComputerUseFastPath {
		return tools.Result{IsError: true, Content: "fast path did not reach the tool"}
	}
	tool.calls++
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: uint8(tool.calls), A: 255})
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		return tools.Result{IsError: true, Content: "synthetic PNG failed"}
	}
	data := base64.StdEncoding.EncodeToString(buffer.Bytes())
	tool.images = append(tool.images, data)
	return tools.Result{
		Content: fmt.Sprintf(`{"observation":{"id":"synthetic-observation-%d"},"dispatch_state":"complete","outcome":"executed"}`, tool.calls),
		ContextMessages: []anthropic.MessageParam{{Role: "user", Content: []anthropic.ContentBlock{
			{Type: blockTypeText, Text: "Fresh synthetic observation"},
			{Type: blockTypeImage, Source: &anthropic.ContentSource{Type: "base64", MediaType: "image/png", Data: data}},
		}}},
	}
}

// Exercise the production Responses HTTP adapter, not just a classifier or an
// image-history helper. No real provider, credential, desktop capture or input.
func TestComputerAcceptancePromptBoundsActualResponsesRequests(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("NIUMA_CONFIG_DIR", t.TempDir())
	cwd := t.TempDir()
	const workspaceMarker = "workspace-discovery-must-not-enter-desktop-wire"
	if err := os.WriteFile(filepath.Join(cwd, "CLAUDE.md"), []byte(workspaceMarker), 0600); err != nil {
		t.Fatal(err)
	}
	prompt, err := os.ReadFile("../computeracceptance/assets/model-closure-prompt.txt")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var requests []json.RawMessage
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			t.Error(readErr)
			return
		}
		if r.URL.Path != "/v1/responses" {
			t.Errorf("unexpected provider path: %s", r.URL.Path)
		}
		mu.Lock()
		requests = append(requests, body)
		turn := len(requests)
		mu.Unlock()
		writeComputerRequestResponse(w, turn)
	}))
	defer provider.Close()
	client := anthropic.NewClient(config.Config{
		Provider: "custom", ProviderProtocol: config.ProviderProtocolOpenAIResponses,
		BaseURL: provider.URL + "/v1", APIKey: "synthetic-test-only",
	})
	tool := &computerRequestTool{}
	runtime := New(client, tools.NewRegistry(tool, echoTool{}), Options{
		Model: "gpt-6-sol", MaxTurns: 5, MaxTokens: defaults.CodeMaxTokens, Effort: "high", CWD: cwd,
		ComputerUse: fastPathComputerService{}, ComputerUseImageSupported: true,
	})
	if _, err := runtime.Run(context.Background(), string(prompt), io.Discard); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 4 || tool.calls != 3 {
		t.Fatalf("HTTP requests=%d synthetic observations=%d, want 4/3", len(requests), tool.calls)
	}
	for index, raw := range requests {
		var request struct {
			Model           string `json:"model"`
			MaxOutputTokens int    `json:"max_output_tokens"`
			Reasoning       struct {
				Effort string `json:"effort"`
			} `json:"reasoning"`
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		}
		if err := json.Unmarshal(raw, &request); err != nil {
			t.Fatal(err)
		}
		if request.Model != "gpt-6-sol" || request.Reasoning.Effort != "high" || request.MaxOutputTokens != computerUseFastPathMaxTokens {
			t.Fatalf("turn %d route/budget changed: %+v", index+1, request)
		}
		if len(request.Tools) != 1 || request.Tools[0].Name != "ComputerUse" || bytes.Contains(raw, []byte(workspaceMarker)) {
			t.Fatalf("turn %d loaded unrelated tool/workspace context", index+1)
		}
		wantImages := 0
		if index > 0 {
			wantImages = 1
			if !bytes.Contains(raw, []byte(tool.images[index-1])) {
				t.Errorf("turn %d lost latest authoritative image", index+1)
			}
		}
		if count := bytes.Count(raw, []byte(`"type":"input_image"`)); count != wantImages {
			t.Errorf("turn %d image count=%d, want %d", index+1, count, wantImages)
		}
		for previous := 0; previous < index-1; previous++ {
			if bytes.Contains(raw, []byte(tool.images[previous])) {
				t.Errorf("turn %d retained old screenshot %d", index+1, previous+1)
			}
		}
		// Pruning old screenshots must not delete tool protocol receipts.
		if count := bytes.Count(raw, []byte(`"type":"function_call_output"`)); count != index {
			t.Errorf("turn %d retained receipts=%d, want %d", index+1, count, index)
		}
	}
}

func writeComputerRequestResponse(w http.ResponseWriter, turn int) {
	w.Header().Set("Content-Type", "text/event-stream")
	var item map[string]any
	if turn <= 3 {
		item = map[string]any{"id": fmt.Sprintf("fc_%d", turn), "type": "function_call", "call_id": fmt.Sprintf("call_%d", turn), "name": "ComputerUse", "arguments": `{"action":"observe"}`, "status": "completed"}
	} else {
		item = map[string]any{"id": "msg_done", "type": "message", "role": "assistant", "status": "completed", "content": []map[string]any{{"type": "output_text", "text": "done", "annotations": []any{}}}}
	}
	for index, event := range []map[string]any{
		{"type": "response.output_item.added", "output_index": 0, "item": item},
		{"type": "response.output_item.done", "output_index": 0, "item": item},
		{"type": "response.completed", "response": map[string]any{"id": fmt.Sprintf("resp_%d", turn), "object": "response", "status": "completed", "model": "gpt-6-sol", "output": []map[string]any{item}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}},
	} {
		event["sequence_number"] = index
		data, _ := json.Marshal(event)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
	}
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
}

func TestComputerFastPathDoesNotRaiseExplicitLowerOutputBudget(t *testing.T) {
	for _, cap := range []int{1536, computerUseFastPathMaxTokens} {
		streamer := &systemStreamer{}
		runtime := New(streamer, tools.NewRegistry(), Options{Model: "test", CWD: t.TempDir(), MaxTokens: cap, MaxTurns: 1, Effort: "high", ComputerUseFastPath: true})
		if _, err := runtime.Run(context.Background(), "probe", io.Discard); err != nil {
			t.Fatal(err)
		}
		if len(streamer.maxTokens) != 1 || streamer.maxTokens[0] != cap || !strings.EqualFold(streamer.thinking[0].Effort, "high") {
			t.Fatalf("lower explicit budget changed: max_tokens=%v thinking=%+v", streamer.maxTokens, streamer.thinking)
		}
	}
}
