package workorder

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"
)

type memoryRepository struct {
	orders  map[string]WorkOrder
	keys    map[string]string
	calls   int
	failure error
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{orders: map[string]WorkOrder{}, keys: map[string]string{}}
}
func (r *memoryRepository) Create(_ context.Context, key string, order WorkOrder) (WorkOrder, bool, error) {
	r.calls++
	if r.failure != nil {
		return WorkOrder{}, false, r.failure
	}
	if id, ok := r.keys[key]; ok {
		return r.orders[id], false, nil
	}
	r.orders[order.ID], r.keys[key] = order, order.ID
	return order, true, nil
}
func (r *memoryRepository) Get(_ context.Context, id string) (WorkOrder, error) {
	r.calls++
	if r.failure != nil {
		return WorkOrder{}, r.failure
	}
	order, ok := r.orders[id]
	if !ok {
		return WorkOrder{}, ErrNotFound
	}
	return order, nil
}
func (r *memoryRepository) List(_ context.Context, limit int) ([]WorkOrder, error) {
	r.calls++
	if limit != ListLimit {
		panic("unbounded list")
	}
	return nil, r.failure
}
func (r *memoryRepository) Cancel(ctx context.Context, id string, at time.Time) (WorkOrder, error) {
	order, err := r.Get(ctx, id)
	if err != nil {
		return WorkOrder{}, err
	}
	if order.Status != StatusCancelled {
		order.Status, order.UpdatedAt = StatusCancelled, at
		r.orders[id] = order
	}
	return order, nil
}
func validInput() CreateInput {
	return CreateInput{Title: "Title", Objective: "Objective", AcceptanceCriteria: []string{"Criterion"}}
}
func TestCreateNormalizesAndNeverStartsExecution(t *testing.T) {
	repo := newMemoryRepository()
	service := NewService(repo, nil)
	input := CreateInput{Title: "  Title  ", Objective: "\nObjective\n", AcceptanceCriteria: []string{" Criterion "}, Workspace: " /metadata/../workspace "}
	order, created, err := service.Create(context.Background(), "key-1", input)
	if err != nil || !created {
		t.Fatalf("create: created=%v err=%v", created, err)
	}
	if order.Title != "Title" || order.Objective != "Objective" || order.Workspace != "/workspace" || !reflect.DeepEqual(order.AcceptanceCriteria, []string{"Criterion"}) {
		t.Fatalf("normalization: %#v", order)
	}
	if order.Status != StatusRecorded || order.ExecutionStatus != NotStarted || order.VerificationStatus != NotStarted || order.DeliveryStatus != NotStarted {
		t.Fatalf("unsafe state: %#v", order)
	}
	if order.ID == "" || order.CreatedAt.IsZero() || !order.UpdatedAt.Equal(order.CreatedAt) {
		t.Fatal("missing identity or timestamps")
	}
	if input.AcceptanceCriteria[0] != " Criterion " {
		t.Fatal("mutated caller's slice")
	}
}
func TestInvalidInputNeverReachesRepository(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*CreateInput)
	}{
		{"empty title", func(v *CreateInput) { v.Title = " \n " }},
		{"long title", func(v *CreateInput) { v.Title = strings.Repeat("a", MaxTitleBytes+1) }},
		{"empty objective", func(v *CreateInput) { v.Objective = "" }},
		{"long objective", func(v *CreateInput) { v.Objective = strings.Repeat("a", MaxObjectiveBytes+1) }},
		{"nil criteria", func(v *CreateInput) { v.AcceptanceCriteria = nil }},
		{"empty criterion", func(v *CreateInput) { v.AcceptanceCriteria = []string{" "} }},
		{"long criterion", func(v *CreateInput) { v.AcceptanceCriteria = []string{strings.Repeat("a", MaxCriterionBytes+1)} }},
		{"many criteria", func(v *CreateInput) { v.AcceptanceCriteria = make([]string, MaxAcceptanceCriteria+1) }},
		{"relative workspace", func(v *CreateInput) { v.Workspace = "relative/path" }},
		{"tilde workspace", func(v *CreateInput) { v.Workspace = "~/workspace" }},
		{"long workspace", func(v *CreateInput) { v.Workspace = "/" + strings.Repeat("a", MaxWorkspaceBytes) }},
		{"null workspace", func(v *CreateInput) { v.Workspace = "/tmp/\x00secret" }},
		{"line workspace", func(v *CreateInput) { v.Workspace = "/tmp/a\nb" }},
		{"invalid utf8", func(v *CreateInput) { v.Title = string([]byte{0xff}) }},
		{"control text", func(v *CreateInput) { v.Objective = "a\x1bb" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newMemoryRepository()
			input := validInput()
			tt.mutate(&input)
			_, _, err := NewService(repo, nil).Create(context.Background(), "key", input)
			if !errors.Is(err, ErrInvalidInput) || repo.calls != 0 {
				t.Fatalf("err=%v calls=%d", err, repo.calls)
			}
		})
	}
	for _, key := range []string{"", " ", " key", "key\n", "key/other", strings.Repeat("a", MaxIdempotencyKeyBytes+1)} {
		repo := newMemoryRepository()
		_, _, err := NewService(repo, nil).Create(context.Background(), key, validInput())
		if !errors.Is(err, ErrInvalidInput) || repo.calls != 0 {
			t.Fatalf("key validation failed: err=%v calls=%d", err, repo.calls)
		}
	}
}
func TestServiceListAndCancellation(t *testing.T) {
	service := NewService(newMemoryRepository(), nil)
	ctx := context.Background()
	items, err := service.List(ctx)
	if err != nil || items == nil || len(items) != 0 {
		t.Fatalf("list: %v %v", items, err)
	}
	order, _, err := service.Create(ctx, "key", validInput())
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := service.Cancel(ctx, order.ID)
	if err != nil || cancelled.Status != StatusCancelled {
		t.Fatalf("cancel: %v %v", cancelled, err)
	}
	again, err := service.Cancel(ctx, order.ID)
	if err != nil || !reflect.DeepEqual(cancelled, again) {
		t.Fatal("repeat cancel changed aggregate")
	}
	for _, id := range []string{"", "../bad", "not-an-id", strings.Repeat("a", 1000)} {
		if _, err := service.Get(ctx, id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("get invalid ID: %v", err)
		}
		if _, err := service.Cancel(ctx, id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("cancel invalid ID: %v", err)
		}
	}
}
func TestServiceLogsAreRedacted(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug}))
	repo := newMemoryRepository()
	service := NewService(repo, logger)
	input := CreateInput{Title: "SECRET_TITLE", Objective: "SECRET_OBJECTIVE", AcceptanceCriteria: []string{"SECRET_CRITERION"}, Workspace: "/SECRET_WORKSPACE"}
	order, _, err := service.Create(context.Background(), "SECRET_KEY", input)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = service.Get(context.Background(), order.ID)
	_, _ = service.List(context.Background())
	_, _ = service.Cancel(context.Background(), order.ID)
	repo.failure = errors.New("SECRET_SQL_FAILURE")
	_, _, err = service.Create(context.Background(), "other", input)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unsafe error: %v", err)
	}
	if strings.Contains(output.String(), "SECRET") || !strings.Contains(output.String(), order.ID) || !strings.Contains(output.String(), "action") {
		t.Fatalf("unsafe/unstructured log: %s", output.String())
	}
}
func TestServiceCancelledContext(t *testing.T) {
	repo := newMemoryRepository()
	service := NewService(repo, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := service.Create(ctx, "key", validInput()); !errors.Is(err, context.Canceled) {
		t.Fatalf("create: %v", err)
	}
	if repo.calls != 0 {
		t.Fatal("cancelled request reached repository")
	}
}
