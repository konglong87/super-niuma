package mysql

import (
	"database/sql/driver"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/DATA-DOG/go-sqlmock"
	drivermysql "github.com/go-sql-driver/mysql"
	"github.com/konglong87/super-niuma/internal/imagegen"
	"gorm.io/gorm"
)

func TestCreateImageGenerationPersistsTenantSessionScope(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectExec("INSERT INTO `image_generations`").WillReturnResult(sqlmock.NewResult(19, 1))
	got, err := repo.CreateImageGeneration(testContext(), ImageGenerationInput{GenerationID: "gen-1", TenantID: 7, UserID: 11, SessionID: 13, Operation: "generate", Status: "running", Prompt: "a cat", Provider: "jiuan", Model: "gpt-image-2", IdempotencyKey: "req-1"})
	if err != nil || got.GenerationID != "gen-1" || got.TenantID != 7 || got.SessionID != 13 {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	assertExpectations(t, mock)
}

func TestGetImageGenerationRequiresExactTenantUserSession(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	now := time.Now().UTC()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `image_generations` WHERE tenant_id = ? AND user_id = ? AND session_id = ? AND generation_id = ? LIMIT ?")).
		WithArgs(uint64(7), uint64(11), uint64(13), "gen-1", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "generation_id", "tenant_id", "user_id", "session_id", "asset_id", "source_asset_id", "operation", "status", "prompt", "provider", "model", "request_json", "error_code", "error_message", "trace_id", "idempotency_key", "created_at", "finished_at"}).AddRow(19, "gen-1", 7, 11, 13, nil, nil, "generate", "running", "a cat", "jiuan", "gpt-image-2", nil, nil, nil, nil, "req-1", now, nil))
	got, err := repo.GetImageGeneration(testContext(), 7, 11, 13, "gen-1")
	if err != nil || got.GenerationID != "gen-1" {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	assertExpectations(t, mock)
}

func TestCreateImageGenerationRejectsMissingScope(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	if _, err := repo.CreateImageGeneration(testContext(), ImageGenerationInput{GenerationID: "gen-1", TenantID: 7, UserID: 11, Operation: "generate", Status: "running", Prompt: "cat", Provider: "p", Model: "m"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err=%v", err)
	}
	assertExpectations(t, mock)
}

func TestEnqueueImageGenerationReturnsExistingIdempotentJob(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	now := time.Now().UTC()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `image_generations` WHERE tenant_id = ? AND user_id = ? AND session_id = ? AND idempotency_key = ? LIMIT ?")).
		WithArgs(uint64(7), uint64(11), uint64(13), "channel:fixed", 1).
		WillReturnRows(imageGenerationRows(now).AddRow(19, "gen-existing", 7, 11, 13, nil, nil, "generate", "queued", "a cat", "jiuan", "gpt-image-2", nil, nil, nil, nil, "channel:fixed", now, nil))
	got, created, err := repo.EnqueueImageGeneration(testContext(), ImageGenerationInput{GenerationID: "gen-new", TenantID: 7, UserID: 11, SessionID: 13, Operation: "generate", Status: "queued", Prompt: "a cat", Provider: "jiuan", Model: "gpt-image-2", IdempotencyKey: "channel:fixed"})
	if err != nil || created || got.GenerationID != "gen-existing" {
		t.Fatalf("got=%+v created=%t err=%v", got, created, err)
	}
	assertExpectations(t, mock)
}

func TestEnqueueImageGenerationCreatesQueuedJobWithDefaults(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectQuery("SELECT .* FROM `image_generations` WHERE tenant_id = \\? AND user_id = \\? AND session_id = \\? AND idempotency_key = \\? LIMIT \\?").WithArgs(uint64(7), uint64(11), uint64(13), "channel:fixed", 1).WillReturnError(gorm.ErrRecordNotFound)
	mock.ExpectExec("INSERT INTO `image_generations` .*ON DUPLICATE KEY UPDATE").WithArgs(queuedImageGenerationArgs()...).WillReturnResult(sqlmock.NewResult(19, 1))
	got, created, err := repo.EnqueueImageGeneration(testContext(), ImageGenerationInput{GenerationID: "gen-new", TenantID: 7, UserID: 11, SessionID: 13, Operation: "generate", Status: "failed", Prompt: "a cat", Provider: "jiuan", Model: "gpt-image-2", IdempotencyKey: "channel:fixed"})
	if err != nil || !created || got.Status != "queued" || got.MaxAttempts != 3 || got.OriginType != "direct" || got.IdempotencyKey != "channel:fixed" {
		t.Fatalf("got=%+v created=%t err=%v", got, created, err)
	}
	assertExpectations(t, mock)
}

func TestEnqueueImageGenerationReadsBackDuplicateKeyRace(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	now := time.Now().UTC()
	mock.ExpectQuery("SELECT .* FROM `image_generations` WHERE tenant_id = \\? AND user_id = \\? AND session_id = \\? AND idempotency_key = \\? LIMIT \\?").WithArgs(uint64(7), uint64(11), uint64(13), "channel:fixed", 1).WillReturnError(gorm.ErrRecordNotFound)
	mock.ExpectExec("INSERT INTO `image_generations` .*ON DUPLICATE KEY UPDATE").WillReturnError(&drivermysql.MySQLError{Number: 1062, Message: "duplicate"})
	mock.ExpectQuery("SELECT .* FROM `image_generations` WHERE tenant_id = \\? AND user_id = \\? AND session_id = \\? AND idempotency_key = \\? LIMIT \\?").WithArgs(uint64(7), uint64(11), uint64(13), "channel:fixed", 1).WillReturnRows(imageGenerationRows(now).AddRow(19, "gen-existing", 7, 11, 13, nil, nil, "generate", "queued", "a cat", "jiuan", "gpt-image-2", nil, nil, nil, nil, "channel:fixed", now, nil))
	got, created, err := repo.EnqueueImageGeneration(testContext(), ImageGenerationInput{GenerationID: "gen-new", TenantID: 7, UserID: 11, SessionID: 13, Operation: "generate", Status: "queued", Prompt: "a cat", Provider: "jiuan", Model: "gpt-image-2", IdempotencyKey: "channel:fixed"})
	if err != nil || created || got.GenerationID != "gen-existing" || got.TenantID != 7 || got.UserID != 11 || got.SessionID != 13 {
		t.Fatalf("got=%+v created=%t err=%v", got, created, err)
	}
	assertExpectations(t, mock)
}

func TestAdmitImageGenerationRejectsTenantQueueLimitAtomically(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `tenants` WHERE id = \\? LIMIT \\? FOR UPDATE").WithArgs(uint64(7), 1).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))
	mock.ExpectQuery("SELECT .* FROM `image_generations` WHERE tenant_id = \\? AND user_id = \\? AND session_id = \\? AND idempotency_key = \\? LIMIT \\?").WithArgs(uint64(7), uint64(11), uint64(13), "channel:fixed", 1).WillReturnError(gorm.ErrRecordNotFound)
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `image_generations` WHERE tenant_id = \\? AND status IN \\(\\?, \\?\\)").WithArgs(uint64(7), "queued", "retry").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	mock.ExpectCommit()
	_, created, err := repo.AdmitImageGeneration(testContext(), admittedGenerationRecord(), imagegen.QueueLimits{MaxQueuedPerTenant: 2})
	if !errors.Is(err, imagegen.ErrImageQueueFull) || created {
		t.Fatalf("created=%t err=%v", created, err)
	}
	assertExpectations(t, mock)
}

func TestAdmitImageGenerationRejectsUserQueueLimitAtomically(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `tenants` WHERE id = \\? LIMIT \\? FOR UPDATE").WithArgs(uint64(7), 1).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))
	mock.ExpectQuery("SELECT .* FROM `image_generations` WHERE tenant_id = \\? AND user_id = \\? AND session_id = \\? AND idempotency_key = \\? LIMIT \\?").WithArgs(uint64(7), uint64(11), uint64(13), "channel:fixed", 1).WillReturnError(gorm.ErrRecordNotFound)
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `image_generations` WHERE tenant_id = \\? AND status IN \\(\\?, \\?\\)").WithArgs(uint64(7), "queued", "retry").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `image_generations` WHERE \\(tenant_id = \\? AND status IN \\(\\?, \\?\\)\\) AND user_id = \\?").WithArgs(uint64(7), "queued", "retry", uint64(11)).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectCommit()
	_, created, err := repo.AdmitImageGeneration(testContext(), admittedGenerationRecord(), imagegen.QueueLimits{MaxQueuedPerTenant: 2, MaxQueuedPerUser: 1})
	if !errors.Is(err, imagegen.ErrImageQueueFull) || created {
		t.Fatalf("created=%t err=%v", created, err)
	}
	assertExpectations(t, mock)
}

