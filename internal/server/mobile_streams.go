package server

import (
	"strconv"
	"sync"
	"time"

	"github.com/konglong87/super-niuma/internal/goal"
)

const (
	mobileGoalPlanUpdatedEvent    = "goal_plan_updated"
	mobileGoalEvidenceAddedEvent  = "goal_evidence_added"
	mobileGoalEvidenceStatusAdded = "added"
)

type MobileStreamRegistry struct {
	mu          sync.Mutex
	cancels     map[string]mobileStreamCancel
	subscribers map[uint64]map[*mobileWSSubscription]struct{}
	goalSubs    map[string]map[*mobileWSSubscription]struct{}
}

type mobileStreamCancel struct {
	tenantKey string
	userID    string
	cancel    func()
}

type mobileWSSubscription struct {
	tenantKey string
	userID    string
	deviceID  string
	sessionID uint64
	goalID    string
	ch        chan mobileWSMessage
	closeOnce sync.Once
}

func NewMobileStreamRegistry() *MobileStreamRegistry {
	return &MobileStreamRegistry{
		cancels:     make(map[string]mobileStreamCancel),
		subscribers: make(map[uint64]map[*mobileWSSubscription]struct{}),
		goalSubs:    make(map[string]map[*mobileWSSubscription]struct{}),
	}
}

func (r *MobileStreamRegistry) Register(sessionID, messageID uint64, messageKey string, cancel func()) {
	r.RegisterFor(mobileClaims{}, sessionID, messageID, messageKey, cancel)
}

func (r *MobileStreamRegistry) RegisterFor(claims mobileClaims, sessionID, messageID uint64, messageKey string, cancel func()) {
	if r == nil || cancel == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, key := range mobileStreamRegistryKeys(sessionID, messageID, messageKey) {
		r.cancels[key] = mobileStreamCancel{tenantKey: claims.TenantKey, userID: claims.UserID, cancel: cancel}
	}
}

func (r *MobileStreamRegistry) Unregister(sessionID, messageID uint64, messageKey string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, key := range mobileStreamRegistryKeys(sessionID, messageID, messageKey) {
		delete(r.cancels, key)
	}
}

func (r *MobileStreamRegistry) Cancel(sessionID, messageID uint64, messageKey string) bool {
	return r.CancelFor(mobileClaims{}, sessionID, messageID, messageKey)
}

func (r *MobileStreamRegistry) CancelFor(claims mobileClaims, sessionID, messageID uint64, messageKey string) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	var cancel func()
	for _, key := range mobileStreamRegistryKeys(sessionID, messageID, messageKey) {
		entry, ok := r.cancels[key]
		if ok && mobileSamePrincipal(claims, entry.tenantKey, entry.userID) {
			cancel = entry.cancel
			break
		}
	}
	r.mu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

func (r *MobileStreamRegistry) Subscribe(claims mobileClaims, sessionID uint64) *mobileWSSubscription {
	if r == nil || sessionID == 0 {
		return nil
	}
	sub := &mobileWSSubscription{
		tenantKey: claims.TenantKey,
		userID:    claims.UserID,
		deviceID:  claims.DeviceID,
		sessionID: sessionID,
		ch:        make(chan mobileWSMessage, 64),
	}
	r.mu.Lock()
	if r.subscribers == nil {
		r.subscribers = make(map[uint64]map[*mobileWSSubscription]struct{})
	}
	if r.subscribers[sessionID] == nil {
		r.subscribers[sessionID] = make(map[*mobileWSSubscription]struct{})
	}
	r.subscribers[sessionID][sub] = struct{}{}
	r.mu.Unlock()
	return sub
}

func (r *MobileStreamRegistry) Unsubscribe(sub *mobileWSSubscription) {
	if r == nil || sub == nil {
		return
	}
	r.mu.Lock()
	if sessions := r.subscribers[sub.sessionID]; sessions != nil {
		delete(sessions, sub)
		if len(sessions) == 0 {
			delete(r.subscribers, sub.sessionID)
		}
	}
	r.mu.Unlock()
	sub.closeOnce.Do(func() { close(sub.ch) })
}

func (r *MobileStreamRegistry) SubscribeGoal(claims mobileClaims, goalID string) *mobileWSSubscription {
	if r == nil || goalID == "" {
		return nil
	}
	sub := &mobileWSSubscription{
		tenantKey: claims.TenantKey,
		userID:    claims.UserID,
		deviceID:  claims.DeviceID,
		goalID:    goalID,
		ch:        make(chan mobileWSMessage, 64),
	}
	r.mu.Lock()
	if r.goalSubs == nil {
		r.goalSubs = make(map[string]map[*mobileWSSubscription]struct{})
	}
	if r.goalSubs[goalID] == nil {
		r.goalSubs[goalID] = make(map[*mobileWSSubscription]struct{})
	}
	r.goalSubs[goalID][sub] = struct{}{}
	r.mu.Unlock()
	return sub
}

func (r *MobileStreamRegistry) UnsubscribeGoal(sub *mobileWSSubscription) {
	if r == nil || sub == nil {
		return
	}
	r.mu.Lock()
	if goals := r.goalSubs[sub.goalID]; goals != nil {
		delete(goals, sub)
		if len(goals) == 0 {
			delete(r.goalSubs, sub.goalID)
		}
	}
	r.mu.Unlock()
	sub.closeOnce.Do(func() { close(sub.ch) })
}

