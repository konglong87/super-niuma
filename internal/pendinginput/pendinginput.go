package pendinginput

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/konglong87/super-niuma/internal/agenttasks"
)

const (
	DefaultMaxItems       = 20
	DefaultMaxContentSize = 32 * 1024
)

var (
	ErrInvalid       = errors.New("invalid pending input")
	ErrNotFound      = errors.New("pending input not found")
	ErrConflict      = errors.New("pending input state conflict")
	ErrQueueDisabled = errors.New("pending input queue is disabled")
	ErrQueueFull     = errors.New("pending input queue is full")
)

type Status string

const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusSent      Status = "sent"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

type Scope struct {
	TenantID   uint64
	UserID     uint64
	SessionID  string
	BaseTaskID uint64
}

func (s Scope) valid() bool {
	return s.TenantID > 0 && s.UserID > 0 && strings.TrimSpace(s.SessionID) != ""
}

func (s Scope) matches(input PendingInput) bool {
	return s.TenantID == input.TenantID && s.UserID == input.UserID &&
		strings.TrimSpace(s.SessionID) == strings.TrimSpace(input.SessionID) &&
		(s.BaseTaskID == 0 || s.BaseTaskID == input.BaseTaskID)
}

type PendingInput struct {
	ID               string                  `json:"id"`
	TenantID         uint64                  `json:"tenant_id"`
	UserID           uint64                  `json:"user_id"`
	SessionID        string                  `json:"session_id"`
	BaseTaskID       uint64                  `json:"base_task_id"`
	ClientInputID    string                  `json:"client_input_id"`
	Content          string                  `json:"content"`
	Direction        string                  `json:"direction,omitempty"`
	Attempt          int                     `json:"attempt"`
	Attachments      []agenttasks.Attachment `json:"attachments,omitempty"`
	Sequence         int64                   `json:"sequence"`
	Status           Status                  `json:"status"`
	DispatchedTaskID uint64                  `json:"dispatched_task_id,omitempty"`
	ErrorCode        string                  `json:"error_code,omitempty"`
	ErrorMessage     string                  `json:"error_message,omitempty"`
	ClaimedAt        time.Time               `json:"claimed_at,omitempty"`
	CreatedAt        time.Time               `json:"created_at"`
	UpdatedAt        time.Time               `json:"updated_at"`
}

type NewInput struct {
	Scope         Scope
	ClientInputID string
	Content       string
	Direction     string
	Attachments   []agenttasks.Attachment
	// GlobalClientInputID reserves ClientInputID across all sessions for this
	// tenant/user. Session Control uses it because its key hash is intentionally
	// operation-scoped rather than target-scoped.
	GlobalClientInputID bool
}

type UpdateInput struct {
	Content   *string
	Direction *string
}

type Queue interface {
	Add(ctx context.Context, input NewInput) (PendingInput, error)
	List(ctx context.Context, scope Scope) ([]PendingInput, error)
	Update(ctx context.Context, id string, patch UpdateInput) (PendingInput, error)
	Cancel(ctx context.Context, id string) error
	MoveUp(ctx context.Context, id string) (PendingInput, error)
	ClaimNext(ctx context.Context, scope Scope) (PendingInput, bool, error)
	MarkSent(ctx context.Context, id string, taskID uint64) error
	MarkFailed(ctx context.Context, id string, code string) error
	Retry(ctx context.Context, id string) (PendingInput, error)
	QueueEnabled(ctx context.Context, scope Scope) (bool, error)
	SetQueueEnabled(ctx context.Context, scope Scope, enabled bool) error
}

// ConsumerLeaseQueue serializes queue consumption per tenant/user/session.
// Implementations must reject acquisition while the queue is disabled and
// allow another consumer to recover after the lease expires.
type ConsumerLeaseQueue interface {
	AcquireConsumerLease(ctx context.Context, scope Scope, owner string, ttl time.Duration) (bool, error)
	ReleaseConsumerLease(ctx context.Context, scope Scope, owner string) error
}

// ClientInputLookup reads every status, including sent/cancelled terminal
// rows, without narrowing to one session or base task.
type ClientInputLookup interface {
	FindByClientInputID(context.Context, uint64, uint64, string) (PendingInput, bool, error)
}

type ActiveSessionState struct {
	Count      int
	BaseTaskID uint64
}