func TestAdmitImageGenerationReturnsExistingScopedJobBeforeQueueCounts(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	now := time.Now().UTC()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `tenants` WHERE id = \\? LIMIT \\? FOR UPDATE").WithArgs(uint64(7), 1).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))
	mock.ExpectQuery("SELECT .* FROM `image_generations` WHERE tenant_id = \\? AND user_id = \\? AND session_id = \\? AND idempotency_key = \\? LIMIT \\?").WithArgs(uint64(7), uint64(11), uint64(13), "channel:fixed", 1).WillReturnRows(imageGenerationRows(now).AddRow(19, "gen-existing", 7, 11, 13, nil, nil, "generate", "queued", "a cat", "jiuan", "gpt-image-2", nil, nil, nil, nil, "channel:fixed", now, nil))
	mock.ExpectCommit()
	got, created, err := repo.AdmitImageGeneration(testContext(), admittedGenerationRecord(), imagegen.QueueLimits{MaxQueuedPerTenant: 1, MaxQueuedPerUser: 1})
	if err != nil || created || got.GenerationID != "gen-existing" || got.TenantID != 7 || got.UserID != 11 || got.SessionID != 13 {
		t.Fatalf("got=%+v created=%t err=%v", got, created, err)
	}
	assertExpectations(t, mock)
}

func TestAdmitImageGenerationInsertsWithExactTenantUserSessionScope(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `tenants` WHERE id = \\? LIMIT \\? FOR UPDATE").WithArgs(uint64(7), 1).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))
	mock.ExpectQuery("SELECT .* FROM `image_generations` WHERE tenant_id = \\? AND user_id = \\? AND session_id = \\? AND idempotency_key = \\? LIMIT \\?").WithArgs(uint64(7), uint64(11), uint64(13), "channel:fixed", 1).WillReturnError(gorm.ErrRecordNotFound)
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `image_generations` WHERE tenant_id = \\? AND status IN \\(\\?, \\?\\)").WithArgs(uint64(7), "queued", "retry").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `image_generations` WHERE \\(tenant_id = \\? AND status IN \\(\\?, \\?\\)\\) AND user_id = \\?").WithArgs(uint64(7), "queued", "retry", uint64(11)).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec("INSERT INTO `image_generations` .*ON DUPLICATE KEY UPDATE").WillReturnResult(sqlmock.NewResult(19, 1))
	mock.ExpectCommit()
	got, created, err := repo.AdmitImageGeneration(testContext(), admittedGenerationRecord(), imagegen.QueueLimits{MaxQueuedPerTenant: 2, MaxQueuedPerUser: 1})
	if err != nil || !created || got.GenerationID != "gen-new" || got.TenantID != 7 || got.UserID != 11 || got.SessionID != 13 {
		t.Fatalf("got=%+v created=%t err=%v", got, created, err)
	}
	assertExpectations(t, mock)
}

func TestAdmitImageGenerationRollsBackTenantLockError(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	want := errors.New("tenant lock unavailable")
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `tenants` WHERE id = \\? LIMIT \\? FOR UPDATE").WithArgs(uint64(7), 1).WillReturnError(want)
	mock.ExpectRollback()
	_, created, err := repo.AdmitImageGeneration(testContext(), admittedGenerationRecord(), imagegen.QueueLimits{})
	if !errors.Is(err, want) || created {
		t.Fatalf("created=%t err=%v", created, err)
	}
	assertExpectations(t, mock)
}

