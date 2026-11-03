package goal

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Store interface {
	Create(ctx context.Context, input CreateInput) (Goal, error)
	Get(ctx context.Context, id string) (Goal, error)
	List(ctx context.Context, filter ListFilter) ([]Goal, error)
	Update(ctx context.Context, goal Goal) error
	AppendEvent(ctx context.Context, event Event) error
	ListEvents(ctx context.Context, goalID string, limit int) ([]Event, error)
}

type PlanStore interface {
	SavePlan(ctx context.Context, plan GoalPlan) error
	GetPlan(ctx context.Context, goalID string) (GoalPlan, bool, error)
	AppendEvidence(ctx context.Context, evidence GoalEvidence) error
	ListEvidence(ctx context.Context, goalID string, limit int) ([]GoalEvidence, error)
}

var ErrGoalLocked = errors.New("goal is already running")

const defaultLockStaleAfter = 6 * time.Hour

type GoalLocker interface {
	LockGoal(ctx context.Context, goalID string) (func(), error)
}

type goalLockMetadata struct {
	PID       int       `json:"pid"`
	Hostname  string    `json:"hostname,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type LocalStore struct {
	Root       string
	LegacyRoot string
	mu         sync.Mutex
}

func DefaultStore() *LocalStore {
	return &LocalStore{Root: DefaultRoot(), LegacyRoot: LegacyRoot()}
}

func NewLocalStore(root string) *LocalStore {
	if strings.TrimSpace(root) == "" {
		root = DefaultRoot()
	}
	return &LocalStore{Root: root}
}

func (s *LocalStore) Create(ctx context.Context, input CreateInput) (Goal, error) {
	_ = ctx
	goal, err := NewGoal(input)
	if err != nil {
		return Goal{}, err
	}
	if err := goal.Validate(); err != nil {
		return Goal{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	goals, err := s.listLocked()
	if err != nil {
		return Goal{}, err
	}
	goals = append(goals, goal)
	if err := s.writeGoalsLocked(goals); err != nil {
		return Goal{}, err
	}
	event, err := NewEvent(EventInput{
		GoalID:    goal.ID,
		Type:      EventGoalStarted,
		Message:   "goal started",
		SessionID: goal.SessionID,
		Status:    goal.Status,
		Now:       goal.CreatedAt,
	})
	if err != nil {
		return Goal{}, err
	}
	if err := s.appendEventLocked(event); err != nil {
		return Goal{}, err
	}
	return goal, nil
}

func (s *LocalStore) Get(ctx context.Context, id string) (Goal, error) {
	_ = ctx
	id = strings.TrimSpace(id)
	if id == "" {
		return Goal{}, errors.New("goal id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	goals, err := s.listLocked()
	if err != nil {
		return Goal{}, err
	}
	for _, goal := range goals {
		if goal.ID == id {
			return goal, nil
		}
	}
	return Goal{}, fmt.Errorf("goal not found: %s", id)
}

func (s *LocalStore) List(ctx context.Context, filter ListFilter) ([]Goal, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	goals, err := s.listLocked()
	if err != nil {
		return nil, err
	}
	out := make([]Goal, 0, len(goals))
	for _, goal := range goals {
		if filter.Active && goal.Status != StatusActive {
			continue
		}
		if filter.Status != "" && goal.Status != filter.Status {
			continue
		}
		out = append(out, goal)
	}
	sortGoals(out)
	return out, nil
}

func (s *LocalStore) Update(ctx context.Context, goal Goal) error {
	_ = ctx
	if err := goal.Validate(); err != nil {
		return err
	}
	goal.UpdatedAt = goal.UpdatedAt.UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	goals, err := s.listLocked()
	if err != nil {
		return err
	}
	for i := range goals {
		if goals[i].ID != goal.ID {
			continue
		}
		goals[i] = goal
		return s.writeGoalsLocked(goals)
	}
	return fmt.Errorf("goal not found: %s", goal.ID)
}

func (s *LocalStore) AppendEvent(ctx context.Context, event Event) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.appendEventLocked(event)
}

func (s *LocalStore) ListEvents(ctx context.Context, goalID string, limit int) ([]Event, error) {
	_ = ctx
	goalID = strings.TrimSpace(goalID)
	if goalID == "" {
		return nil, errors.New("goal id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	events, err := s.readEvents(goalID)
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(events) > limit {
		events = events[len(events)-limit:]
	}
	return events, nil
}

func (s *LocalStore) SavePlan(ctx context.Context, plan GoalPlan) error {
	_ = ctx
	if err := plan.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writePlanLocked(plan)
}

func (s *LocalStore) GetPlan(ctx context.Context, goalID string) (GoalPlan, bool, error) {
	_ = ctx
	goalID = strings.TrimSpace(goalID)
	if goalID == "" {
		return GoalPlan{}, false, errors.New("goal id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	plan, ok, err := readPlan(s.planPath(goalID))
	if err != nil || ok || strings.TrimSpace(s.LegacyRoot) == "" {
		return plan, ok, err
	}
	return readPlan(s.legacyPlanPath(goalID))
}

func (s *LocalStore) AppendEvidence(ctx context.Context, evidence GoalEvidence) error {
	_ = ctx
	if err := evidence.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.appendEvidenceLocked(evidence)
}

func (s *LocalStore) ListEvidence(ctx context.Context, goalID string, limit int) ([]GoalEvidence, error) {
	_ = ctx
	goalID = strings.TrimSpace(goalID)
	if goalID == "" {
		return nil, errors.New("goal id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	evidence, err := s.readEvidence(goalID)
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(evidence) > limit {
		evidence = evidence[len(evidence)-limit:]
	}
	return evidence, nil
}

func (s *LocalStore) LockGoal(ctx context.Context, goalID string) (func(), error) {
	goalID = strings.TrimSpace(goalID)
	if goalID == "" {
		return nil, errors.New("goal id is required")
	}
	path := s.lockPath(goalID)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	for {
		err := os.Mkdir(path, 0700)
		if err == nil {
			_ = s.writeLockMetadata(path)
			unlocked := false
			return func() {
				if unlocked {
					return
				}
				unlocked = true
				_ = os.RemoveAll(path)
			}, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		if s.lockIsStale(path, time.Now().UTC()) {
			_ = os.RemoveAll(path)
			continue
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			return nil, ErrGoalLocked
		}
	}
}

func (s *LocalStore) ForceUnlockGoal(goalID string) error {
	goalID = strings.TrimSpace(goalID)
	if goalID == "" {
		return errors.New("goal id is required")
	}
	return os.RemoveAll(s.lockPath(goalID))
}

func (s *LocalStore) writeLockMetadata(path string) error {
	host, _ := os.Hostname()
	meta := goalLockMetadata{PID: os.Getpid(), Hostname: host, CreatedAt: time.Now().UTC()}
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(path, "meta.json"), data, 0600)
}

func (s *LocalStore) lockIsStale(path string, now time.Time) bool {
	data, err := os.ReadFile(filepath.Join(path, "meta.json"))
	if err != nil {
		info, statErr := os.Stat(path)
		return statErr == nil && now.Sub(info.ModTime()) > defaultLockStaleAfter
	}
	var meta goalLockMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return true
	}
	if meta.CreatedAt.IsZero() || now.Sub(meta.CreatedAt) > defaultLockStaleAfter {
		return true
	}
	if meta.PID <= 0 {
		return true
	}
	return processIsGone(meta.PID)
}

func processIsGone(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return true
	}
	if err := process.Signal(syscall.Signal(0)); err != nil {
		return true
	}
	return false
}

func (s *LocalStore) listLocked() ([]Goal, error) {
	byID := make(map[string]Goal)
	if strings.TrimSpace(s.LegacyRoot) != "" && filepath.Clean(s.LegacyRoot) != filepath.Clean(s.Root) {
		legacyGoals, err := readGoals(s.legacyGoalsPath())
		if err != nil {
			return nil, err
		}
		for _, goal := range legacyGoals {
			byID[goal.ID] = goal
		}
	}
	goals, err := readGoals(s.goalsPath())
	if err != nil {
		return nil, err
	}
	for _, goal := range goals {
		byID[goal.ID] = goal
	}
	goals = goals[:0]
	for _, goal := range byID {
		goals = append(goals, goal)
	}
	sortGoals(goals)
	return goals, nil
}

func (s *LocalStore) readEvents(goalID string) ([]Event, error) {
	var events []Event
	if strings.TrimSpace(s.LegacyRoot) != "" && filepath.Clean(s.LegacyRoot) != filepath.Clean(s.Root) {
		legacy, err := readEvents(s.legacyEventsPath(goalID))
		if err != nil {
			return nil, err
		}
		events = append(events, legacy...)
	}
	current, err := readEvents(s.eventsPath(goalID))
	if err != nil {
		return nil, err
	}
	return append(events, current...), nil
}

func (s *LocalStore) readEvidence(goalID string) ([]GoalEvidence, error) {
	var evidence []GoalEvidence
	if strings.TrimSpace(s.LegacyRoot) != "" && filepath.Clean(s.LegacyRoot) != filepath.Clean(s.Root) {
		legacy, err := readEvidence(s.legacyEvidencePath(goalID))
		if err != nil {
			return nil, err
		}
		evidence = append(evidence, legacy...)
	}
	current, err := readEvidence(s.evidencePath(goalID))
	if err != nil {
		return nil, err
	}
	return append(evidence, current...), nil
}

func readGoals(path string) ([]Goal, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()
	var goals []Goal
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var goal Goal
		if err := json.Unmarshal([]byte(line), &goal); err != nil {
			return nil, err
		}
		goals = append(goals, goal)
	}
	return goals, scanner.Err()
}

func readEvents(path string) ([]Event, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()
	var events []Event
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, scanner.Err()
}

func readPlan(path string) (GoalPlan, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return GoalPlan{}, false, nil
		}
		return GoalPlan{}, false, err
	}
	var plan GoalPlan
	if err := json.Unmarshal(data, &plan); err != nil {
		return GoalPlan{}, false, err
	}
	if err := plan.Validate(); err != nil {
		return GoalPlan{}, false, err
	}
	return plan, true, nil
}

func readEvidence(path string) ([]GoalEvidence, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()
	var evidence []GoalEvidence
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var item GoalEvidence
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			return nil, err
		}
		if err := item.Validate(); err != nil {
			return nil, err
		}
		evidence = append(evidence, item)
	}
	return evidence, scanner.Err()
}

func (s *LocalStore) writeGoalsLocked(goals []Goal) error {
	if err := os.MkdirAll(filepath.Dir(s.goalsPath()), 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.goalsPath()), ".goals-*.jsonl")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	enc := json.NewEncoder(tmp)
	for _, goal := range goals {
		if err := enc.Encode(goal); err != nil {
			_ = tmp.Close()
			return err
		}
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, s.goalsPath()); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func (s *LocalStore) writePlanLocked(plan GoalPlan) error {
	path := s.planPath(plan.GoalID)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".plan-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(plan); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func (s *LocalStore) appendEventLocked(event Event) error {
	if strings.TrimSpace(event.ID) == "" {
		return errors.New("event id is required")
	}
	if strings.TrimSpace(event.GoalID) == "" {
		return errors.New("event goal id is required")
	}
	if event.Type == "" {
		return errors.New("event type is required")
	}
	if event.CreatedAt.IsZero() {
		return errors.New("event created_at is required")
	}
	path := s.eventsPath(event.GoalID)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	return json.NewEncoder(file).Encode(event)
}

func (s *LocalStore) appendEvidenceLocked(evidence GoalEvidence) error {
	path := s.evidencePath(evidence.GoalID)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	return json.NewEncoder(file).Encode(evidence)
}

func sortGoals(goals []Goal) {
	sort.Slice(goals, func(i, j int) bool {
		return goals[i].CreatedAt.After(goals[j].CreatedAt)
	})
}

func (s *LocalStore) goalsPath() string {
	return filepath.Join(s.Root, "goals", "goals.jsonl")
}

func (s *LocalStore) legacyGoalsPath() string {
	return filepath.Join(s.LegacyRoot, "goals", "goals.jsonl")
}

func (s *LocalStore) eventsPath(goalID string) string {
	name := "goal_" + strings.TrimPrefix(strings.TrimSpace(goalID), "goal_") + ".jsonl"
	return filepath.Join(s.Root, "goals", "events", name)
}

func (s *LocalStore) legacyEventsPath(goalID string) string {
	name := "goal_" + strings.TrimPrefix(strings.TrimSpace(goalID), "goal_") + ".jsonl"
	return filepath.Join(s.LegacyRoot, "goals", "events", name)
}

func (s *LocalStore) planPath(goalID string) string {
	name := "goal_" + strings.TrimPrefix(strings.TrimSpace(goalID), "goal_") + ".json"
	return filepath.Join(s.Root, "goals", "plans", name)
}

func (s *LocalStore) legacyPlanPath(goalID string) string {
	name := "goal_" + strings.TrimPrefix(strings.TrimSpace(goalID), "goal_") + ".json"
	return filepath.Join(s.LegacyRoot, "goals", "plans", name)
}

func (s *LocalStore) evidencePath(goalID string) string {
	name := "goal_" + strings.TrimPrefix(strings.TrimSpace(goalID), "goal_") + ".jsonl"
	return filepath.Join(s.Root, "goals", "evidence", name)
}

func (s *LocalStore) legacyEvidencePath(goalID string) string {
	name := "goal_" + strings.TrimPrefix(strings.TrimSpace(goalID), "goal_") + ".jsonl"
	return filepath.Join(s.LegacyRoot, "goals", "evidence", name)
}

func (s *LocalStore) lockPath(goalID string) string {
	name := "goal_" + strings.TrimPrefix(strings.TrimSpace(goalID), "goal_") + ".lock"
	return filepath.Join(s.Root, "goals", "locks", name)
}