type ActiveSessionStateLookup interface {
	ListActiveSessionStates(context.Context, uint64, uint64, []string) (map[string]ActiveSessionState, error)
}

type consumerLease struct {
	owner     string
	expiresAt time.Time
}

type MemoryQueue struct {
	mu       sync.Mutex
	items    map[string]PendingInput
	settings map[string]bool
	leases   map[string]consumerLease
	sequence int64
	id       uint64
	maxItems int
}

func NewMemoryQueue() *MemoryQueue {
	return &MemoryQueue{items: make(map[string]PendingInput), settings: make(map[string]bool), leases: make(map[string]consumerLease), maxItems: DefaultMaxItems}
}

func (q *MemoryQueue) Add(ctx context.Context, input NewInput) (PendingInput, error) {
	if err := contextErr(ctx); err != nil {
		return PendingInput{}, err
	}
	if err := validateNewInput(input); err != nil {
		return PendingInput{}, err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.enabledLocked(input.Scope) {
		return PendingInput{}, ErrQueueDisabled
	}
	for _, item := range q.items {
		sameClient := item.TenantID == input.Scope.TenantID && item.UserID == input.Scope.UserID && item.ClientInputID == strings.TrimSpace(input.ClientInputID)
		if sameClient && (input.GlobalClientInputID || input.Scope.matches(item)) {
			return cloneInput(item), nil
		}
	}
	if q.countActiveLocked(input.Scope) >= q.maxItems {
		return PendingInput{}, ErrQueueFull
	}
	now := time.Now().UTC()
	item := PendingInput{
		ID:            fmt.Sprintf("pi-%d", atomic.AddUint64(&q.id, 1)),
		TenantID:      input.Scope.TenantID,
		UserID:        input.Scope.UserID,
		SessionID:     strings.TrimSpace(input.Scope.SessionID),
		BaseTaskID:    input.Scope.BaseTaskID,
		ClientInputID: strings.TrimSpace(input.ClientInputID),
		Content:       input.Content,
		Direction:     input.Direction,
		Attachments:   cloneAttachments(input.Attachments),
		Sequence:      q.nextSequenceLocked(),
		Status:        StatusQueued,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	q.items[item.ID] = item
	return cloneInput(item), nil
}

func (q *MemoryQueue) FindByClientInputID(ctx context.Context, tenantID, userID uint64, clientInputID string) (PendingInput, bool, error) {
	if err := contextErr(ctx); err != nil {
		return PendingInput{}, false, err
	}
	clientInputID = strings.TrimSpace(clientInputID)
	if tenantID == 0 || userID == 0 || clientInputID == "" {
		return PendingInput{}, false, ErrInvalid
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	var found PendingInput
	for _, item := range q.items {
		if item.TenantID != tenantID || item.UserID != userID || item.ClientInputID != clientInputID {
			continue
		}
		if found.ID != "" && found.ID != item.ID {
			return PendingInput{}, false, ErrConflict
		}
		found = item
	}
	if found.ID == "" {
		return PendingInput{}, false, nil
	}
	return cloneInput(found), true, nil
}

func (q *MemoryQueue) ListActiveSessionStates(ctx context.Context, tenantID, userID uint64, sessionIDs []string) (map[string]ActiveSessionState, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	if tenantID == 0 || userID == 0 || len(sessionIDs) == 0 {
		return nil, ErrInvalid
	}
	wanted := make(map[string]struct{}, len(sessionIDs))
	for _, sessionID := range sessionIDs {
		sessionID = strings.TrimSpace(sessionID)
		if sessionID == "" {
			return nil, ErrInvalid
		}
		wanted[sessionID] = struct{}{}
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	states := make(map[string]ActiveSessionState)
	for _, item := range q.items {
		if item.TenantID != tenantID || item.UserID != userID || item.Status == StatusSent || item.Status == StatusCancelled {
			continue
		}
		if _, ok := wanted[item.SessionID]; !ok {
			continue
		}
		state := states[item.SessionID]
		if state.BaseTaskID != 0 && state.BaseTaskID != item.BaseTaskID {
			return nil, ErrConflict
		}
		state.Count++
		state.BaseTaskID = item.BaseTaskID
		states[item.SessionID] = state
	}
	return states, nil
}

func (q *MemoryQueue) List(ctx context.Context, scope Scope) ([]PendingInput, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	if !scope.valid() {
		return nil, ErrInvalid
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]PendingInput, 0)
	for _, item := range q.items {
		if scope.matches(item) && item.Status != StatusCancelled && item.Status != StatusSent {
			out = append(out, cloneInput(item))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Sequence < out[j].Sequence })
	return out, nil
}

func (q *MemoryQueue) Update(ctx context.Context, id string, patch UpdateInput) (PendingInput, error) {
	if err := contextErr(ctx); err != nil {
		return PendingInput{}, err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	item, ok := q.items[strings.TrimSpace(id)]
	if !ok {
		return PendingInput{}, ErrNotFound
	}
	if item.Status != StatusQueued && item.Status != StatusFailed {
		return PendingInput{}, ErrConflict
	}
	if patch.Content != nil {
		if strings.TrimSpace(*patch.Content) == "" || len([]byte(*patch.Content)) > DefaultMaxContentSize {
			return PendingInput{}, ErrInvalid
		}
		item.Content = *patch.Content
	}
	if patch.Direction != nil {
		if len([]byte(*patch.Direction)) > DefaultMaxContentSize {
			return PendingInput{}, ErrInvalid
		}
		item.Direction = *patch.Direction
	}
	item.UpdatedAt = time.Now().UTC()
	q.items[item.ID] = item
	return cloneInput(item), nil
}

func (q *MemoryQueue) Cancel(ctx context.Context, id string) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	item, ok := q.items[strings.TrimSpace(id)]
	if !ok {
		return ErrNotFound
	}
	if item.Status == StatusSent || item.Status == StatusCancelled || item.Status == StatusRunning {
		return ErrConflict
	}
	item.Status = StatusCancelled
	item.UpdatedAt = time.Now().UTC()
	q.items[item.ID] = item
	return nil
}

func (q *MemoryQueue) MoveUp(ctx context.Context, id string) (PendingInput, error) {
	if err := contextErr(ctx); err != nil {
		return PendingInput{}, err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	item, ok := q.items[strings.TrimSpace(id)]
	if !ok {
		return PendingInput{}, ErrNotFound
	}
	if item.Status != StatusQueued {
		return PendingInput{}, ErrConflict
	}
	var previous *PendingInput
	for candidate := range q.items {
		other := q.items[candidate]
		if other.TenantID == item.TenantID && other.UserID == item.UserID && other.SessionID == item.SessionID && other.Status == StatusQueued && other.Sequence < item.Sequence && (previous == nil || other.Sequence > previous.Sequence) {
			copy := other
			previous = &copy
		}
	}
	if previous == nil {
		return cloneInput(item), nil
	}
	item.Sequence, previous.Sequence = previous.Sequence, item.Sequence
	now := time.Now().UTC()
	item.UpdatedAt, previous.UpdatedAt = now, now
	q.items[item.ID] = item
	q.items[previous.ID] = *previous
	return cloneInput(item), nil
}

func (q *MemoryQueue) ClaimNext(ctx context.Context, scope Scope) (PendingInput, bool, error) {
	if err := contextErr(ctx); err != nil {
		return PendingInput{}, false, err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if !scope.valid() {
		return PendingInput{}, false, ErrInvalid
	}
	if !q.enabledLocked(scope) {
		return PendingInput{}, false, nil
	}
	var next *PendingInput
	for id, item := range q.items {
		if scope.matches(item) && item.Status == StatusQueued && (next == nil || item.Sequence < next.Sequence) {
			copy := item
			next = &copy
			_ = id
		}
	}
	if next == nil {
		return PendingInput{}, false, nil
	}
	next.Status = StatusRunning
	next.ClaimedAt = time.Now().UTC()
	next.UpdatedAt = next.ClaimedAt
	q.items[next.ID] = *next
	return cloneInput(*next), true, nil
}

func (q *MemoryQueue) MarkSent(ctx context.Context, id string, taskID uint64) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	item, ok := q.items[strings.TrimSpace(id)]
	if !ok {
		return ErrNotFound
	}
	if item.Status != StatusRunning {
		return ErrConflict
	}
	item.Status, item.DispatchedTaskID, item.UpdatedAt = StatusSent, taskID, time.Now().UTC()
	q.items[item.ID] = item
	return nil
}

func (q *MemoryQueue) MarkFailed(ctx context.Context, id string, code string) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	item, ok := q.items[strings.TrimSpace(id)]
	if !ok {
		return ErrNotFound
	}
	if item.Status != StatusRunning {
		return ErrConflict
	}
	item.Status, item.ErrorCode, item.UpdatedAt = StatusFailed, strings.TrimSpace(code), time.Now().UTC()
	q.items[item.ID] = item
	return nil
}

func (q *MemoryQueue) Retry(ctx context.Context, id string) (PendingInput, error) {
	if err := contextErr(ctx); err != nil {
		return PendingInput{}, err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	item, ok := q.items[strings.TrimSpace(id)]
	if !ok {
		return PendingInput{}, ErrNotFound
	}
	if item.Status != StatusFailed {
		return PendingInput{}, ErrConflict
	}
	item.Status, item.Attempt, item.ErrorCode, item.ErrorMessage = StatusQueued, item.Attempt+1, "", ""
	item.Sequence, item.UpdatedAt = q.nextSequenceLocked(), time.Now().UTC()
	q.items[item.ID] = item
	return cloneInput(item), nil
}

func (q *MemoryQueue) QueueEnabled(ctx context.Context, scope Scope) (bool, error) {
	if err := contextErr(ctx); err != nil {
		return false, err
	}
	if !scope.valid() {
		return false, ErrInvalid
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.enabledLocked(scope), nil
}

func (q *MemoryQueue) SetQueueEnabled(ctx context.Context, scope Scope, enabled bool) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if !scope.valid() {
		return ErrInvalid
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.settings[scopeKey(scope)] = enabled
	return nil
}

func (q *MemoryQueue) AcquireConsumerLease(ctx context.Context, scope Scope, owner string, ttl time.Duration) (bool, error) {
	if err := contextErr(ctx); err != nil {
		return false, err
	}
	owner = strings.TrimSpace(owner)
	if !scope.valid() || owner == "" || ttl <= 0 {
		return false, ErrInvalid
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.enabledLocked(scope) {
		return false, nil
	}
	key := scopeKey(scope)
	now := time.Now().UTC()
	lease, hasLease := q.leases[key]
	if hasLease && lease.owner != owner && lease.expiresAt.After(now) {
		return false, nil
	}
	if !hasLease || lease.owner != owner {
		for id, item := range q.items {
			if scope.matches(item) && item.Status == StatusRunning {
				item.Status = StatusFailed
				item.ErrorCode = "consumer_lease_expired"
				item.UpdatedAt = now
				q.items[id] = item
			}
		}
	}
	q.leases[key] = consumerLease{owner: owner, expiresAt: now.Add(ttl)}
	return true, nil
}

func (q *MemoryQueue) ReleaseConsumerLease(ctx context.Context, scope Scope, owner string) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if !scope.valid() || strings.TrimSpace(owner) == "" {
		return ErrInvalid
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	key := scopeKey(scope)
	if lease, ok := q.leases[key]; ok && lease.owner == strings.TrimSpace(owner) {
		delete(q.leases, key)
	}
	return nil
}

func (q *MemoryQueue) enabledLocked(scope Scope) bool {
	enabled, ok := q.settings[scopeKey(scope)]
	return !ok || enabled
}

func (q *MemoryQueue) countActiveLocked(scope Scope) int {
	count := 0
	for _, item := range q.items {
		if scope.matches(item) && item.Status != StatusSent && item.Status != StatusCancelled {
			count++
		}
	}
	return count
}

func (q *MemoryQueue) nextSequenceLocked() int64 {
	q.sequence++
	return q.sequence
}

func validateNewInput(input NewInput) error {
	if !input.Scope.valid() || strings.TrimSpace(input.ClientInputID) == "" || (strings.TrimSpace(input.Content) == "" && len(input.Attachments) == 0) {
		return ErrInvalid
	}
	if len([]byte(input.Content)) > DefaultMaxContentSize || len([]byte(input.Direction)) > DefaultMaxContentSize {
		return ErrInvalid
	}
	return nil
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func scopeKey(scope Scope) string {
	return fmt.Sprintf("%d:%d:%s", scope.TenantID, scope.UserID, strings.TrimSpace(scope.SessionID))
}

func cloneInput(input PendingInput) PendingInput {
	input.Attachments = cloneAttachments(input.Attachments)
	return input
}

func cloneAttachments(items []agenttasks.Attachment) []agenttasks.Attachment {
	if len(items) == 0 {
		return nil
	}
	return append([]agenttasks.Attachment(nil), items...)
}