func TestClaimDueImageGenerationsLeasesJobAndRecordsAttempt(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	now := time.Now().UTC()
	leaseUntil := now.Add(4 * time.Minute)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `image_generations` WHERE tenant_id = \\? AND attempts < max_attempts AND .*status IN \\(\\?, \\?\\).*status = \\?.*lease_until IS NOT NULL.* ORDER BY next_attempt_at ASC, id ASC LIMIT \\? FOR UPDATE").
		WillReturnRows(imageGenerationRows(now).AddRow(19, "gen-1", 7, 11, 13, nil, nil, "generate", "queued", "a cat", "jiuan", "gpt-image-2", nil, nil, nil, nil, "channel:fixed", now, nil))
	mock.ExpectExec("UPDATE `image_generations` SET").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO `image_generation_attempts`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	got, err := repo.ClaimDueImageGenerations(testContext(), 7, "worker-a", 1, leaseUntil)
	if err != nil || len(got) != 1 || got[0].Status != "running" || got[0].LeaseOwner != "worker-a" || got[0].Attempts != 1 {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	assertExpectations(t, mock)
}

func TestClaimDueImageGenerationsReclaimsOnlyExpiredLeasedRunningJob(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	now := time.Now().UTC()
	leaseUntil := now.Add(4 * time.Minute)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `image_generations` WHERE tenant_id = \\? AND attempts < max_attempts AND .*status IN \\(\\?, \\?\\).*status = \\?.*lease_until IS NOT NULL.*lease_until < \\?.* ORDER BY next_attempt_at ASC, id ASC LIMIT \\? FOR UPDATE").
		WillReturnRows(sqlmock.NewRows([]string{"id", "generation_id", "tenant_id", "user_id", "session_id", "operation", "status", "prompt", "provider", "model", "request_json", "idempotency_key", "origin_type", "attempts", "max_attempts", "lease_owner", "lease_until", "created_at"}).
			AddRow(19, "gen-stale", 7, 11, 13, "generate", "running", "a cat", "jiuan", "gpt-image-2", validStoredGenerateRequest(), "channel:fixed", "channel", 1, 3, "dead-worker", now.Add(-time.Second), now.Add(-time.Minute)))
	mock.ExpectExec("UPDATE `image_generations` SET").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO `image_generation_attempts`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	jobs, err := repo.ClaimDueImageGenerations(testContext(), 7, "worker-new", 1, leaseUntil)
	if err != nil || len(jobs) != 1 || jobs[0].GenerationID != "gen-stale" || jobs[0].Attempts != 2 || jobs[0].LeaseOwner != "worker-new" {
		t.Fatalf("jobs=%+v err=%v", jobs, err)
	}
	assertExpectations(t, mock)
}

func validStoredGenerateRequest() string {
	return `{"operation":"generate","prompt":"a cat","model":"gpt-image-2","quality":"auto","size":"auto","output_format":"png","background":"auto"}`
}

func TestRenewImageGenerationLeaseRejectsWrongOwner(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `image_generations` WHERE tenant_id = \\? AND generation_id = \\? AND status = \\? AND lease_owner = \\? LIMIT \\? FOR UPDATE").
		WithArgs(uint64(7), "gen-1", "running", "old-worker", 1).
		WillReturnRows(sqlmock.NewRows([]string{"cancel_requested_at"}))
	mock.ExpectRollback()
	_, err := repo.RenewImageGenerationLease(testContext(), 7, "gen-1", "old-worker", time.Now().UTC().Add(time.Minute))
	if !errors.Is(err, ErrImageGenerationLeaseLost) {
		t.Fatalf("err=%v", err)
	}
	assertExpectations(t, mock)
}

func TestRenewImageGenerationLeaseWritesHeartbeat(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	leaseUntil := time.Now().UTC().Add(time.Minute)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `image_generations` WHERE tenant_id = \\? AND generation_id = \\? AND status = \\? AND lease_owner = \\? LIMIT \\? FOR UPDATE").
		WithArgs(uint64(7), "gen-1", "running", "worker-a", 1).
		WillReturnRows(sqlmock.NewRows([]string{"cancel_requested_at"}).AddRow(nil))
	mock.ExpectExec("UPDATE `image_generations` SET .*heartbeat_at.*lease_until.*WHERE tenant_id = \\? AND generation_id = \\? AND status = \\? AND lease_owner = \\?").WithArgs(anyTime{}, leaseUntil, anyTime{}, uint64(7), "gen-1", "running", "worker-a").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	cancelRequested, err := repo.RenewImageGenerationLease(testContext(), 7, "gen-1", "worker-a", leaseUntil)
	if err != nil || cancelRequested {
		t.Fatalf("cancelRequested=%t err=%v", cancelRequested, err)
	}
	assertExpectations(t, mock)
}

func TestRenewImageGenerationLeaseObservesCancellationWithoutExtendingLease(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	cancelledAt := time.Now().UTC()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `image_generations` WHERE tenant_id = \\? AND generation_id = \\? AND status = \\? AND lease_owner = \\? LIMIT \\? FOR UPDATE").
		WithArgs(uint64(7), "gen-1", "running", "worker-a", 1).
		WillReturnRows(sqlmock.NewRows([]string{"cancel_requested_at"}).AddRow(cancelledAt))
	mock.ExpectCommit()
	cancelRequested, err := repo.RenewImageGenerationLease(testContext(), 7, "gen-1", "worker-a", time.Now().UTC().Add(time.Minute))
	if err != nil || !cancelRequested {
		t.Fatalf("cancelRequested=%t err=%v", cancelRequested, err)
	}
	assertExpectations(t, mock)
}

func TestRenewImageGenerationLeaseReturnsRollbackFailure(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	selectErr := errors.New("lease read failed")
	rollbackErr := errors.New("lease rollback failed")
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `image_generations`").WillReturnError(selectErr)
	mock.ExpectRollback().WillReturnError(rollbackErr)
	_, err := repo.RenewImageGenerationLease(testContext(), 7, "gen-1", "worker-a", time.Now().UTC().Add(time.Minute))
	if !errors.Is(err, selectErr) || !errors.Is(err, rollbackErr) {
		t.Fatal(err)
	}
	assertExpectations(t, mock)
}

func TestScheduleImageGenerationRetryRequiresLeaseOwner(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectExec("UPDATE `image_generations` SET .*WHERE tenant_id = \\? AND generation_id = \\? AND status = \\? AND lease_owner = \\?").WillReturnResult(sqlmock.NewResult(0, 0))
	err := repo.ScheduleImageGenerationRetry(testContext(), 7, "gen-1", "old-worker", time.Now().UTC().Add(time.Minute), "provider_unavailable", "temporary")
	if !errors.Is(err, ErrImageGenerationLeaseLost) {
		t.Fatalf("err=%v", err)
	}
	assertExpectations(t, mock)
}

func TestScheduleImageGenerationRetryClearsLease(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	next := time.Now().UTC().Add(time.Minute)
	mock.ExpectExec("UPDATE `image_generations` SET .*lease_owner.*lease_until.*next_attempt_at.*status.*WHERE tenant_id = \\? AND generation_id = \\? AND status = \\? AND lease_owner = \\?").WithArgs("provider_unavailable", "temporary", nil, nil, next, "retry", anyTime{}, uint64(7), "gen-1", "running", "worker-a").WillReturnResult(sqlmock.NewResult(0, 1))
	if err := repo.ScheduleImageGenerationRetry(testContext(), 7, "gen-1", "worker-a", next, "provider_unavailable", "temporary"); err != nil {
		t.Fatal(err)
	}
	assertExpectations(t, mock)
}

func TestFinalizeImageGenerationRequiresRunningLeaseOwner(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `image_generations` .*FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"generation_id"}))
	mock.ExpectRollback()
	err := repo.FinalizeImageGeneration(testContext(), 7, "gen-1", "old-worker", "outcome_unknown", "provider_outcome_unknown", "response lost", "provider-req-1")
	if !errors.Is(err, ErrImageGenerationLeaseLost) {
		t.Fatalf("err=%v", err)
	}
	assertExpectations(t, mock)
}

func TestFinalizeImageGenerationClearsLeaseAndSetsTerminalFields(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `image_generations` .*FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"generation_id", "tenant_id", "origin_type"}).AddRow("gen-1", 7, imagegen.OriginTypeDirect))
	mock.ExpectExec("UPDATE `image_generations` SET .*finished_at.*lease_owner.*lease_until.*status.*WHERE tenant_id = \\? AND generation_id = \\? AND status = \\? AND lease_owner = \\?").WithArgs("provider_rejected", "invalid", anyTime{}, nil, nil, "provider-1", "failed", anyTime{}, uint64(7), "gen-1", "running", "worker-a").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := repo.FinalizeImageGeneration(testContext(), 7, "gen-1", "worker-a", "failed", "provider_rejected", "invalid", "provider-1"); err != nil {
		t.Fatal(err)
	}
	assertExpectations(t, mock)
}

func TestFinalizeImageGenerationCreatesCanonicalTerminalEventForChannelJob(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `image_generations` .*FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"generation_id", "tenant_id", "origin_type", "origin_ref_json", "idempotency_key"}).AddRow("gen-1", 7, imagegen.OriginTypeChannel, `{"version":1,"account_id":3,"conversation_id":9,"run_id":"run-1"}`, "channel:fixed"))
	mock.ExpectExec("UPDATE `image_generations` SET").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO `image_completion_outbox` .*ON DUPLICATE KEY UPDATE").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	if err := repo.FinalizeImageGeneration(testContext(), 7, "gen-1", "worker-a", imagegen.GenerationStatusOutcomeUnknown, imagegen.ErrorClassProviderOutcomeUnknown, "response lost", "provider-1"); err != nil {
		t.Fatal(err)
	}
	assertExpectations(t, mock)
}

func TestRequestImageGenerationCancelCancelsQueuedJob(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `image_generations` .*FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"generation_id", "tenant_id", "user_id", "session_id", "status", "origin_type"}).AddRow("gen-1", 7, 11, 13, imagegen.GenerationStatusQueued, imagegen.OriginTypeDirect))
	mock.ExpectExec("UPDATE `image_generations` SET .*WHERE tenant_id = \\? AND user_id = \\? AND session_id = \\? AND generation_id = \\? AND status = \\?").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	cancelled, err := repo.RequestImageGenerationCancel(testContext(), 7, 11, 13, "gen-1")
	if err != nil || !cancelled {
		t.Fatalf("cancelled=%t err=%v", cancelled, err)
	}
	assertExpectations(t, mock)
}

func TestRequestImageGenerationCancelRecordsRunningCancellation(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `image_generations` .*FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"generation_id", "tenant_id", "user_id", "session_id", "status", "origin_type"}).AddRow("gen-1", 7, 11, 13, imagegen.GenerationStatusRunning, imagegen.OriginTypeDirect))
	mock.ExpectExec("UPDATE `image_generations` SET .*WHERE tenant_id = \\? AND user_id = \\? AND session_id = \\? AND generation_id = \\? AND status = \\?").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	cancelled, err := repo.RequestImageGenerationCancel(testContext(), 7, 11, 13, "gen-1")
	if err != nil || cancelled {
		t.Fatalf("cancelled=%t err=%v", cancelled, err)
	}
	assertExpectations(t, mock)
}

