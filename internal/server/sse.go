package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	_ "github.com/konglong87/super-niuma/docs"
)

func writeSSEError(w http.ResponseWriter, id string, created int64, model string, message string) {
	data, err := json.Marshal(map[string]any{
		"id":      id,
		"object":  "error",
		"created": created,
		"model":   model,
		"error": map[string]any{
			"type":    "server_error",
			"message": message,
		},
	})
	if err != nil {
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", data)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func writeSSEData(w http.ResponseWriter, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", data)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func writeSSEChunk(w http.ResponseWriter, chunk openAIStreamChunk) {
	data, err := json.Marshal(chunk)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", data)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}
