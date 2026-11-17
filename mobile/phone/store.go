package phone

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Task statuses.
const (
	StatusRunning        = "running"
	StatusDone           = "done"
	StatusNeedsAttention = "needs_attention"
	StatusFailed         = "failed"
	StatusStopped        = "stopped"
	StatusInterrupted    = "interrupted"
)

// TaskView is a task as the app's screens show it.
type TaskView struct {
	ID           string    `json:"id"`
	Goal         string    `json:"goal"`
	Apps         []string  `json:"apps"`
	Expect       []string  `json:"expect,omitempty"`
	Status       string    `json:"status"`
	Code         string    `json:"code,omitempty"`
	Result       string    `json:"result,omitempty"`
	Verified     *bool     `json:"verified,omitempty"`
	InputTokens  int       `json:"input_tokens,omitempty"`
	OutputTokens int       `json:"output_tokens,omitempty"`
	StartedMS    int64     `json:"started_ms"`
	EndedMS      int64     `json:"ended_ms,omitempty"`
	Steps        []Step    `json:"steps,omitempty"`
	Pending      *Approval `json:"pending,omitempty"`
}

// Store keeps tasks and their steps. It never holds the model credential.
type Store struct{ db *sql.DB }

// OpenStore opens the task database at path. A task still marked running
// was cut off by the app stopping: it becomes interrupted and is never
// resumed or retried on its own.
func OpenStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite3", path+"?_busy_timeout=5000&_journal_mode=WAL")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS tasks (
			id TEXT PRIMARY KEY, goal TEXT NOT NULL, apps TEXT NOT NULL, expect TEXT NOT NULL,
			status TEXT NOT NULL, code TEXT NOT NULL DEFAULT '', result TEXT NOT NULL DEFAULT '',
			verified INTEGER, input_tokens INTEGER NOT NULL DEFAULT 0, output_tokens INTEGER NOT NULL DEFAULT 0,
			started_ms INTEGER NOT NULL, ended_ms INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE IF NOT EXISTS steps (
			task_id TEXT NOT NULL, seq INTEGER NOT NULL, data TEXT NOT NULL, PRIMARY KEY (task_id, seq))`,
		`UPDATE tasks SET status = 'interrupted', code = 'app_stopped', ended_ms = ` + itoa(time.Now().UnixMilli()) + ` WHERE status = 'running'`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			db.Close()
			return nil, err
		}
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Create(t TaskView) error {
	apps, _ := json.Marshal(t.Apps)
	expect, _ := json.Marshal(t.Expect)
	_, err := s.db.Exec(`INSERT INTO tasks (id, goal, apps, expect, status, started_ms) VALUES (?, ?, ?, ?, ?, ?)`,
		t.ID, t.Goal, string(apps), string(expect), StatusRunning, t.StartedMS)
	return err
}

func (s *Store) AddStep(taskID string, step Step) error {
	data, _ := json.Marshal(step)
	_, err := s.db.Exec(`INSERT OR REPLACE INTO steps (task_id, seq, data) VALUES (?, ?, ?)`, taskID, step.Seq, string(data))
	return err
}

// Finish records how a running task ended. A task already finished is left
// as it is.
func (s *Store) Finish(t TaskView) error {
	var verified any
	if t.Verified != nil {
		verified = *t.Verified
	}
	_, err := s.db.Exec(`UPDATE tasks SET status = ?, code = ?, result = ?, verified = ?, input_tokens = ?, output_tokens = ?, ended_ms = ?
		WHERE id = ? AND status = 'running'`, t.Status, t.Code, t.Result, verified, t.InputTokens, t.OutputTokens, t.EndedMS, t.ID)
	return err
}

var ErrNoTask = errors.New("no such task")

// Get returns a task with its steps.
func (s *Store) Get(id string) (TaskView, error) {
	rows, err := s.query(`WHERE id = ?`, id)
	if err != nil {
		return TaskView{}, err
	}
	if len(rows) == 0 {
		return TaskView{}, ErrNoTask
	}
	t := rows[0]
	stepRows, err := s.db.Query(`SELECT data FROM steps WHERE task_id = ? ORDER BY seq`, id)
	if err != nil {
		return TaskView{}, err
	}
	defer stepRows.Close()
	for stepRows.Next() {
		var data string
		var st Step
		if stepRows.Scan(&data) == nil && json.Unmarshal([]byte(data), &st) == nil {
			t.Steps = append(t.Steps, st)
		}
	}
	return t, stepRows.Err()
}

// Recent returns the newest tasks without their steps.
func (s *Store) Recent(limit int) ([]TaskView, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	return s.query(`ORDER BY started_ms DESC LIMIT ?`, limit)
}

func (s *Store) query(tail string, args ...any) ([]TaskView, error) {
	rows, err := s.db.Query(`SELECT id, goal, apps, expect, status, code, result, verified, input_tokens, output_tokens, started_ms, ended_ms FROM tasks `+tail, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TaskView
	for rows.Next() {
		var t TaskView
		var apps, expect string
		var verified sql.NullBool
		if err := rows.Scan(&t.ID, &t.Goal, &apps, &expect, &t.Status, &t.Code, &t.Result, &verified, &t.InputTokens, &t.OutputTokens, &t.StartedMS, &t.EndedMS); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(apps), &t.Apps)
		_ = json.Unmarshal([]byte(expect), &t.Expect)
		if verified.Valid {
			v := verified.Bool
			t.Verified = &v
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func itoa(n int64) string {
	data, _ := json.Marshal(n)
	return string(data)
}