func TestRetryImageGenerationClonesTerminalSourceAtomicallyWithExactScope(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	now := time.Date(2026, time.September, 3, 2, 3, 4, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `image_generations` WHERE tenant_id = \\? AND user_id = \\? AND session_id = \\? AND generation_id = \\? LIMIT \\? FOR UPDATE").
		WithArgs(uint64(7), uint64(11), uint64(13), "gen-failed", 1).
		WillReturnRows(manualRetrySourceRows().AddRow(19, "gen-failed", 7, 11, 13, "asset-old", "asset-source", imagegen.OperationEdit, imagegen.GenerationStatusFailed, "private prompt", "jiuan", "gpt-image-2", `{"operation":"edit","prompt":"private prompt"}`, "provider_rejected", "safe", "trace-old", "channel:old", "batch-old", imagegen.OriginTypeChannel, `{"version":1,"account_id":3,"conversation_id":9,"run_id":"run-old"}`, "tool-old", nil, 3, now, now))
	mock.ExpectQuery("SELECT .* FROM `image_generations` WHERE tenant_id = \\? AND user_id = \\? AND session_id = \\? AND idempotency_key = \\? LIMIT \\?").
		WithArgs(uint64(7), uint64(11), uint64(13), "channel:new", 1).
		WillReturnError(gorm.ErrRecordNotFound)
	mock.ExpectExec("INSERT INTO `image_generations`").WillReturnResult(sqlmock.NewResult(20, 1))
	mock.ExpectCommit()

	got, created, err := repo.RetryImageGeneration(testContext(), imagegen.ManualRetryRequest{
		Scope: imagegen.JobScope{TenantID: 7, UserID: 11, SessionID: 13}, SourceGenerationID: "gen-failed",
		GenerationID: "gen-new", IdempotencyKey: "channel:new", BatchID: "batch-new", ToolUseID: "command:image-retry:gen-failed",
		Origin:  imagegen.OriginMetadata{Type: imagegen.OriginTypeChannel, RefJSON: `{"version":2,"tenant_id":7,"account_id":3,"conversation_id":9,"run_id":"run-new","session_id":13,"user_id":11}`},
		TraceID: "trace-new", CreatedAt: now,
	})
	if err != nil || !created {
		t.Fatalf("got=%+v created=%t err=%v", got, created, err)
	}
	if got.GenerationID != "gen-new" || got.RetryOfGenerationID != "gen-failed" || got.Status != imagegen.GenerationStatusQueued || got.Attempts != 0 || got.AssetID != "" || got.ErrorMessage != "" || got.Prompt != "private prompt" || got.RequestJSON == "" || got.BatchID != "batch-new" || got.IdempotencyKey != "channel:new" {
		t.Fatalf("retried generation=%+v", got)
	}
	assertExpectations(t, mock)
}

func TestRetryImageGenerationRejectsNonRetryableState(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	now := time.Now().UTC()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `image_generations` WHERE tenant_id = \\? AND user_id = \\? AND session_id = \\? AND generation_id = \\? LIMIT \\? FOR UPDATE").
		WithArgs(uint64(7), uint64(11), uint64(13), "gen-running", 1).
		WillReturnRows(manualRetrySourceRows().AddRow(19, "gen-running", 7, 11, 13, nil, nil, imagegen.OperationGenerate, imagegen.GenerationStatusRunning, "private", "jiuan", "gpt-image-2", `{}`, nil, nil, nil, "channel:old", "batch-old", imagegen.OriginTypeChannel, `{"version":1}`, "tool-old", nil, 3, now, nil))
	mock.ExpectRollback()

	_, _, err := repo.RetryImageGeneration(testContext(), imagegen.ManualRetryRequest{
		Scope: imagegen.JobScope{TenantID: 7, UserID: 11, SessionID: 13}, SourceGenerationID: "gen-running",
		GenerationID: "gen-new", IdempotencyKey: "channel:new", Origin: imagegen.OriginMetadata{Type: imagegen.OriginTypeChannel, RefJSON: `{"version":2}`}, CreatedAt: now,
	})
	if !errors.Is(err, imagegen.ErrImageManualRetryNotAllowed) {
		t.Fatalf("err=%v", err)
	}
	assertExpectations(t, mock)
}

func TestRetryImageGenerationDoesNotRevealCrossScopeSource(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `image_generations` WHERE tenant_id = \\? AND user_id = \\? AND session_id = \\? AND generation_id = \\? LIMIT \\? FOR UPDATE").
		WithArgs(uint64(7), uint64(11), uint64(13), "gen-other", 1).
		WillReturnError(gorm.ErrRecordNotFound)
	mock.ExpectRollback()

	_, _, err := repo.RetryImageGeneration(testContext(), imagegen.ManualRetryRequest{
		Scope: imagegen.JobScope{TenantID: 7, UserID: 11, SessionID: 13}, SourceGenerationID: "gen-other",
		GenerationID: "gen-new", IdempotencyKey: "channel:new", Origin: imagegen.OriginMetadata{Type: imagegen.OriginTypeChannel, RefJSON: `{"version":2}`}, CreatedAt: time.Now(),
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
	assertExpectations(t, mock)
}

func TestRetryImageGenerationReusesIdempotentClone(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	now := time.Now().UTC()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `image_generations` WHERE tenant_id = \\? AND user_id = \\? AND session_id = \\? AND generation_id = \\? LIMIT \\? FOR UPDATE").
		WithArgs(uint64(7), uint64(11), uint64(13), "gen-failed", 1).
		WillReturnRows(manualRetrySourceRows().AddRow(19, "gen-failed", 7, 11, 13, nil, nil, imagegen.OperationGenerate, imagegen.GenerationStatusDead, "private", "jiuan", "gpt-image-2", `{}`, "dead", "safe", nil, "channel:old", "batch-old", imagegen.OriginTypeChannel, `{"version":1}`, "tool-old", nil, 3, now, &now))
	mock.ExpectQuery("SELECT .* FROM `image_generations` WHERE tenant_id = \\? AND user_id = \\? AND session_id = \\? AND idempotency_key = \\? LIMIT \\?").
		WithArgs(uint64(7), uint64(11), uint64(13), "channel:new", 1).
		WillReturnRows(manualRetrySourceRows().AddRow(20, "gen-existing", 7, 11, 13, nil, nil, imagegen.OperationGenerate, imagegen.GenerationStatusQueued, "private", "jiuan", "gpt-image-2", `{}`, nil, nil, "trace-new", "channel:new", "batch-new", imagegen.OriginTypeChannel, `{"version":2}`, "command:image-retry:gen-failed", "gen-failed", 3, now, nil))
	mock.ExpectCommit()

	got, created, err := repo.RetryImageGeneration(testContext(), imagegen.ManualRetryRequest{
		Scope: imagegen.JobScope{TenantID: 7, UserID: 11, SessionID: 13}, SourceGenerationID: "gen-failed",
		GenerationID: "gen-new", IdempotencyKey: "channel:new", Origin: imagegen.OriginMetadata{Type: imagegen.OriginTypeChannel, RefJSON: `{"version":2}`}, CreatedAt: now,
	})
	if err != nil || created || got.GenerationID != "gen-existing" {
		t.Fatalf("got=%+v created=%t err=%v", got, created, err)
	}
	assertExpectations(t, mock)
}

func TestFinishImageGenerationAttemptRequiresMatchingWorker(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectExec("UPDATE `image_generation_attempts` SET .*WHERE tenant_id = \\? AND generation_id = \\? AND attempt_no = \\? AND worker_id = \\?").WillReturnResult(sqlmock.NewResult(0, 0))
	err := repo.FinishImageGenerationAttempt(testContext(), ImageGenerationAttemptFinishInput{TenantID: 7, GenerationID: "gen-1", AttemptNo: 1, WorkerID: "old-worker", FinishedAt: time.Now().UTC(), Outcome: "failed", ErrorClass: "provider_unavailable", ErrorMessage: "temporary"})
	if !errors.Is(err, ErrImageGenerationLeaseLost) {
		t.Fatalf("err=%v", err)
	}
	assertExpectations(t, mock)
}

func TestFinishImageGenerationAttemptPersistsOutcomeFields(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	finished := time.Now().UTC()
	mock.ExpectExec("UPDATE `image_generation_attempts` SET .*WHERE tenant_id = \\? AND generation_id = \\? AND attempt_no = \\? AND worker_id = \\?").WithArgs(uint64(42), "provider_unavailable", "temporary", finished, "failed", "request-7", uint64(7), "gen-1", uint(2), "worker-a").WillReturnResult(sqlmock.NewResult(0, 1))
	err := repo.FinishImageGenerationAttempt(testContext(), ImageGenerationAttemptFinishInput{TenantID: 7, GenerationID: "gen-1", AttemptNo: 2, WorkerID: "worker-a", FinishedAt: finished, DurationMS: 42, Outcome: "failed", ErrorClass: "provider_unavailable", ErrorMessage: "temporary", ProviderRequestID: "request-7"})
	if err != nil {
		t.Fatal(err)
	}
	assertExpectations(t, mock)
}

func TestTransitionClaimedImageAttemptAtomicallyFinishesAttemptAndSchedulesRetry(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	finished := time.Now().UTC()
	next := finished.Add(30 * time.Second)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `image_generations` WHERE tenant_id = \\? AND generation_id = \\? AND status = \\? AND lease_owner = \\? LIMIT \\? FOR UPDATE").
		WithArgs(uint64(7), "gen-1", "running", "worker-a", 1).
		WillReturnRows(sqlmock.NewRows([]string{"cancel_requested_at"}).AddRow(nil))
	mock.ExpectExec("UPDATE `image_generation_attempts` SET .*WHERE tenant_id = \\? AND generation_id = \\? AND attempt_no = \\? AND worker_id = \\? AND finished_at IS NULL").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE `image_generations` SET .*WHERE \\(tenant_id = \\? AND generation_id = \\? AND status = \\? AND lease_owner = \\?\\) AND cancel_requested_at IS NULL").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	status, err := repo.TransitionClaimedImageAttempt(testContext(), imagegen.ImageGenerationAttemptTransition{
		Attempt: imagegen.ImageGenerationAttemptFinish{TenantID: 7, GenerationID: "gen-1", AttemptNo: 2, WorkerID: "worker-a", FinishedAt: finished, Outcome: "retry", ErrorClass: "provider_unavailable", ErrorMessage: "temporary"},
		Status:  imagegen.GenerationStatusRetry, NextAttemptAt: next,
	})
	if err != nil || status != imagegen.GenerationStatusRetry {
		t.Fatalf("status=%q err=%v", status, err)
	}
	assertExpectations(t, mock)
}

func TestTransitionClaimedImageAttemptConcurrentUserCancelWins(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	finished := time.Now().UTC()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `image_generations`").WillReturnRows(sqlmock.NewRows([]string{"cancel_requested_at"}).AddRow(finished.Add(-time.Second)))
	mock.ExpectExec("UPDATE `image_generation_attempts` SET .*WHERE tenant_id = \\? AND generation_id = \\? AND attempt_no = \\? AND worker_id = \\? AND finished_at IS NULL").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE `image_generations` SET .*WHERE \\(tenant_id = \\? AND generation_id = \\? AND status = \\? AND lease_owner = \\?\\) AND cancel_requested_at IS NOT NULL").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	status, err := repo.TransitionClaimedImageAttempt(testContext(), imagegen.ImageGenerationAttemptTransition{
		Attempt: imagegen.ImageGenerationAttemptFinish{TenantID: 7, GenerationID: "gen-1", AttemptNo: 1, WorkerID: "worker-a", FinishedAt: finished, Outcome: "failed", ErrorClass: "provider_rejected", ErrorMessage: "rejected"},
		Status:  imagegen.GenerationStatusFailed,
	})
	if err != nil || status != imagegen.GenerationStatusCancelled {
		t.Fatalf("status=%q err=%v", status, err)
	}
	assertExpectations(t, mock)
}

