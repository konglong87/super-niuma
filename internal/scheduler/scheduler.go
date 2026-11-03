package scheduler

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/konglong87/super-niuma/internal/background"
	"github.com/konglong87/super-niuma/internal/config"
)

type Options struct {
	Prompt          string
	CWD             string
	Kind            string
	Spec            string
	IntervalSeconds int
	Model           string
	Provider        string
	OutputFormat    string
	MaxTurns        int
	MaxTokens       int
	Resume          string
	SessionID       string
	SessionName     string
	NoPersistence   bool
	SystemPrompt    string
	AppendSystem    string
	AllowedTools    []string
	DeniedTools     []string
	PermissionMode  string
	AdditionalDirs  []string
	SkipPermissions bool
}

type Schedule struct {
	ID              string     `json:"id"`
	BackgroundID    string     `json:"background_id"`
	Prompt          string     `json:"prompt"`
	CWD             string     `json:"cwd"`
	Kind            string     `json:"kind,omitempty"`
	Spec            string     `json:"spec"`
	IntervalSeconds int        `json:"interval_seconds,omitempty"`
	Enabled         bool       `json:"enabled"`
	Model           string     `json:"model,omitempty"`
	Provider        string     `json:"provider,omitempty"`
	OutputFormat    string     `json:"output_format,omitempty"`
	MaxTurns        int        `json:"max_turns,omitempty"`
	MaxTokens       int        `json:"max_tokens,omitempty"`
	Resume          string     `json:"resume,omitempty"`
	SessionID       string     `json:"session_id,omitempty"`
	SessionName     string     `json:"session_name,omitempty"`
	NoPersistence   bool       `json:"no_session_persistence,omitempty"`
	SystemPrompt    string     `json:"system_prompt,omitempty"`
	AppendSystem    string     `json:"append_system,omitempty"`
	AllowedTools    []string   `json:"allowed_tools,omitempty"`
	DeniedTools     []string   `json:"denied_tools,omitempty"`
	PermissionMode  string     `json:"permission_mode,omitempty"`
	AdditionalDirs  []string   `json:"additional_dirs,omitempty"`
	SkipPermissions bool       `json:"skip_permissions,omitempty"`
	RunCount        int        `json:"run_count,omitempty"`
	LastRunAt       *time.Time `json:"last_run_at,omitempty"`
	NextRunAt       *time.Time `json:"next_run_at,omitempty"`
	LastError       string     `json:"last_error,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type Event struct {
	Offset       int64     `json:"offset,omitempty"`
	ID           string    `json:"id"`
	Type         string    `json:"type"`
	ScheduleID   string    `json:"schedule_id,omitempty"`
	BackgroundID string    `json:"background_id,omitempty"`
	Prompt       string    `json:"prompt,omitempty"`
	CWD          string    `json:"cwd,omitempty"`
	Status       string    `json:"status,omitempty"`
	RunCount     int       `json:"run_count,omitempty"`
	Error        string    `json:"error,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type RunRecord struct {
	ID           string     `json:"id"`
	ScheduleID   string     `json:"schedule_id"`
	BackgroundID string     `json:"background_id"`
	Prompt       string     `json:"prompt"`
	CWD          string     `json:"cwd"`
	Status       string     `json:"status"`
	Error        string     `json:"error,omitempty"`
	StartedAt    time.Time  `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
	LogBytes     int64      `json:"log_bytes,omitempty"`
}

type Store struct {
	Root string
	mu   sync.Mutex
}

type daemonMeta struct {
	PID                    int       `json:"pid"`
	Executable             string    `json:"executable"`
	ExecutableSize         int64     `json:"executable_size,omitempty"`
	ExecutableMTime        time.Time `json:"executable_mtime,omitempty"`
	EnvironmentFingerprint string    `json:"environment_fingerprint"`
	Root                   string    `json:"root"`
	StartedAt              time.Time `json:"started_at"`
}

var (
	// ErrDaemonDisabled lets callers fail closed instead of treating a disabled
	// scheduler as a successful no-op. Session monitors must not be audited as
	// accepted when their durable executor is unavailable.
	ErrDaemonDisabled = errors.New("scheduler daemon is disabled")
	// ErrDaemonEnvironmentMismatch means a live daemon cannot be safely reused
	// or restarted because its recorded execution capabilities no longer match.
	ErrDaemonEnvironmentMismatch = errors.New("scheduler daemon environment mismatch")
)

var startSchedulerDaemon = func(executable string, env []string, output io.Writer) (int, error) {
	cmd := exec.Command(executable, "__schedule-daemon")
	cmd.Stdout = output
	cmd.Stderr = output
	cmd.Env = append(env, "NIUMA_SCHEDULER_DAEMON=1")
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	return pid, nil
}

var stopSchedulerDaemon = func(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Kill()
}

var isSchedulerDaemonProcess = schedulerDaemonProcessMatches

func DefaultStore() Store {
	return Store{Root: configRoot()}
}

func configRoot() string {
	root, err := config.CurrentIdentity("").GlobalConfigRoot()
	if err == nil && root != "" {
		return root
	}
	return "."
}

func (s *Store) Create(opts Options, bg background.Job) (Schedule, error) {
	if opts.Spec == "" {
		return Schedule{}, errors.New("schedule spec is required")
	}
	if err := validateScheduleSpec(opts.Spec); err != nil {
		return Schedule{}, err
	}
	id, err := newID()
	if err != nil {
		return Schedule{}, err
	}
	now := time.Now().UTC()
	schedule := Schedule{
		ID:              id,
		BackgroundID:    bg.ID,
		Prompt:          opts.Prompt,
		CWD:             opts.CWD,
		Kind:            firstNonEmpty(opts.Kind, "loop"),
		Spec:            opts.Spec,
		IntervalSeconds: opts.IntervalSeconds,
		Enabled:         true,
		Model:           opts.Model,
		Provider:        opts.Provider,
		OutputFormat:    firstNonEmpty(opts.OutputFormat, "text"),
		MaxTurns:        opts.MaxTurns,
		MaxTokens:       opts.MaxTokens,
		Resume:          opts.Resume,
		SessionID:       opts.SessionID,
		SessionName:     opts.SessionName,
		NoPersistence:   opts.NoPersistence,
		SystemPrompt:    opts.SystemPrompt,
		AppendSystem:    opts.AppendSystem,
		AllowedTools:    append([]string(nil), opts.AllowedTools...),
		DeniedTools:     append([]string(nil), opts.DeniedTools...),
		PermissionMode:  opts.PermissionMode,
		AdditionalDirs:  append([]string(nil), opts.AdditionalDirs...),
		SkipPermissions: opts.SkipPermissions,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	err = s.withRegistryLock(func() error {
		items, listErr := s.listLocked()
		if listErr != nil {
			return listErr
		}
		return s.saveLocked(append(items, schedule))
	})
	if err != nil {
		return Schedule{}, err
	}
	_ = s.AppendEvent(Event{Type: "created", ScheduleID: schedule.ID, BackgroundID: schedule.BackgroundID, Prompt: schedule.Prompt, CWD: schedule.CWD, Status: "enabled"})
	return schedule, nil
}

// EnsureProjection creates or updates a schedule under a caller-derived stable
// ID. It is used when another durable store owns logical identity and this file
// store is only a repairable execution projection.
func (s *Store) EnsureProjection(id string, opts Options) (Schedule, error) {
	return s.EnsureProjectionFromAuthority(id, func() (Options, error) { return opts, nil })
}

// EnsureProjectionFromAuthority serializes the authority read with the file
// projection write. A stale caller therefore cannot overwrite a projection
// written from a newer authority value by another process sharing this store.
func (s *Store) EnsureProjectionFromAuthority(id string, load func() (Options, error)) (Schedule, error) {
	if strings.TrimSpace(id) == "" || load == nil {
		return Schedule{}, errors.New("schedule projection identity, kind, and spec are required")
	}
	var created Schedule
	wasUpdate := false
	err := s.withRegistryLock(func() error {
		opts, loadErr := load()
		if loadErr != nil {
			return loadErr
		}
		if strings.TrimSpace(opts.Spec) == "" || strings.TrimSpace(opts.Kind) == "" {
			return errors.New("schedule projection identity, kind, and spec are required")
		}
		if validateErr := validateScheduleSpec(opts.Spec); validateErr != nil {
			return validateErr
		}
		items, listErr := s.listLocked()
		if listErr != nil {
			return listErr
		}
		now := time.Now().UTC()
		for index := range items {
			if items[index].ID != id {
				continue
			}
			applyProjectionOptions(&items[index], opts)
			items[index].Enabled, items[index].UpdatedAt = true, now
			created, wasUpdate = items[index], true
			return s.saveLocked(items)
		}
		created = Schedule{ID: id, Enabled: true, CreatedAt: now, UpdatedAt: now}
		applyProjectionOptions(&created, opts)
		return s.saveLocked(append(items, created))
	})
	if err == nil {
		eventType := "created"
		if wasUpdate {
			eventType = "updated"
		}
		_ = s.AppendEvent(Event{Type: eventType, ScheduleID: created.ID, Status: "enabled"})
	}
	return created, err
}

func applyProjectionOptions(schedule *Schedule, opts Options) {
	schedule.Prompt, schedule.CWD, schedule.Kind, schedule.Spec = opts.Prompt, opts.CWD, opts.Kind, opts.Spec
	schedule.IntervalSeconds, schedule.OutputFormat = opts.IntervalSeconds, firstNonEmpty(opts.OutputFormat, "text")
	schedule.NoPersistence = opts.NoPersistence
	schedule.AllowedTools = append([]string(nil), opts.AllowedTools...)
	schedule.DeniedTools = append([]string(nil), opts.DeniedTools...)
}

func (s *Store) List() ([]Schedule, error) {
	return s.listLocked()
}

func (s *Store) listLocked() ([]Schedule, error) {
	data, err := os.ReadFile(s.path())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var schedules []Schedule
	if err := json.Unmarshal(data, &schedules); err != nil {
		return nil, err
	}
	sort.Slice(schedules, func(i, j int) bool {
		return schedules[i].CreatedAt.After(schedules[j].CreatedAt)
	})
	return schedules, nil
}

func (s *Store) Find(id string) (Schedule, bool, error) {
	items, err := s.List()
	if err != nil {
		return Schedule{}, false, err
	}
	for _, item := range items {
		if item.ID == id || item.BackgroundID == id {
			return item, true, nil
		}
	}
	return Schedule{}, false, nil
}

func (s *Store) Disable(id string) (Schedule, bool, error) {
	updated, ok, err := s.update(id, func(schedule *Schedule) {
		schedule.Enabled = false
	})
	if err == nil && ok {
		_ = s.AppendEvent(Event{Type: "disabled", ScheduleID: updated.ID, BackgroundID: updated.BackgroundID, Prompt: updated.Prompt, CWD: updated.CWD, Status: "disabled", RunCount: updated.RunCount})
	}
	return updated, ok, err
}

func (s *Store) UpdateLoop(id string, opts Options) (Schedule, bool, error) {
	if strings.TrimSpace(opts.Spec) != "" {
		if err := validateScheduleSpec(opts.Spec); err != nil {
			return Schedule{}, false, err
		}
	}
	updated, ok, err := s.update(id, func(schedule *Schedule) {
		if strings.TrimSpace(opts.Kind) != "" {
			schedule.Kind = opts.Kind
		}
		if strings.TrimSpace(opts.Prompt) != "" {
			schedule.Prompt = opts.Prompt
		}
		if strings.TrimSpace(opts.CWD) != "" {
			schedule.CWD = opts.CWD
		}
		if strings.TrimSpace(opts.Spec) != "" {
			schedule.Spec = opts.Spec
		}
		if opts.IntervalSeconds > 0 {
			schedule.IntervalSeconds = opts.IntervalSeconds
		}
		if strings.TrimSpace(opts.Model) != "" {
			schedule.Model = opts.Model
		}
		if strings.TrimSpace(opts.Provider) != "" {
			schedule.Provider = opts.Provider
		}
		if strings.TrimSpace(opts.OutputFormat) != "" {
			schedule.OutputFormat = opts.OutputFormat
		}
		if opts.MaxTurns > 0 {
			schedule.MaxTurns = opts.MaxTurns
		}
		if opts.MaxTokens > 0 {
			schedule.MaxTokens = opts.MaxTokens
		}
		if opts.AllowedTools != nil {
			schedule.AllowedTools = append([]string(nil), opts.AllowedTools...)
		}
		if opts.DeniedTools != nil {
			schedule.DeniedTools = append([]string(nil), opts.DeniedTools...)
		}
		if opts.NoPersistence {
			schedule.NoPersistence = true
		}
		schedule.Enabled = true
	})
	if err == nil && ok {
		_ = s.AppendEvent(Event{Type: "updated", ScheduleID: updated.ID, BackgroundID: updated.BackgroundID, Prompt: updated.Prompt, CWD: updated.CWD, Status: "enabled", RunCount: updated.RunCount})
	}
	return updated, ok, err
}

func (s *Store) RecordRun(id string, nextRunAt *time.Time, errText string) (Schedule, bool, error) {
	updated, ok, err := s.update(id, func(schedule *Schedule) {
		now := time.Now().UTC()
		schedule.RunCount++
		schedule.LastRunAt = &now
		schedule.NextRunAt = nextRunAt
		schedule.LastError = errText
	})
	if err == nil && ok {
		status := "completed"
		if strings.TrimSpace(errText) != "" {
			status = "failed"
		}
		_ = s.AppendEvent(Event{Type: "run_finished", ScheduleID: updated.ID, BackgroundID: updated.BackgroundID, Prompt: updated.Prompt, CWD: updated.CWD, Status: status, RunCount: updated.RunCount, Error: errText})
	}
	return updated, ok, err
}

func (s *Store) update(id string, mutate func(*Schedule)) (Schedule, bool, error) {
	var updated Schedule
	found := false
	err := s.withRegistryLock(func() error {
		items, listErr := s.listLocked()
		if listErr != nil {
			return listErr
		}
		for i := range items {
			if items[i].ID != id && items[i].BackgroundID != id {
				continue
			}
			mutate(&items[i])
			items[i].UpdatedAt = time.Now().UTC()
			updated, found = items[i], true
			return s.saveLocked(items)
		}
		return nil
	})
	return updated, found, err
}

func (s *Store) save(items []Schedule) error {
	return s.withRegistryLock(func() error { return s.saveLocked(items) })
}

func (s *Store) saveLocked(items []Schedule) error {
	if err := os.MkdirAll(filepath.Dir(s.path()), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(s.path()), filepath.Base(s.path())+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(name, s.path())
	}
	if err != nil {
		_ = os.Remove(name)
	}
	return err
}

func (s *Store) path() string {
	return filepath.Join(s.Root, "schedules.json")
}

func (s *Store) DaemonLogPath() string {
	return filepath.Join(s.Root, "scheduler", "daemon.log")
}

func (s *Store) DaemonPIDPath() string {
	return filepath.Join(s.Root, "scheduler", "daemon.pid")
}

func (s *Store) DaemonMetaPath() string {
	return filepath.Join(s.Root, "scheduler", "daemon.meta.json")
}

func (s *Store) EventsPath() string {
	return filepath.Join(s.Root, "scheduler", "events.jsonl")
}

func (s *Store) RunsPath() string {
	return filepath.Join(s.Root, "scheduler", "runs.jsonl")
}

func (s *Store) AppendEvent(event Event) error {
	if event.ID == "" {
		id, err := newEventID("evt")
		if err != nil {
			return err
		}
		event.ID = id
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	return appendJSONLine(s.EventsPath(), event)
}

func (s *Store) ReadEventsAfter(offset int64, limit int) ([]Event, int64, error) {
	if limit <= 0 {
		limit = 100
	}
	file, err := os.Open(s.EventsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, offset, err
	}
	defer file.Close()
	if offset < 0 {
		offset = 0
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return nil, offset, err
	}
	reader := bufio.NewReader(file)
	events := []Event{}
	for len(events) < limit {
		line, err := reader.ReadBytes('\n')
		if len(line) == 0 && err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return events, offset, err
		}
		if strings.TrimSpace(string(line)) == "" {
			if current, seekErr := file.Seek(0, io.SeekCurrent); seekErr == nil {
				offset = current - int64(reader.Buffered())
			}
			if err != nil {
				break
			}
			continue
		}
		var event Event
		if err := json.Unmarshal(line, &event); err != nil {
			return events, offset, err
		}
		if current, seekErr := file.Seek(0, io.SeekCurrent); seekErr == nil {
			offset = current - int64(reader.Buffered())
			event.Offset = offset
		}
		events = append(events, event)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return events, offset, err
		}
	}
	if current, err := file.Seek(0, io.SeekCurrent); err == nil {
		buffered := int64(reader.Buffered())
		if current >= buffered {
			offset = current - buffered
		}
	}
	return events, offset, nil
}

func (s *Store) EventsEndOffset() (int64, error) {
	info, err := os.Stat(s.EventsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	return info.Size(), nil
}

func appendJSONLine(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

func newEventID(prefix string) (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	if strings.TrimSpace(prefix) == "" {
		prefix = "evt"
	}
	return prefix + "_" + hex.EncodeToString(b[:]), nil
}

func (s *Store) AppendRun(record RunRecord) error {
	if record.ID == "" {
		id, err := newEventID("run")
		if err != nil {
			return err
		}
		record.ID = id
	}
	if record.StartedAt.IsZero() {
		record.StartedAt = time.Now().UTC()
	}
	return appendJSONLine(s.RunsPath(), record)
}

func (s *Store) ListRuns(id string, limit int) ([]RunRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	data, err := os.ReadFile(s.RunsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	out := []RunRecord{}
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		var record RunRecord
		if err := json.Unmarshal([]byte(lines[i]), &record); err != nil {
			return nil, err
		}
		if id == "" || record.ScheduleID == id || record.BackgroundID == id {
			out = append(out, record)
		}
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *Store) EnsureDaemon(executable string, env []string) (int, bool, error) {
	if executable == "" {
		return 0, false, errors.New("scheduler executable is required")
	}
	if schedulerDaemonDisabled(executable) {
		return 0, false, ErrDaemonDisabled
	}
	fingerprint := daemonEnvironmentFingerprint(env)
	var pid int
	started := false
	err := s.withDaemonLock(func() error {
		// Recheck after acquiring the cross-process lock. The earlier caller may
		// have started and published a compatible daemon while this caller waited.
		if reusablePID, ok, reuseErr := s.reusableDaemonPID(executable, fingerprint); reuseErr != nil {
			return reuseErr
		} else if ok {
			pid = reusablePID
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(s.DaemonLogPath()), 0755); err != nil {
			return err
		}
		logFile, err := os.OpenFile(s.DaemonLogPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return err
		}
		defer logFile.Close()
		pid, err = startSchedulerDaemon(executable, env, logFile)
		if err != nil {
			return err
		}
		started = true
		if err := os.WriteFile(s.DaemonPIDPath(), []byte(fmt.Sprintf("%d\n", pid)), 0600); err != nil {
			s.rollbackDaemonStart(pid)
			return err
		}
		meta := daemonMeta{PID: pid, Executable: executable, EnvironmentFingerprint: fingerprint, Root: s.Root, StartedAt: time.Now().UTC()}
		meta.ExecutableSize, meta.ExecutableMTime = executableFingerprint(executable)
		if err := s.writeDaemonMeta(meta); err != nil {
			s.rollbackDaemonStart(pid)
			return err
		}
		return nil
	})
	if err != nil {
		return pid, started, err
	}
	return pid, started, nil
}

func schedulerDaemonDisabled(executable string) bool {
	if strings.EqualFold(os.Getenv("NIUMA_DISABLE_SCHEDULER"), "1") {
		return true
	}
	return strings.HasSuffix(filepath.Base(executable), ".test")
}

func (s *Store) reusableDaemonPID(executable, environmentFingerprint string) (int, bool, error) {
	pid, ok := s.liveDaemonPID()
	if !ok {
		return 0, false, nil
	}
	meta, metaOK := s.readDaemonMeta()
	size, mtime := executableFingerprint(executable)
	if !isSchedulerDaemonProcess(pid, meta) {
		return 0, false, ErrDaemonEnvironmentMismatch
	}
	if metaOK && meta.PID == pid && meta.Executable == executable && meta.ExecutableSize == size && meta.ExecutableMTime.Equal(mtime) && meta.EnvironmentFingerprint == environmentFingerprint {
		return pid, true, nil
	}
	if pid == os.Getpid() {
		return 0, false, ErrDaemonEnvironmentMismatch
	}
	if err := stopSchedulerDaemon(pid); err != nil {
		return 0, false, fmt.Errorf("stop stale scheduler daemon %d: %w", pid, err)
	}
	_ = os.Remove(s.DaemonPIDPath())
	_ = os.Remove(s.DaemonMetaPath())
	return 0, false, nil
}

func (s *Store) rollbackDaemonStart(pid int) {
	_ = stopSchedulerDaemon(pid)
	_ = os.Remove(s.DaemonPIDPath())
	_ = os.Remove(s.DaemonMetaPath())
}

// daemonEnvironmentFingerprint records only a stable digest of capabilities
// needed by scheduler children. The meta file never contains raw DSNs, payload
// keys, credentials, or other environment values.
func daemonEnvironmentFingerprint(env []string) string {
	values := make(map[string]string, len(env))
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			values[key] = value
		}
	}
	keys := []string{
		"NIUMA_MYSQL_DSN", "MYSQL_DSN",
		"NIUMA_CHANNEL_TENANT_ID", "NIUMA_CHANNEL_ACCOUNT_ID", "NIUMA_CHANNEL_ACCOUNT_KEY",
		"NIUMA_CHANNEL_USER_ID", "NIUMA_CHANNEL_PAYLOAD_KEY", "NIUMA_FEISHU_CREDENTIAL_FILE",
		"NIUMA_CHANNEL_MODEL_PROVIDER", "NIUMA_CHANNEL_MODEL",
	}
	var material strings.Builder
	for _, key := range keys {
		material.WriteString(key)
		material.WriteByte('=')
		material.WriteString(values[key])
		material.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(material.String()))
	return hex.EncodeToString(sum[:])
}

func (s *Store) liveDaemonPID() (int, bool) {
	data, err := os.ReadFile(s.DaemonPIDPath())
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return 0, false
	}
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return 0, false
	}
	return pid, true
}

func (s *Store) readDaemonMeta() (daemonMeta, bool) {
	data, err := os.ReadFile(s.DaemonMetaPath())
	if err != nil {
		return daemonMeta{}, false
	}
	var meta daemonMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return daemonMeta{}, false
	}
	return meta, true
}

func (s *Store) writeDaemonMeta(meta daemonMeta) error {
	if err := os.MkdirAll(filepath.Dir(s.DaemonMetaPath()), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.DaemonMetaPath(), append(data, '\n'), 0600)
}

func executableFingerprint(path string) (int64, time.Time) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, time.Time{}
	}
	return info.Size(), info.ModTime().UTC()
}

type Executor interface {
	RunSchedule(ctx context.Context, schedule Schedule) error
}

type Runner struct {
	Store          *Store
	Executor       Executor
	MaxParallel    int
	ReloadInterval time.Duration
	LogWriter      io.Writer
}

func (r Runner) Run(ctx context.Context) error {
	store := r.Store
	if store == nil {
		defaultStore := DefaultStore()
		store = &defaultStore
	}
	executor := r.Executor
	if executor == nil {
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		executor = ChildProcessExecutor{Executable: executable}
	}
	maxParallel := r.MaxParallel
	if maxParallel <= 0 {
		maxParallel = 2
	}
	logWriter := r.LogWriter
	if logWriter == nil {
		logWriter = os.Stdout
	}
	logf := func(format string, args ...any) {
		fmt.Fprintf(logWriter, time.Now().Format(time.RFC3339)+" scheduler: "+format+"\n", args...)
	}
	logf("daemon started pid=%d root=%s max_parallel=%d", os.Getpid(), store.Root, maxParallel)
	sem := make(chan struct{}, maxParallel)
	var workers sync.WaitGroup
	c := cron.New(cron.WithParser(cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)))
	entries := map[string]cron.EntryID{}
	registeredSpecs := map[string]string{}
	registerSchedule := func(schedule Schedule) error {
		if !schedule.Enabled {
			return nil
		}
		if existing, exists := entries[schedule.ID]; exists {
			if registeredSpecs[schedule.ID] == schedule.Spec {
				return nil
			}
			c.Remove(existing)
			delete(entries, schedule.ID)
			delete(registeredSpecs, schedule.ID)
		}
		var entryID cron.EntryID
		var err error
		entryIDReady := make(chan cron.EntryID, 1)
		if entryID, err = c.AddFunc(schedule.Spec, func() {
			select {
			case sem <- struct{}{}:
			default:
				logf("schedule skipped id=%s background=%s reason=worker_pool_full", schedule.ID, schedule.BackgroundID)
				_, _, _ = store.RecordRun(schedule.ID, nil, "skipped: scheduler worker pool is full")
				return
			}
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer func() { <-sem }()
				logf("schedule tick id=%s background=%s spec=%s cwd=%s", schedule.ID, schedule.BackgroundID, schedule.Spec, schedule.CWD)
				started := time.Now().UTC()
				_ = store.AppendEvent(Event{Type: "run_started", ScheduleID: schedule.ID, BackgroundID: schedule.BackgroundID, Prompt: schedule.Prompt, CWD: schedule.CWD, Status: "running", RunCount: schedule.RunCount})
				runErr := executor.RunSchedule(ctx, schedule)
				finished := time.Now().UTC()
				errText := ""
				if runErr != nil {
					errText = runErr.Error()
					logf("schedule run failed id=%s background=%s error=%v", schedule.ID, schedule.BackgroundID, runErr)
				} else {
					logf("schedule run completed id=%s background=%s", schedule.ID, schedule.BackgroundID)
				}
				status := "completed"
				if errText != "" {
					status = "failed"
				}
				_ = store.AppendRun(RunRecord{
					ScheduleID:   schedule.ID,
					BackgroundID: schedule.BackgroundID,
					Prompt:       schedule.Prompt,
					CWD:          schedule.CWD,
					Status:       status,
					Error:        errText,
					StartedAt:    started,
					FinishedAt:   &finished,
					LogBytes:     backgroundLogSize(store.Root, schedule.BackgroundID),
				})
				stableEntryID := <-entryIDReady
				entryIDReady <- stableEntryID
				entry := c.Entry(stableEntryID)
				var next *time.Time
				if !entry.Next.IsZero() {
					next = &entry.Next
				}
				if schedule.BackgroundID != "" {
					if _, _, err := (background.Store{Root: store.Root}).RecordLoopRun(schedule.BackgroundID, next); err != nil {
						logf("background record failed id=%s background=%s error=%v", schedule.ID, schedule.BackgroundID, err)
					}
				}
				if _, _, err := store.RecordRun(schedule.ID, next, errText); err != nil {
					logf("schedule record failed id=%s background=%s error=%v", schedule.ID, schedule.BackgroundID, err)
				}
			}()
		}); err != nil {
			return fmt.Errorf("register schedule %s: %w", schedule.ID, err)
		}
		entryIDReady <- entryID
		entries[schedule.ID] = entryID
		registeredSpecs[schedule.ID] = schedule.Spec
		logf("schedule registered id=%s background=%s spec=%s", schedule.ID, schedule.BackgroundID, schedule.Spec)
		return nil
	}
	reconcile := func() error {
		// The daemon is long-lived, while /loop can add schedules from another
		// process. Reconcile keeps cron registrations aligned with the store.
		schedules, err := store.List()
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, schedule := range schedules {
			seen[schedule.ID] = true
			if !schedule.Enabled {
				if entryID, exists := entries[schedule.ID]; exists {
					c.Remove(entryID)
					delete(entries, schedule.ID)
					delete(registeredSpecs, schedule.ID)
					logf("schedule removed id=%s background=%s reason=disabled", schedule.ID, schedule.BackgroundID)
				}
				continue
			}
			if err := registerSchedule(schedule); err != nil {
				logf("schedule ignored id=%s reason=invalid_spec error=%v", schedule.ID, err)
				_ = store.AppendEvent(Event{Type: "registration_failed", ScheduleID: schedule.ID, BackgroundID: schedule.BackgroundID, Status: "failed", Error: err.Error()})
				continue
			}
		}
		for id, entryID := range entries {
			if !seen[id] {
				c.Remove(entryID)
				delete(entries, id)
				delete(registeredSpecs, id)
				logf("schedule removed id=%s reason=missing_from_store", id)
			}
		}
		return nil
	}
	if err := reconcile(); err != nil {
		logf("initial reconcile failed error=%v", err)
		return err
	}
	c.Start()
	defer func() {
		stopCtx := c.Stop()
		<-stopCtx.Done()
		workers.Wait()
	}()
	reloadInterval := r.ReloadInterval
	if reloadInterval <= 0 {
		reloadInterval = 5 * time.Second
	}
	ticker := time.NewTicker(reloadInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			logf("daemon stopping error=%v", ctx.Err())
			return ctx.Err()
		case <-ticker.C:
			if err := reconcile(); err != nil {
				logf("reconcile failed error=%v", err)
				return err
			}
		}
	}
}

type ChildProcessExecutor struct {
	Executable string
	Stdout     io.Writer
	Stderr     io.Writer
}

func (e ChildProcessExecutor) RunSchedule(ctx context.Context, schedule Schedule) error {
	if e.Executable == "" {
		return errors.New("scheduler executable is required")
	}
	cmd := exec.CommandContext(ctx, e.Executable, "__schedule-run", schedule.ID)
	if schedule.CWD != "" {
		cmd.Dir = schedule.CWD
	}
	cmd.Env = append(os.Environ(), "NIUMA_SCHEDULER_CHILD=1")
	var logFile *os.File
	if e.Stdout == nil || e.Stderr == nil {
		bg, ok, err := background.DefaultStore().Find(schedule.BackgroundID)
		if err == nil && ok && bg.LogPath != "" {
			if mkErr := os.MkdirAll(filepath.Dir(bg.LogPath), 0755); mkErr != nil {
				return mkErr
			}
			logFile, err = os.OpenFile(bg.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
			if err != nil {
				return err
			}
			defer logFile.Close()
		}
	}
	if e.Stdout != nil {
		cmd.Stdout = e.Stdout
	} else if logFile != nil {
		cmd.Stdout = logFile
	}
	if e.Stderr != nil {
		cmd.Stderr = e.Stderr
	} else if logFile != nil {
		cmd.Stderr = logFile
	}
	return cmd.Run()
}

func backgroundLogSize(root, backgroundID string) int64 {
	if strings.TrimSpace(backgroundID) == "" {
		return 0
	}
	bg, ok, err := (background.Store{Root: root}).Find(backgroundID)
	if err != nil || !ok || bg.LogPath == "" {
		return 0
	}
	info, err := os.Stat(bg.LogPath)
	if err != nil {
		return 0
	}
	return info.Size()
}

func newID() (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "sched_" + hex.EncodeToString(b[:]), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func validateScheduleSpec(spec string) error {
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	if _, err := parser.Parse(strings.TrimSpace(spec)); err != nil {
		return fmt.Errorf("invalid schedule spec: %w", err)
	}
	return nil
}
