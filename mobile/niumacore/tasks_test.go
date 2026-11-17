package niumacore

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/mobile/phone"
)

// jsonPhone implements DeviceBridge with JSON, as the Kotlin service does.
type jsonPhone struct {
	mu     sync.Mutex
	alarm  bool
	acts   []string
	pkg    string
	listen chan string
}

func (p *jsonPhone) Snapshot() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	nodes := `[{"parent":-1,"cls":"android.widget.FrameLayout","b":[0,0,1080,2400]},` +
		`{"parent":0,"cls":"android.widget.ImageButton","desc":"Add alarm","b":[440,2000,640,2200],"click":true}`
	if p.alarm {
		nodes += `,{"parent":0,"cls":"android.widget.TextView","text":"7:00 AM","b":[0,600,540,700]}`
	}
	return `{"package":"` + p.pkg + `","home":"com.example.home","nodes":` + nodes + `]}`
}

func (p *jsonPhone) Perform(action string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.acts = append(p.acts, action)
	var a phone.Action
	json.Unmarshal([]byte(action), &a)
	switch {
	case a.Kind == "launch":
		p.pkg = a.App
	case a.Kind == "tap" && a.Index == 1:
		p.alarm = true
	}
	return `{"status":"done"}`
}

type listener struct{ ch chan string }

func (l listener) OnState(s string) {
	select {
	case l.ch <- s:
	default:
	}
}

func TestTaskRunsEndToEndThroughExecution(t *testing.T) {
	resetInit(t)
	dir := canonicalTempDir(t)
	stateDir := filepath.Join(dir, "super-state")
	if err := os.Mkdir(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if r := Init(stateDir); !strings.Contains(r, `"ok":true`) {
		t.Fatal(r)
	}
	t.Cleanup(func() {
		tasksMu.Lock()
		runner = nil
		tasksMu.Unlock()
	})
	if r := StartTask(`{}`); !strings.Contains(r, "OpenTasks first") {
		t.Fatalf("start before open: %s", r)
	}
	if r := OpenTasks(stateDir, "io.github.konglong87.superniuma.mobile"); !strings.Contains(r, "outside the Super state directory") {
		t.Fatalf("workspace inside the state directory accepted: %s", r)
	}
	if r := OpenTasks(dir, "io.github.konglong87.superniuma.mobile"); !strings.Contains(r, `"ok":true`) {
		t.Fatal(r)
	}

	var mu sync.Mutex
	var calls int
	script := []string{
		`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"c1","type":"function","function":{"name":"super_launch","arguments":"{\"app\":\"com.example.clock\",\"intent\":\"open clock\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":50,"completion_tokens":5}}`,
		`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"c2","type":"function","function":{"name":"super_tap","arguments":"{\"ref\":1,\"intent\":\"add alarm\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":60,"completion_tokens":5}}`,
		`{"choices":[{"message":{"role":"assistant","content":"Added a 7:00 alarm."},"finish_reason":"stop"}],"usage":{"prompt_tokens":70,"completion_tokens":6}}`,
	}
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		n := calls
		calls++
		bodies = append(bodies, string(body))
		mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer relay-test-token" || n >= len(script) {
			http.Error(w, "unexpected", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, script[n])
	}))
	defer server.Close()

	spec := `{"goal":"Set an alarm for 7:00","apps":["com.example.clock"],"expect":["7:00"],` +
		`"route":{"base_url":"` + server.URL + `/v1","model":"m","token":"relay-test-token"}}`
	if r := StartTask(spec); !strings.Contains(r, "screen reader is off") {
		t.Fatalf("started without a bridge: %s", r)
	}
	p := &jsonPhone{pkg: "io.github.konglong87.superniuma.mobile"}
	SetBridge(p)
	states := listener{ch: make(chan string, 64)}
	SetListener(states)
	r := StartTask(spec)
	if !strings.Contains(r, `"ok":true`) {
		t.Fatal(r)
	}
	deadline := time.After(20 * time.Second)
	for {
		var s phone.State
		json.Unmarshal([]byte(TaskState()), &s)
		if s.Task != nil && s.Task.Status != phone.StatusRunning {
			if s.Task.Status != phone.StatusDone || s.Task.Verified == nil || !*s.Task.Verified || s.Task.Result != "Added a 7:00 alarm." {
				t.Fatalf("task ended as %+v", s.Task)
			}
			if len(s.Task.Steps) != 2 || s.Task.Steps[0].Kind != "launch" || s.Task.Steps[1].Target != "Add alarm" {
				t.Fatalf("steps %+v", s.Task.Steps)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatalf("task did not finish: %s", TaskState())
		case <-time.After(20 * time.Millisecond):
		}
	}
	if len(p.acts) != 2 || calls != 3 {
		t.Fatalf("actions %v, model calls %d", p.acts, calls)
	}
	if !strings.Contains(bodies[1], "Add alarm") {
		t.Fatalf("model did not see the clock screen after launching: %.400s", bodies[1])
	}
	if len(states.ch) == 0 {
		t.Fatal("listener never called")
	}
	if r := StopTask(); !strings.Contains(r, "no task is running") {
		t.Fatalf("stop after finish: %s", r)
	}
}
