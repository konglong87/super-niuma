package computerdiag

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

const (
	SchemaVersion   = "computer-use-diagnostic.v1"
	ToolObservePath = "/tmp/tool-observe.log"
	GoMismatchPath  = "/tmp/go-mismatch.log"
)

var writeMu sync.Mutex

// Append writes one sanitized diagnostic event as a single JSON line. Callers
// must pass only metadata; screenshots, credentials, headers, and raw provider
// or helper errors must never be included.
func Append(path string, fields map[string]any) {
	if path == "" {
		return
	}
	event := make(map[string]any, len(fields)+2)
	for key, value := range fields {
		event[key] = value
	}
	event["schema_version"] = SchemaVersion
	event["timestamp"] = time.Now().UTC().Format(time.RFC3339Nano)
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	data = append(data, '\n')

	writeMu.Lock()
	defer writeMu.Unlock()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = file.Write(data)
}
