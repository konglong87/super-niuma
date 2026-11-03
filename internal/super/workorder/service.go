package workorder

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

const (
	actionCreate = "work_order.create"
	actionGet    = "work_order.get"
	actionList   = "work_order.list"
	actionCancel = "work_order.cancel"
)

type Service struct {
	repo   Repository
	logger *slog.Logger
}

func NewService(repo Repository, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{repo: repo, logger: logger.With("component", "super.workorder")}
}

// Create returns created=false for an exact normalized retry of the same key.
func (s *Service) Create(ctx context.Context, key string, input CreateInput) (WorkOrder, bool, error) {
	if err := s.ready(ctx); err != nil {
		return WorkOrder{}, false, s.failure(ctx, actionCreate, err)
	}
	normalized, err := normalizeInput(input)
	if err != nil || !validKey(key) {
		return WorkOrder{}, false, s.failure(ctx, actionCreate, ErrInvalidInput)
	}
	id, err := uuid.NewRandom()
	if err != nil {
		return WorkOrder{}, false, s.failure(ctx, actionCreate, ErrUnavailable)
	}
	now := time.Now().UTC()
	order := WorkOrder{ID: id.String(), Title: normalized.Title, Objective: normalized.Objective,
		AcceptanceCriteria: normalized.AcceptanceCriteria, Workspace: normalized.Workspace,
		Status: StatusRecorded, ExecutionStatus: NotStarted, VerificationStatus: NotStarted,
		DeliveryStatus: NotStarted, CreatedAt: now, UpdatedAt: now}
	order, created, err := s.repo.Create(ctx, key, order)
	if err != nil {
		return WorkOrder{}, false, s.failure(ctx, actionCreate, err)
	}
	s.logger.InfoContext(ctx, "work-order intake handled", "action", actionCreate, "work_order_id", order.ID, "created", created)
	return order, created, nil
}

func (s *Service) Get(ctx context.Context, id string) (WorkOrder, error) {
	if err := s.ready(ctx); err != nil {
		return WorkOrder{}, s.failure(ctx, actionGet, err)
	}
	if !validID(id) {
		return WorkOrder{}, s.failure(ctx, actionGet, ErrNotFound)
	}
	order, err := s.repo.Get(ctx, id)
	if err != nil {
		return WorkOrder{}, s.failure(ctx, actionGet, err)
	}
	s.logger.DebugContext(ctx, "work order read", "action", actionGet, "work_order_id", order.ID)
	return order, nil
}

func (s *Service) List(ctx context.Context) ([]WorkOrder, error) {
	if err := s.ready(ctx); err != nil {
		return nil, s.failure(ctx, actionList, err)
	}
	orders, err := s.repo.List(ctx, ListLimit)
	if err != nil {
		return nil, s.failure(ctx, actionList, err)
	}
	if orders == nil {
		orders = []WorkOrder{}
	}
	if len(orders) > ListLimit {
		return nil, s.failure(ctx, actionList, ErrUnavailable)
	}
	s.logger.DebugContext(ctx, "work orders listed", "action", actionList, "count", len(orders))
	return orders, nil
}

func (s *Service) Cancel(ctx context.Context, id string) (WorkOrder, error) {
	if err := s.ready(ctx); err != nil {
		return WorkOrder{}, s.failure(ctx, actionCancel, err)
	}
	if !validID(id) {
		return WorkOrder{}, s.failure(ctx, actionCancel, ErrNotFound)
	}
	order, err := s.repo.Cancel(ctx, id, time.Now().UTC())
	if err != nil {
		return WorkOrder{}, s.failure(ctx, actionCancel, err)
	}
	s.logger.InfoContext(ctx, "work-order cancellation handled", "action", actionCancel, "work_order_id", order.ID)
	return order, nil
}

func (s *Service) ready(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.repo == nil {
		return ErrUnavailable
	}
	return nil
}

func (s *Service) failure(ctx context.Context, action string, err error) error {
	safe, code := safeError(err)
	// Never log err, request content, workspace, or an unvalidated caller ID.
	s.logger.WarnContext(ctx, "work-order operation refused", "action", action, "code", code)
	return safe
}

func safeError(err error) (error, string) {
	for _, known := range []struct {
		err  error
		code string
	}{
		{ErrInvalidInput, "invalid_input"}, {ErrNotFound, "not_found"},
		{ErrIdempotencyConflict, "idempotency_conflict"},
		{context.Canceled, "request_cancelled"}, {context.DeadlineExceeded, "deadline_exceeded"},
	} {
		if errors.Is(err, known.err) {
			return known.err, known.code
		}
	}
	return ErrUnavailable, "service_unavailable"
}