func TestTransitionClaimedImageAttemptRollsBackAttemptWhenGenerationTransitionFails(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	want := errors.New("generation update failed")
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `image_generations`").WillReturnRows(sqlmock.NewRows([]string{"cancel_requested_at"}).AddRow(nil))
	mock.ExpectExec("UPDATE `image_generation_attempts` SET").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE `image_generations` SET").WillReturnError(want)
	mock.ExpectRollback()
	_, err := repo.TransitionClaimedImageAttempt(testContext(), imagegen.ImageGenerationAttemptTransition{
		Attempt: imagegen.ImageGenerationAttemptFinish{TenantID: 7, GenerationID: "gen-1", AttemptNo: 1, WorkerID: "worker-a", FinishedAt: time.Now().UTC(), Outcome: "dead", ErrorClass: "provider_unavailable"},
		Status:  imagegen.GenerationStatusDead,
	})
	if !errors.Is(err, want) {
		t.Fatalf("err=%v", err)
	}
	assertExpectations(t, mock)
}

func TestTransitionClaimedImageAttemptCreatesCanonicalTerminalEventForChannelFailure(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	finished := time.Now().UTC()
	originJSON := `{"version":1,"account_id":3,"conversation_id":9,"run_id":"run-1"}`
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `image_generations`").WillReturnRows(sqlmock.NewRows([]string{"generation_id", "tenant_id", "origin_type", "origin_ref_json", "idempotency_key", "cancel_requested_at"}).AddRow("gen-1", 7, imagegen.OriginTypeChannel, originJSON, "channel:fixed", nil))
	mock.ExpectExec("UPDATE `image_generation_attempts` SET").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE `image_generations` SET").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO `image_completion_outbox` .*ON DUPLICATE KEY UPDATE").
		WithArgs(uint64(7), "gen-1", imagegen.CompletionEventImageTerminal, imagegen.OriginTypeChannel, originJSON, "channel:fixed", imagegen.CompletionDeliveryStatusPending, uint(0), finished, nil, nil, nil, nil, anyTime{}, nil).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	status, err := repo.TransitionClaimedImageAttempt(testContext(), imagegen.ImageGenerationAttemptTransition{
		Attempt: imagegen.ImageGenerationAttemptFinish{TenantID: 7, GenerationID: "gen-1", AttemptNo: 1, WorkerID: "worker-a", FinishedAt: finished, Outcome: imagegen.GenerationStatusFailed, ErrorClass: imagegen.ErrorClassProviderRejected},
		Status:  imagegen.GenerationStatusFailed,
	})
	if err != nil || status != imagegen.GenerationStatusFailed {
		t.Fatalf("status=%q err=%v", status, err)
	}
	assertExpectations(t, mock)
}

func TestCompleteImageGenerationAtomicallyLinksAssetAndCreatesEvent(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `image_generations` WHERE tenant_id = \\? AND generation_id = \\? AND status = \\? AND lease_owner = \\? LIMIT \\? FOR UPDATE").WithArgs(uint64(7), "gen-1", "running", "worker-a", 1).WillReturnRows(sqlmock.NewRows([]string{"id", "generation_id", "tenant_id", "user_id", "session_id", "origin_type", "origin_ref_json", "idempotency_key"}).AddRow(19, "gen-1", 7, 11, 13, "channel", `{"version":1}`, "channel:fixed"))
	mock.ExpectQuery("SELECT .* FROM `media_assets` WHERE tenant_id = \\? AND user_id = \\? AND session_id = \\? AND asset_id = \\? LIMIT \\?").WithArgs(uint64(7), uint64(11), uint64(13), "asset-1", 1).WillReturnRows(sqlmock.NewRows([]string{"asset_id", "tenant_id"}).AddRow("asset-1", 7))
	mock.ExpectExec("UPDATE `image_generations` SET .*WHERE tenant_id = \\? AND generation_id = \\? AND status = \\? AND lease_owner = \\?").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO `image_completion_outbox` .*ON DUPLICATE KEY UPDATE").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	err := repo.CompleteImageGeneration(testContext(), CompleteImageGenerationInput{TenantID: 7, GenerationID: "gen-1", WorkerID: "worker-a", AssetID: "asset-1", EventType: "image_completed"})
	if err != nil {
		t.Fatal(err)
	}
	assertExpectations(t, mock)
}