func (r *MobileStreamRegistry) BroadcastFor(claims mobileClaims, event mobileSSEEvent) {
	if r == nil || event.SessionID == 0 {
		return
	}
	msg := mobileWSMessage{
		Type:       "message_event",
		Event:      event.Type,
		SessionID:  event.SessionID,
		MessageID:  event.MessageID,
		MessageKey: event.MessageKey,
		Status:     event.Status,
		Delta:      event.Delta,
		Error:      event.Error,
		TenantKey:  claims.TenantKey,
		UserID:     claims.UserID,
		DeviceID:   claims.DeviceID,
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
	}
	r.mu.Lock()
	targets := make([]*mobileWSSubscription, 0, len(r.subscribers[event.SessionID]))
	for sub := range r.subscribers[event.SessionID] {
		if mobileSamePrincipal(claims, sub.tenantKey, sub.userID) {
			targets = append(targets, sub)
		}
	}
	r.mu.Unlock()
	// WebSocket fanout is best-effort: a slow mobile client must not block the
	// active SSE stream or the model response path for other devices.
	for _, sub := range targets {
		select {
		case sub.ch <- msg:
		default:
		}
	}
}

func (r *MobileStreamRegistry) BroadcastGoalFor(claims mobileClaims, event goal.Event) {
	if r == nil || event.GoalID == "" {
		return
	}
	msg := mobileWSMessage{
		Type:      "goal_event",
		Event:     string(event.Type),
		GoalID:    event.GoalID,
		GoalEvent: event,
		Status:    string(event.Status),
		Error:     event.Error,
		TenantKey: claims.TenantKey,
		UserID:    claims.UserID,
		DeviceID:  claims.DeviceID,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	r.broadcastGoalMessage(event.GoalID, claims, msg)
}

func (r *MobileStreamRegistry) BroadcastGoalPlanFor(claims mobileClaims, plan goal.GoalPlan) {
	if r == nil || plan.GoalID == "" {
		return
	}
	stepID, stepStatus := mobileGoalCurrentStep(plan)
	msg := mobileWSMessage{
		Type:       "goal_event",
		Event:      mobileGoalPlanUpdatedEvent,
		GoalID:     plan.GoalID,
		GoalPlan:   plan,
		StepID:     stepID,
		StepStatus: stepStatus,
		Status:     stepStatus,
		TenantKey:  claims.TenantKey,
		UserID:     claims.UserID,
		DeviceID:   claims.DeviceID,
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
	}
	r.broadcastGoalMessage(plan.GoalID, claims, msg)
}

func (r *MobileStreamRegistry) BroadcastGoalEvidenceFor(claims mobileClaims, evidence goal.GoalEvidence) {
	if r == nil || evidence.GoalID == "" {
		return
	}
	msg := mobileWSMessage{
		Type:         "goal_event",
		Event:        mobileGoalEvidenceAddedEvent,
		GoalID:       evidence.GoalID,
		GoalEvidence: evidence,
		Status:       mobileGoalEvidenceStatusAdded,
		TenantKey:    claims.TenantKey,
		UserID:       claims.UserID,
		DeviceID:     claims.DeviceID,
		Timestamp:    time.Now().UTC().Format(time.RFC3339),
	}
	r.broadcastGoalMessage(evidence.GoalID, claims, msg)
}

func (r *MobileStreamRegistry) broadcastGoalMessage(goalID string, claims mobileClaims, msg mobileWSMessage) {
	r.mu.Lock()
	targets := make([]*mobileWSSubscription, 0, len(r.goalSubs[goalID]))
	for sub := range r.goalSubs[goalID] {
		if mobileSamePrincipal(claims, sub.tenantKey, sub.userID) {
			targets = append(targets, sub)
		}
	}
	r.mu.Unlock()
	for _, sub := range targets {
		select {
		case sub.ch <- msg:
		default:
		}
	}
}

func mobileGoalCurrentStep(plan goal.GoalPlan) (string, string) {
	if plan.CurrentStepID != "" {
		for _, step := range plan.Steps {
			if step.ID == plan.CurrentStepID {
				return step.ID, string(step.Status)
			}
		}
		return plan.CurrentStepID, ""
	}
	for _, step := range plan.Steps {
		if step.Status == goal.StepStatusActive {
			return step.ID, string(step.Status)
		}
	}
	return "", ""
}

func mobileSamePrincipal(claims mobileClaims, tenantKey, userID string) bool {
	if claims.TenantKey == "" && claims.UserID == "" {
		return true
	}
	if tenantKey == "" && userID == "" {
		return true
	}
	return claims.TenantKey == tenantKey && claims.UserID == userID
}

func mobileStreamRegistryKeys(sessionID, messageID uint64, messageKey string) []string {
	keys := make([]string, 0, 2)
	if messageID > 0 {
		keys = append(keys, strconv.FormatUint(sessionID, 10)+":id:"+strconv.FormatUint(messageID, 10))
	}
	if messageKey != "" {
		keys = append(keys, strconv.FormatUint(sessionID, 10)+":key:"+messageKey)
	}
	return keys
}
