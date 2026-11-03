package background

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"

	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/defaults"
)

type Options struct {
	Prompt          string
	CWD             string
	Kind            string
	IntervalSeconds int
	Model           string
	Provider        string
	GoalEvaluator   string
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
	RuntimeProfile  string
	PromptMode      string
	Agent           string
	ToolsSpecified  bool
	EnabledTools    []string
	SettingsInputs  []string
	MCPConfigInputs []string
	StrictMCPConfig bool
}

type Job struct {
	ID              string     `json:"id"`
	Prompt          string     `json:"prompt"`
	CWD             string     `json:"cwd"`
	Kind            string     `json:"kind,omitempty"`
	IntervalSeconds int        `json:"interval_seconds,omitempty"`
	RunCount        int        `json:"run_count,omitempty"`
	LastRunAt       *time.Time `json:"last_run_at,omitempty"`
	NextRunAt       *time.Time `json:"next_run_at,omitempty"`
	Model           string     `json:"model,omitempty"`
	Provider        string     `json:"provider,omitempty"`
	GoalEvaluator   string     `json:"goal_evaluator,omitempty"`
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
	RuntimeProfile  string     `json:"runtime_profile,omitempty"`
	PromptMode      string     `json:"prompt_mode,omitempty"`
	Agent           string     `json:"agent,omitempty"`
	ToolsSpecified  bool       `json:"tools_specified,omitempty"`
	EnabledTools    []string   `json:"enabled_tools,omitempty"`
	SettingsInputs  []string   `json:"settings_inputs,omitempty"`
	MCPConfigInputs []string   `json:"mcp_config_inputs,omitempty"`
	StrictMCPConfig bool       `json:"strict_mcp_config,omitempty"`
	Status          string     `json:"status"`
	PID             int        `json:"pid,omitempty"`
	ExitCode        int        `json:"exit_code,omitempty"`
	Error           string     `json:"error,omitempty"`
	LogPath         string     `json:"log_path,omitempty"`
	// OutputOffset is the persisted read cursor used by ReadNewOutput so a
	// polling reader gets only what was appended since its last read.
	OutputOffset int64      `json:"output_offset,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
}

type Store struct {
	Root string
}

func DefaultStore() Store {
	root, err := config.CurrentIdentity("").GlobalConfigRoot()
	if err != nil {
		root = "."
	}
	return Store{Root: root}
}

func (s Store) Create(prompt, cwd string) (Job, error) {
	return s.CreateWithOptions(Options{Prompt: prompt, CWD: cwd})
}

func (s Store) CreateWithOptions(opts Options) (Job, error) {
	id, err := newID()
	if err != nil {
		return Job{}, err
	}
	if opts.OutputFormat == "" {
		opts.OutputFormat = "text"
	}
	if opts.MaxTurns <= 0 {
		opts.MaxTurns = defaults.MaxTurns
	}
	now := time.Now().UTC()
	job := Job{
		ID:              id,
		Prompt:          opts.Prompt,
		CWD:             opts.CWD,
		Kind:            opts.Kind,
		IntervalSeconds: opts.IntervalSeconds,
		Model:           opts.Model,
		Provider:        opts.Provider,
		GoalEvaluator:   opts.GoalEvaluator,
		OutputFormat:    opts.OutputFormat,
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
		RuntimeProfile:  opts.RuntimeProfile,
		PromptMode:      opts.PromptMode,
		Agent:           opts.Agent,
		ToolsSpecified:  opts.ToolsSpecified,
		EnabledTools:    append([]string(nil), opts.EnabledTools...),
		SettingsInputs:  append([]string(nil), opts.SettingsInputs...),
		MCPConfigInputs: append([]string(nil), opts.MCPConfigInputs...),
		StrictMCPConfig: opts.StrictMCPConfig,
		Status:          "queued",
		LogPath:         s.logPath(id),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	err = s.withRegistryLock(func() error {
		jobs, err := s.List()
		if err != nil {
			return err
		}
		return s.save(append(jobs, job))
	})
	if err != nil {
		return Job{}, err
	}
	return job, nil
}

func (s Store) RecordLoopRun(id string, nextRunAt *time.Time) (Job, bool, error) {
	return s.update(id, func(job *Job) {
		if job.Status == "killed" {
			return
		}
		now := time.Now().UTC()
		job.RunCount++
		job.LastRunAt = &now
		job.NextRunAt = nextRunAt
		if job.Kind == "loop" {
			job.Status = "running"
			job.FinishedAt = nil
		} else if !terminalStatus(job.Status) {
			job.Status = "running"
		}
	})
}

func (s Store) UpdateLoopOptions(id string, opts Options) (Job, bool, error) {
	return s.update(id, func(job *Job) {
		if opts.Prompt != "" {
			job.Prompt = opts.Prompt
		}
		if opts.CWD != "" {
			job.CWD = opts.CWD
		}
		if opts.Kind != "" {
			job.Kind = opts.Kind
		}
		if opts.IntervalSeconds > 0 {
			job.IntervalSeconds = opts.IntervalSeconds
		}
		if opts.Model != "" {
			job.Model = opts.Model
		}
		if opts.Provider != "" {
			job.Provider = opts.Provider
		}
		if opts.OutputFormat != "" {
			job.OutputFormat = opts.OutputFormat
		}
		if opts.MaxTurns > 0 {
			job.MaxTurns = opts.MaxTurns
		}
		if opts.MaxTokens > 0 {
			job.MaxTokens = opts.MaxTokens
		}
	})
}

func (s Store) Find(id string) (Job, bool, error) {
	jobs, err := s.List()
	if err != nil {
		return Job{}, false, err
	}
	for _, job := range jobs {
		if job.ID == id {
			return job, true, nil
		}
	}
	return Job{}, false, nil
}

func (s Store) Start(id, executable string, args []string, env []string) (Job, error) {
	job, ok, err := s.Find(id)
	if err != nil {
		return Job{}, err
	}
	if !ok {
		return Job{}, fmt.Errorf("background session not found: %s", id)
	}
	if job.LogPath == "" {
		job.LogPath = s.logPath(id)
	}
	if err := os.MkdirAll(filepath.Dir(job.LogPath), 0755); err != nil {
		return Job{}, err
	}
	logFile, err := os.OpenFile(job.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return Job{}, err
	}
	defer logFile.Close()

	cmd := exec.Command(executable, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = env
	if job.CWD != "" {
		cmd.Dir = job.CWD
	}
	if err := cmd.Start(); err != nil {
		_, _, _ = s.Finish(id, 1, err.Error())
		return Job{}, err
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	updated, _, err := s.update(id, func(job *Job) {
		job.PID = pid
		job.LogPath = s.logPath(id)
		if !terminalStatus(job.Status) {
			now := time.Now().UTC()
			job.Status = "running"
			job.StartedAt = &now
		}
	})
	return updated, err
}

// List takes no lock. save replaces the registry with a rename, so a reader
// sees either the whole previous file or the whole new one; there is no torn
// state to be excluded from. Writers must go through withRegistryLock instead.
func (s Store) List() ([]Job, error) {
	data, err := os.ReadFile(s.path())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var jobs []Job
	if err := json.Unmarshal(data, &jobs); err != nil {
		return nil, err
	}
	sort.Slice(jobs, func(i, j int) bool {
		return jobs[i].CreatedAt.After(jobs[j].CreatedAt)
	})
	return jobs, nil
}

func (s Store) Kill(id string) (bool, error) {
	_, outcome, err := s.Terminate(id)
	if err != nil {
		return false, err
	}
	return outcome != TerminateNotFound, nil
}

func (s Store) Logs(id string) (string, bool, error) {
	jobs, err := s.List()
	if err != nil {
		return "", false, err
	}
	for _, job := range jobs {
		if job.ID != id {
			continue
		}
		if job.LogPath == "" {
			return "", true, nil
		}
		data, err := os.ReadFile(job.LogPath)
		if err != nil {
			if os.IsNotExist(err) {
				return "", true, nil
			}
			return "", true, err
		}
		return string(data), true, nil
	}
	return "", false, nil
}

func (s Store) MarkRunning(id string, pid int) (Job, bool, error) {
	return s.update(id, func(job *Job) {
		job.PID = pid
		if !terminalStatus(job.Status) {
			now := time.Now().UTC()
			job.Status = "running"
			job.StartedAt = &now
		}
	})
}

func (s Store) Finish(id string, exitCode int, errText string) (Job, bool, error) {
	return s.update(id, func(job *Job) {
		if job.Status == "killed" {
			return
		}
		now := time.Now().UTC()
		job.ExitCode = exitCode
		job.Error = errText
		job.FinishedAt = &now
		if job.Kind == "loop" {
			job.Status = "running"
			return
		}
		if exitCode == 0 {
			job.Status = "completed"
		} else {
			job.Status = "failed"
		}
	})
}

func (s Store) update(id string, mutate func(*Job)) (Job, bool, error) {
	var updated Job
	found := false
	err := s.withRegistryLock(func() error {
		jobs, err := s.List()
		if err != nil {
			return err
		}
		for i := range jobs {
			if jobs[i].ID != id {
				continue
			}
			mutate(&jobs[i])
			jobs[i].UpdatedAt = time.Now().UTC()
			found = true
			if err := s.save(jobs); err != nil {
				return err
			}
			updated = jobs[i]
			return nil
		}
		return nil
	})
	if err != nil {
		return Job{}, found, err
	}
	return updated, found, nil
}

// save replaces the registry in one step: the payload goes to a temp file in the
// same directory, is flushed, and is then renamed over the registry. rename is
// atomic within a filesystem, so a concurrent reader can only observe the whole
// old file or the whole new one.
//
// os.WriteFile cannot give that: its O_TRUNC empties the registry before the new
// bytes land, and a reader that hits the gap gets a truncated document. That was
// the "unexpected end of JSON input" of TODO-111.
func (s Store) save(jobs []Job) error {
	if err := os.MkdirAll(filepath.Dir(s.path()), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(jobs, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(s.path()), filepath.Base(s.path())+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	err = writeAndSync(tmp, data)
	if err == nil {
		err = os.Rename(name, s.path())
	}
	if err != nil {
		os.Remove(name)
	}
	return err
}

// writeAndSync writes data to file, forces it to disk, and closes it, so the
// rename that follows publishes bytes that are already durable.
func writeAndSync(file *os.File, data []byte) error {
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		return err
	}
	return file.Sync()
}

func (s Store) path() string {
	return filepath.Join(s.Root, "background_sessions.json")
}

func (s Store) logPath(id string) string {
	return filepath.Join(s.Root, "background", "logs", id+".log")
}

func terminalStatus(status string) bool {
	return status == "completed" || status == "failed" || status == "killed"
}

func newID() (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "bg_" + hex.EncodeToString(b[:]), nil
}