func TestCompleteClaimedImageAttemptIncludesAttemptInCompletionTransaction(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	finished := time.Now().UTC()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `image_generations`").WillReturnRows(sqlmock.NewRows([]string{"id", "generation_id", "tenant_id", "user_id", "session_id", "origin_type", "origin_ref_json", "idempotency_key", "cancel_requested_at"}).AddRow(19, "gen-1", 7, 11, 13, "channel", `{"version":1}`, "channel:fixed", nil))
	mock.ExpectQuery("SELECT .* FROM `media_assets`").WillReturnRows(sqlmock.NewRows([]string{"asset_id"}).AddRow("asset-1"))
	mock.ExpectExec("UPDATE `image_generation_attempts` SET .*finished_at.*outcome.*WHERE tenant_id = \\? AND generation_id = \\? AND attempt_no = \\? AND worker_id = \\? AND finished_at IS NULL").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE `image_generations` SET .*WHERE \\(tenant_id = \\? AND generation_id = \\? AND status = \\? AND lease_owner = \\?\\) AND cancel_requested_at IS NULL").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO `image_completion_outbox`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	status, err := repo.CompleteClaimedImageAttempt(testContext(), imagegen.CompleteClaimedImageAttemptRequest{
		Attempt: imagegen.ImageGenerationAttemptFinish{TenantID: 7, GenerationID: "gen-1", AttemptNo: 1, WorkerID: "worker-a", FinishedAt: finished, Outcome: imagegen.GenerationStatusCompleted},
		AssetID: "asset-1", EventType: imagegen.CompletionEventImageCompleted,
	})
	if err != nil || status != imagegen.GenerationStatusCompleted {
		t.Fatalf("status=%q err=%v", status, err)
	}
	assertExpectations(t, mock)
}

func TestCompleteClaimedImageAttemptCancellationRemovesMetadataAndCreatesTerminalEvent(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	finished := time.Now().UTC()
	mock.ExpectBegin()
	originJSON := `{"version":1,"account_id":3,"conversation_id":9,"run_id":"run-1"}`
	mock.ExpectQuery("SELECT .* FROM `image_generations`").WillReturnRows(sqlmock.NewRows([]string{"generation_id", "tenant_id", "user_id", "session_id", "origin_type", "origin_ref_json", "idempotency_key", "cancel_requested_at"}).AddRow("gen-1", 7, 11, 13, "channel", originJSON, "channel:fixed", finished.Add(-time.Second)))
	mock.ExpectQuery("SELECT .* FROM `media_assets`").WillReturnRows(sqlmock.NewRows([]string{"asset_id"}).AddRow("asset-1"))
	mock.ExpectExec("UPDATE `image_generation_attempts` SET").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE `image_generations` SET .*cancel_requested_at IS NOT NULL").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM `media_assets` WHERE tenant_id = \\? AND user_id = \\? AND session_id = \\? AND asset_id = \\?").WithArgs(uint64(7), uint64(11), uint64(13), "asset-1").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO `image_completion_outbox` .*ON DUPLICATE KEY UPDATE").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	status, err := repo.CompleteClaimedImageAttempt(testContext(), imagegen.CompleteClaimedImageAttemptRequest{
		Attempt: imagegen.ImageGenerationAttemptFinish{TenantID: 7, GenerationID: "gen-1", AttemptNo: 1, WorkerID: "worker-a", FinishedAt: finished, Outcome: imagegen.GenerationStatusCompleted},
		AssetID: "asset-1", EventType: imagegen.CompletionEventImageCompleted,
	})
	if err != nil || status != imagegen.GenerationStatusCancelled {
		t.Fatalf("status=%q err=%v", status, err)
	}
	assertExpectations(t, mock)
}

func TestFinalizeExhaustedImageGenerationsClosesStaleRunningAttemptWithoutProvider(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	now := time.Now().UTC()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `image_generations` WHERE tenant_id = \\? AND status = \\? AND attempts >= max_attempts AND lease_until IS NOT NULL AND lease_until < \\? LIMIT \\? FOR UPDATE").
		WillReturnRows(sqlmock.NewRows([]string{"generation_id", "tenant_id", "attempts", "lease_owner", "origin_type", "origin_ref_json", "idempotency_key", "cancel_requested_at"}).AddRow("gen-stale", 7, 3, "worker-dead", imagegen.OriginTypeChannel, `{"version":1,"account_id":3,"conversation_id":9,"run_id":"run-1"}`, "channel:fixed", nil))
	mock.ExpectExec("UPDATE `image_generation_attempts` SET .*WHERE tenant_id = \\? AND generation_id = \\? AND attempt_no = \\? AND worker_id = \\? AND finished_at IS NULL").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE `image_generations` SET .*WHERE \\(tenant_id = \\? AND generation_id = \\? AND status = \\? AND lease_owner = \\? AND lease_until < \\?\\) AND cancel_requested_at IS NULL").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO `image_completion_outbox` .*ON DUPLICATE KEY UPDATE").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	count, err := repo.FinalizeExhaustedImageGenerations(testContext(), 7, now)
	if err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	assertExpectations(t, mock)
}

func TestRequestImageGenerationCancelCreatesTerminalEventForQueuedChannelJob(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `image_generations` .*FOR UPDATE").
		WillReturnRows(sqlmock.NewRows([]string{"generation_id", "tenant_id", "user_id", "session_id", "status", "origin_type", "origin_ref_json", "idempotency_key"}).AddRow("gen-queued", 7, 11, 13, imagegen.GenerationStatusQueued, imagegen.OriginTypeChannel, `{"version":1,"account_id":3,"conversation_id":9,"run_id":"run-1"}`, "channel:fixed"))
	mock.ExpectExec("UPDATE `image_generations` SET").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO `image_completion_outbox` .*ON DUPLICATE KEY UPDATE").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	changed, err := repo.RequestImageGenerationCancel(testContext(), 7, 11, 13, "gen-queued")
	if err != nil || !changed {
		t.Fatalf("changed=%t err=%v", changed, err)
	}
	assertExpectations(t, mock)
}

func TestCountLegacyOrphanedImageGenerationsUsesExactTenantCutoffAndNullLeaseGuards(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	cutoff := time.Date(2026, time.September, 2, 9, 0, 0, 0, time.UTC)
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `image_generations` WHERE tenant_id = \\? AND status = \\? AND lease_owner IS NULL AND lease_until IS NULL AND created_at < \\?").
		WithArgs(uint64(7), imagegen.GenerationStatusRunning, cutoff).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	count, err := repo.CountLegacyOrphanedImageGenerations(testContext(), 7, cutoff)
	if err != nil || count != 2 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	assertExpectations(t, mock)
}

func TestFailLegacyOrphanedImageGenerationsTransitionsOnlyGuardedRows(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	cutoff := time.Date(2026, time.September, 2, 9, 0, 0, 0, time.UTC)
	mock.ExpectExec("UPDATE `image_generations` SET .*error_class.*error_message.*finished_at.*lease_owner.*lease_until.*next_attempt_at.*status.*WHERE tenant_id = \\? AND status = \\? AND lease_owner IS NULL AND lease_until IS NULL AND created_at < \\?").
		WithArgs(imagegen.ErrorClassLegacyOrphaned, nil, "legacy image generation was not leased by an async worker", sqlmock.AnyArg(), nil, nil, nil, nil, nil, imagegen.GenerationStatusFailed, sqlmock.AnyArg(), uint64(7), imagegen.GenerationStatusRunning, cutoff).
		WillReturnResult(sqlmock.NewResult(0, 2))
	count, err := repo.FailLegacyOrphanedImageGenerations(testContext(), 7, cutoff)
	if err != nil || count != 2 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	assertExpectations(t, mock)
}

