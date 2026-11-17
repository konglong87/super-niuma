package niumacore

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/konglong87/super-niuma/internal/super"
	"github.com/konglong87/super-niuma/mobile/phone"
)

// DeviceBridge is implemented by the app's accessibility service.
type DeviceBridge interface {
	// Snapshot returns the active window in the snapshot format, or "" when
	// no window is readable.
	Snapshot() string
	// Perform runs one action (phone.Action as JSON) on the latest snapshot,
	// waits for the screen to settle and returns phone.Result as JSON.
	Perform(actionJSON string) string
}

// Listener receives the task state (phone.State as JSON) when it changes.
// It is called on a background goroutine.
type Listener interface {
	OnState(stateJSON string)
}

var (
	tasksMu sync.Mutex
	runner  *phone.Runner
)

type okReport struct {
	OK    bool   `json:"ok"`
	ID    string `json:"id,omitempty"`
	Error string `json:"error,omitempty"`
}

// OpenTasks opens the task store under dataDir and prepares the runner. Call
// it after Init; calling it again is a no-op. A task left running by a
// previous process is marked interrupted.
func OpenTasks(dataDir, ownPackage string) string {
	tasksMu.Lock()
	defer tasksMu.Unlock()
	if runner != nil {
		return encode(okReport{OK: true})
	}
	state, err := super.StateDirectory()
	if err != nil {
		return encode(okReport{Error: "call Init first"})
	}
	if !filepath.IsAbs(dataDir) || filepath.Clean(dataDir) != dataDir {
		return encode(okReport{Error: "data directory must be a clean absolute path"})
	}
	workspace := filepath.Join(dataDir, "phone-workspace")
	// Execution refuses a workspace inside Super's protected state; say so
	// now instead of failing every task as revoked authority.
	if rel, err := filepath.Rel(state, workspace); err == nil && !strings.HasPrefix(rel, "..") {
		return encode(okReport{Error: "data directory must be outside the Super state directory"})
	}
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		return encode(okReport{Error: "create workspace"})
	}
	store, err := phone.OpenStore(filepath.Join(dataDir, "phone-tasks.db"))
	if err != nil {
		return encode(okReport{Error: "open task store: " + err.Error()})
	}
	runner = phone.NewRunner(store, workspace, ownPackage, nil)
	return encode(okReport{OK: true})
}

func currentRunner() (*phone.Runner, error) {
	tasksMu.Lock()
	defer tasksMu.Unlock()
	if runner == nil {
		return nil, errors.New("call OpenTasks first")
	}
	return runner, nil
}

type bridgeAdapter struct{ b DeviceBridge }

func (a bridgeAdapter) Snapshot() (phone.Snapshot, error) {
	data := a.b.Snapshot()
	if data == "" {
		return phone.Snapshot{}, phone.ErrNoWindow
	}
	return phone.ParseSnapshot(data)
}

func (a bridgeAdapter) Perform(action phone.Action) (phone.Result, error) {
	data, err := json.Marshal(action)
	if err != nil {
		return phone.Result{}, err
	}
	var r phone.Result
	if err := json.Unmarshal([]byte(a.b.Perform(string(data))), &r); err != nil {
		return phone.Result{}, errors.New("bridge returned an unreadable result")
	}
	return r, nil
}

// SetBridge connects the accessibility service; tasks need it.
func SetBridge(b DeviceBridge) {
	r, err := currentRunner()
	if err != nil {
		return
	}
	if b == nil {
		r.SetBridge(nil)
		return
	}
	r.SetBridge(bridgeAdapter{b})
}

// ClearBridge disconnects the accessibility service.
func ClearBridge() {
	if r, err := currentRunner(); err == nil {
		r.SetBridge(nil)
	}
}

// SetListener registers the app's state listener.
func SetListener(l Listener) {
	r, err := currentRunner()
	if err != nil {
		return
	}
	if l == nil {
		r.SetListener(nil)
		return
	}
	r.SetListener(func(s phone.State) { l.OnState(encode(s)) })
}

// StartTask starts a task from phone.TaskSpec JSON.
func StartTask(specJSON string) string {
	r, err := currentRunner()
	if err != nil {
		return encode(okReport{Error: err.Error()})
	}
	var spec phone.TaskSpec
	if err := json.Unmarshal([]byte(specJSON), &spec); err != nil {
		return encode(okReport{Error: "task is not valid JSON"})
	}
	id, err := r.Start(spec)
	if err != nil {
		return encode(okReport{Error: err.Error()})
	}
	return encode(okReport{OK: true, ID: id})
}

// StopTask stops the running task.
func StopTask() string {
	r, err := currentRunner()
	if err == nil {
		err = r.Stop()
	}
	if err != nil {
		return encode(okReport{Error: err.Error()})
	}
	return encode(okReport{OK: true})
}

// Decide answers the pending approval with "allow_once" or "deny".
func Decide(id, decision string) string {
	r, err := currentRunner()
	if err == nil {
		err = r.Decide(id, phone.Decision(decision))
	}
	if err != nil {
		return encode(okReport{Error: err.Error()})
	}
	return encode(okReport{OK: true})
}

// TaskState returns phone.State as JSON.
func TaskState() string {
	r, err := currentRunner()
	if err != nil {
		return encode(okReport{Error: err.Error()})
	}
	return encode(r.State())
}