func TestCompleteImageGenerationRejectsCrossScopeAsset(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `image_generations` WHERE tenant_id = \\? AND generation_id = \\? AND status = \\? AND lease_owner = \\? LIMIT \\? FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"generation_id", "tenant_id", "user_id", "session_id", "origin_type", "idempotency_key"}).AddRow("gen-1", 7, 11, 13, "channel", "channel:fixed"))
	mock.ExpectQuery("SELECT .* FROM `media_assets` WHERE tenant_id = \\? AND user_id = \\? AND session_id = \\? AND asset_id = \\? LIMIT \\?").WillReturnRows(sqlmock.NewRows([]string{"asset_id"}))
	mock.ExpectRollback()
	err := repo.CompleteImageGeneration(testContext(), CompleteImageGenerationInput{TenantID: 7, GenerationID: "gen-1", WorkerID: "worker-a", AssetID: "asset-other-user", EventType: "image_completed"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
	assertExpectations(t, mock)
}

func TestCompleteImageGenerationRejectsCrossUserAsset(t *testing.T) {
	testCompleteImageGenerationRejectsScopedAsset(t, 11, 13, "asset-other-user")
}

func TestCompleteImageGenerationRejectsCrossSessionAsset(t *testing.T) {
	testCompleteImageGenerationRejectsScopedAsset(t, 11, 13, "asset-other-session")
}

func testCompleteImageGenerationRejectsScopedAsset(t *testing.T, userID, sessionID uint64, assetID string) {
	t.Helper()
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `image_generations`").WillReturnRows(sqlmock.NewRows([]string{"generation_id", "tenant_id", "user_id", "session_id", "origin_type", "idempotency_key"}).AddRow("gen-1", 7, userID, sessionID, "channel", "channel:fixed"))
	mock.ExpectQuery("SELECT .* FROM `media_assets` WHERE tenant_id = \\? AND user_id = \\? AND session_id = \\? AND asset_id = \\? LIMIT \\?").WithArgs(uint64(7), userID, sessionID, assetID, 1).WillReturnRows(sqlmock.NewRows([]string{"asset_id"}))
	mock.ExpectRollback()
	err := repo.CompleteImageGeneration(testContext(), CompleteImageGenerationInput{TenantID: 7, GenerationID: "gen-1", WorkerID: "worker-a", AssetID: assetID, EventType: "image_completed"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
	assertExpectations(t, mock)
}

func TestCompleteImageGenerationRollsBackOutboxInsertFailure(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	outboxErr := errors.New("outbox insert failed")
	rollbackErr := errors.New("rollback failed")
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `image_generations`").WillReturnRows(sqlmock.NewRows([]string{"generation_id", "tenant_id", "user_id", "session_id", "origin_type", "origin_ref_json", "idempotency_key"}).AddRow("gen-1", 7, 11, 13, "channel", `{"version":1,"account_id":3,"conversation_id":9,"run_id":"run-1"}`, "channel:fixed"))
	mock.ExpectQuery("SELECT .* FROM `media_assets`").WillReturnRows(sqlmock.NewRows([]string{"asset_id"}).AddRow("asset-1"))
	mock.ExpectExec("UPDATE `image_generations` SET").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO `image_completion_outbox`").WillReturnError(outboxErr)
	mock.ExpectRollback().WillReturnError(rollbackErr)
	err := repo.CompleteImageGeneration(testContext(), CompleteImageGenerationInput{TenantID: 7, GenerationID: "gen-1", WorkerID: "worker-a", AssetID: "asset-1", EventType: "image_completed"})
	if !errors.Is(err, outboxErr) || !errors.Is(err, rollbackErr) {
		t.Fatalf("err=%v", err)
	}
	assertExpectations(t, mock)
}

func TestClaimDueImageGenerationsReturnsRollbackFailure(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	selectErr := errors.New("select failed")
	rollbackErr := errors.New("rollback failed")
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `image_generations`").WillReturnError(selectErr)
	mock.ExpectRollback().WillReturnError(rollbackErr)
	_, err := repo.ClaimDueImageGenerations(testContext(), 7, "worker-a", 1, time.Now().UTC().Add(time.Minute))
	if !errors.Is(err, selectErr) || !errors.Is(err, rollbackErr) {
		t.Fatalf("err=%v", err)
	}
	assertExpectations(t, mock)
}

func TestSafeImageErrorMessageRedactsAndPreservesUTF8Boundary(t *testing.T) {
	message := "prompt=very secret Authorization: Bearer top-secret https://private.example.test/file data:image/png;base64,QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVo= " + strings.Repeat("图", 600)
	got := safeImageErrorMessage(message)
	if len(got) > 1024 || !utf8.ValidString(got) || strings.Contains(got, "top-secret") || strings.Contains(got, "private.example.test") || strings.Contains(got, "very secret") || strings.Contains(got, "QUJDREV") {
		t.Fatalf("unsafe message=%q", got)
	}
}

func TestImageGenerationRepositoryImplementsJobContracts(t *testing.T) {
	var _ ImageJobRepository = (*GormRepository)(nil)
	var _ ImageCompletionOutboxRepository = (*GormRepository)(nil)
	var _ imagegen.ImageWorkerRepository = (*GormRepository)(nil)
	var _ imagegen.CompletionDispatcherRepository = (*GormRepository)(nil)
}

func TestImageGenerationQueueStatsUsesTenantScopedActiveStatusAggregation(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	oldest := time.Date(2026, time.September, 2, 1, 2, 3, 0, time.UTC)
	mock.ExpectQuery("SELECT COALESCE\\(SUM\\(CASE WHEN status IN \\(\\?, \\?\\) THEN 1 ELSE 0 END\\), 0\\) AS queue_depth.*FROM `image_generations` WHERE tenant_id = \\?").
		WithArgs(imagegen.GenerationStatusQueued, imagegen.GenerationStatusRetry, imagegen.GenerationStatusRunning, imagegen.GenerationStatusQueued, imagegen.GenerationStatusRetry, uint64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"queue_depth", "running", "oldest_queued_at"}).AddRow(4, 2, oldest))

	stats, err := repo.ImageGenerationQueueStats(testContext(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if stats.QueueDepth != 4 || stats.Running != 2 || stats.OldestQueuedAt == nil || !stats.OldestQueuedAt.Equal(oldest) {
		t.Fatalf("stats = %+v", stats)
	}
	assertExpectations(t, mock)
}

func TestImageGenerationQueueStatsRejectsMissingTenant(t *testing.T) {
	repo := NewGormRepository(nil, nil)
	if _, err := repo.ImageGenerationQueueStats(testContext(), 0); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("error = %v, want ErrInvalidInput", err)
	}
}

func TestClaimDueImageCompletionEventsLeasesDueEvent(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	leaseUntil := time.Now().UTC().Add(time.Minute)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `image_completion_outbox` WHERE tenant_id = \\? AND \\(\\(status IN \\(\\?, \\?\\).*\\) OR \\(status = \\? AND lease_until IS NOT NULL AND lease_until < \\?\\)\\) ORDER BY next_attempt_at ASC, id ASC LIMIT \\? FOR UPDATE").WillReturnRows(imageCompletionOutboxRows().AddRow(3, 7, "gen-1", "image_completed", "channel", nil, "generation:gen-1", "pending", 0, nil, nil, nil, nil, nil, time.Now().UTC(), nil))
	mock.ExpectExec("UPDATE `image_completion_outbox` SET .* WHERE tenant_id = \\? AND id IN \\(\\?\\) AND \\(\\(status IN \\(\\?, \\?\\).*\\) OR \\(status = \\? AND lease_until IS NOT NULL AND lease_until < \\?\\)\\)").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	got, err := repo.ClaimDueImageCompletionEvents(testContext(), 7, "dispatcher-a", 1, leaseUntil)
	if err != nil || len(got) != 1 || got[0].Status != "sending" || got[0].LeaseOwner != "dispatcher-a" || got[0].Attempts != 1 {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	assertExpectations(t, mock)
}

func TestClaimDueImageCompletionEventsReclaimsExpiredSendingAfterCrash(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	now := time.Now().UTC()
	leaseUntil := now.Add(time.Minute)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `image_completion_outbox` WHERE tenant_id = \\? AND \\(\\(status IN \\(\\?, \\?\\).*\\) OR \\(status = \\? AND lease_until IS NOT NULL AND lease_until < \\?\\)\\) ORDER BY next_attempt_at ASC, id ASC LIMIT \\? FOR UPDATE").
		WillReturnRows(imageCompletionOutboxRows().AddRow(4, 7, "gen-crashed", imagegen.CompletionEventImageCompleted, imagegen.OriginTypeChannel, nil, "channel:crashed", imagegen.CompletionDeliveryStatusSending, 1, nil, "dispatcher-dead", now.Add(-time.Second), nil, nil, now.Add(-time.Minute), nil))
	mock.ExpectExec("UPDATE `image_completion_outbox` SET .* WHERE tenant_id = \\? AND id IN \\(\\?\\) AND \\(\\(status IN \\(\\?, \\?\\).*\\) OR \\(status = \\? AND lease_until IS NOT NULL AND lease_until < \\?\\)\\)").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	got, err := repo.ClaimDueImageCompletionEvents(testContext(), 7, "dispatcher-new", 1, leaseUntil)
	if err != nil || len(got) != 1 || got[0].Status != imagegen.CompletionDeliveryStatusSending || got[0].LeaseOwner != "dispatcher-new" || got[0].Attempts != 2 || got[0].LeaseUntil == nil || !got[0].LeaseUntil.Equal(leaseUntil) {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	assertExpectations(t, mock)
}

func TestClaimDueImageCompletionEventsReturnsRollbackFailure(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	selectErr := errors.New("outbox select failed")
	rollbackErr := errors.New("outbox rollback failed")
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `image_completion_outbox`").WillReturnError(selectErr)
	mock.ExpectRollback().WillReturnError(rollbackErr)
	_, err := repo.ClaimDueImageCompletionEvents(testContext(), 7, "dispatcher-a", 1, time.Now().UTC().Add(time.Minute))
	if !errors.Is(err, selectErr) || !errors.Is(err, rollbackErr) {
		t.Fatalf("err=%v", err)
	}
	assertExpectations(t, mock)
}

func TestMarkImageCompletionEventRetryRequiresLeaseOwner(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectExec("UPDATE `image_completion_outbox` SET .*WHERE tenant_id = \\? AND id = \\? AND status = \\? AND lease_owner = \\?").WillReturnResult(sqlmock.NewResult(0, 0))
	err := repo.MarkImageCompletionEventRetry(testContext(), 7, 3, "dispatcher-a", time.Now().UTC().Add(time.Minute), "not_ready", "wait")
	if !errors.Is(err, ErrImageGenerationLeaseLost) {
		t.Fatalf("err=%v", err)
	}
	assertExpectations(t, mock)
}

func TestMarkImageCompletionEventRetryWritesSchedule(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	next := time.Now().UTC().Add(time.Minute)
	mock.ExpectExec("UPDATE `image_completion_outbox` SET .*next_attempt_at.*status.*WHERE tenant_id = \\? AND id = \\? AND status = \\? AND lease_owner = \\?").WithArgs("not_ready", "wait", nil, nil, next, "retry", uint64(7), uint64(3), "sending", "dispatcher-a").WillReturnResult(sqlmock.NewResult(0, 1))
	if err := repo.MarkImageCompletionEventRetry(testContext(), 7, 3, "dispatcher-a", next, "not_ready", "wait"); err != nil {
		t.Fatal(err)
	}
	assertExpectations(t, mock)
}

func TestMarkImageCompletionEventSentRequiresLeaseOwner(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectExec("UPDATE `image_completion_outbox` SET .*WHERE tenant_id = \\? AND id = \\? AND status = \\? AND lease_owner = \\?").WillReturnResult(sqlmock.NewResult(0, 0))
	err := repo.MarkImageCompletionEventSent(testContext(), 7, 3, "dispatcher-a")
	if !errors.Is(err, ErrImageGenerationLeaseLost) {
		t.Fatalf("err=%v", err)
	}
	assertExpectations(t, mock)
}

func TestMarkImageCompletionEventSentClearsLease(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectExec("UPDATE `image_completion_outbox` SET .*lease_owner.*lease_until.*sent_at.*status.*WHERE tenant_id = \\? AND id = \\? AND status = \\? AND lease_owner = \\?").WithArgs(nil, nil, nil, nil, anyTime{}, "sent", uint64(7), uint64(3), "sending", "dispatcher-a").WillReturnResult(sqlmock.NewResult(0, 1))
	if err := repo.MarkImageCompletionEventSent(testContext(), 7, 3, "dispatcher-a"); err != nil {
		t.Fatal(err)
	}
	assertExpectations(t, mock)
}

func TestMarkImageCompletionEventDeadRequiresLeaseOwner(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectExec("UPDATE `image_completion_outbox` SET .*WHERE tenant_id = \\? AND id = \\? AND status = \\? AND lease_owner = \\?").WillReturnResult(sqlmock.NewResult(0, 0))
	err := repo.MarkImageCompletionEventDead(testContext(), 7, 3, "dispatcher-a", "delivery_dead", "stopped")
	if !errors.Is(err, ErrImageGenerationLeaseLost) {
		t.Fatalf("err=%v", err)
	}
	assertExpectations(t, mock)
}

func TestMarkImageCompletionEventDeadClearsLease(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectExec("UPDATE `image_completion_outbox` SET .*lease_owner.*lease_until.*status.*WHERE tenant_id = \\? AND id = \\? AND status = \\? AND lease_owner = \\?").WithArgs("dead", "stopped", nil, nil, "dead", uint64(7), uint64(3), "sending", "dispatcher-a").WillReturnResult(sqlmock.NewResult(0, 1))
	if err := repo.MarkImageCompletionEventDead(testContext(), 7, 3, "dispatcher-a", "dead", "stopped"); err != nil {
		t.Fatal(err)
	}
	assertExpectations(t, mock)
}

func imageGenerationRows(now time.Time) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "generation_id", "tenant_id", "user_id", "session_id", "asset_id", "source_asset_id", "operation", "status", "prompt", "provider", "model", "request_json", "error_code", "error_message", "trace_id", "idempotency_key", "created_at", "finished_at"})
}

func manualRetrySourceRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "generation_id", "tenant_id", "user_id", "session_id", "asset_id", "source_asset_id", "operation", "status", "prompt", "provider", "model", "request_json", "error_code", "error_message", "trace_id", "idempotency_key", "batch_id", "origin_type", "origin_ref_json", "tool_use_id", "retry_of_generation_id", "max_attempts", "created_at", "finished_at",
	})
}

func imageCompletionOutboxRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "tenant_id", "generation_id", "event_type", "origin_type", "origin_ref_json", "idempotency_key", "status", "attempts", "next_attempt_at", "lease_owner", "lease_until", "last_error_code", "last_error_message", "created_at", "sent_at"})
}

func queuedImageGenerationArgs() []driver.Value {
	return []driver.Value{
		"gen-new", uint64(7), uint64(11), uint64(13), nil, nil,
		"generate", "queued", "a cat", "jiuan", "gpt-image-2", nil, nil, nil, nil, "channel:fixed",
		nil, "direct", nil, nil, uint(0), uint(3), anyTime{}, nil, nil, nil, nil, nil, nil, nil, nil, anyTime{}, anyTime{}, nil,
	}
}

func admittedGenerationRecord() imagegen.GenerationRecord {
	now := time.Now().UTC()
	return imagegen.GenerationRecord{GenerationID: "gen-new", TenantID: 7, UserID: 11, SessionID: 13, Operation: imagegen.OperationGenerate, Status: imagegen.GenerationStatusQueued, Prompt: "a cat", Provider: "jiuan", Model: "gpt-image-2", IdempotencyKey: "channel:fixed", OriginType: imagegen.OriginTypeChannel, MaxAttempts: 3, CreatedAt: now, UpdatedAt: now}
}

type anyTime struct{}

func (anyTime) Match(value driver.Value) bool {
	_, ok := value.(time.Time)
	return ok
}
