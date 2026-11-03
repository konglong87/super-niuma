package tenant

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/observability"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

func TestMySQLE2ETenantServiceLifecycle(t *testing.T) {
	dsn := os.Getenv("NIUMA_MYSQL_E2E_DSN")
	if dsn == "" {
		t.Skip("set NIUMA_MYSQL_E2E_DSN to run real MySQL tenant e2e")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	migrator, err := mysqlstore.NewMigrator(mysqlstore.MigrationOptions{DSN: dsn, Path: mysqlE2EMigrationsPath(t)})
	if err != nil {
		t.Fatal(err)
	}
	if err := migrator.Up(); err != nil {
		_ = migrator.Close()
		t.Fatal(err)
	}
	if err := migrator.Close(); err != nil {
		t.Fatal(err)
	}

	repo, err := mysqlstore.OpenGormRepository(ctx, dsn, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = repo.Close()
	}()
	svc := NewService(repo, nil)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	userKey := "user-mysql-e2e-" + suffix
	traceID := "trace-mysql-e2e-" + suffix
	reqCtx := observability.WithRequestValues(ctx, traceID, userKey, "yutang")

	userID, err := svc.SaveCurrentUser(reqCtx, UserRequest{
		Email:        userKey + "@example.test",
		DisplayName:  "MySQL E2E User",
		Role:         "owner",
		Status:       "active",
		UserInfoJSON: `{"suite":"mysql_e2e"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	current, err := svc.GetCurrentUser(reqCtx)
	if err != nil {
		t.Fatal(err)
	}
	if current.ID != userID || current.UserKey != userKey || current.Role != "owner" {
		t.Fatalf("current user = %+v userID=%d", current, userID)
	}

	tenantKey := "tenant-mysql-e2e-" + suffix
	tenantID, err := svc.SaveTenant(reqCtx, TenantRequest{TenantKey: tenantKey, Name: "Tenant MySQL E2E", Status: "active", SettingsJSON: `{"suite":"mysql_e2e"}`})
	if err != nil {
		t.Fatal(err)
	}
	tenants, err := svc.ListTenants(reqCtx, 100)
	if err != nil {
		t.Fatal(err)
	}
	foundTenant := false
	for _, tenant := range tenants {
		if tenant.ID == tenantID && tenant.TenantKey == tenantKey {
			foundTenant = true
			break
		}
	}
	if !foundTenant {
		t.Fatalf("created tenant %d/%s not found in %+v", tenantID, tenantKey, tenants)
	}
	tenantPage, err := svc.ListTenantsPage(reqCtx, ListOptions{Limit: 1, Search: tenantKey})
	if err != nil {
		t.Fatal(err)
	}
	if len(tenantPage.Data) != 1 || tenantPage.Data[0].TenantKey != tenantKey || tenantPage.HasMore {
		t.Fatalf("tenant page = %+v", tenantPage)
	}
	if err := svc.ArchiveTenant(reqCtx, tenantKey); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.UpsertMemory(reqCtx, MemoryRequest{MemoryKey: "pref.lang." + suffix, Category: "profile", Content: "Go", Importance: 9, Source: "e2e"}); err != nil {
		t.Fatal(err)
	}
	memories, err := svc.ListMemories(reqCtx, "profile", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(memories) == 0 || memories[0].Content != "Go" {
		t.Fatalf("memories = %+v", memories)
	}

	if _, err := svc.SaveDocument(reqCtx, DocumentRequest{DocType: "CLAUDE.md", Title: "E2E", ContentMD: "# E2E", Version: 1, Active: true}); err != nil {
		t.Fatal(err)
	}
	doc, err := svc.GetActiveDocument(reqCtx, "CLAUDE.md")
	if err != nil {
		t.Fatal(err)
	}
	if doc.ContentMD != "# E2E" || !doc.Active {
		t.Fatalf("doc = %+v", doc)
	}

	sessionID, err := svc.UpsertSession(reqCtx, SessionRequest{SessionKey: "sess-mysql-e2e-" + suffix, Title: "MySQL E2E", Status: "active", Model: "claude-test", CWD: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveProfile(reqCtx, ProfileRequest{ProfileVersion: 1, Summary: "mysql e2e", ProfileJSON: `{"lang":"go"}`, GeneratedFromSessionID: sessionID}); err != nil {
		t.Fatal(err)
	}
	profile, err := svc.GetProfile(reqCtx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if profile.GeneratedFromSessionID != sessionID || !jsonFieldEquals(t, profile.ProfileJSON, "lang", "go") {
		t.Fatalf("profile = %+v", profile)
	}

	if _, err := svc.UpsertMessage(reqCtx, MessageRequest{SessionID: sessionID, TurnIndex: 1, Role: "user", Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	messages, err := svc.ListMessages(reqCtx, sessionID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].TraceID != traceID || messages[0].Content != "hello" {
		t.Fatalf("messages = %+v trace=%s", messages, traceID)
	}

	taskID, err := svc.CreateAgentTask(reqCtx, agenttasks.TaskInput{ParentSessionID: sessionID, SubagentSessionKey: "sub-" + suffix, AgentName: "reviewer", Description: "review", Prompt: "check", Model: "claude-test", TraceID: traceID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AppendAgentTaskEvent(reqCtx, agenttasks.EventInput{TaskID: taskID, EventType: agenttasks.EventStarted, PayloadJSON: `{"turn":1}`, TraceID: traceID}); err != nil {
		t.Fatal(err)
	}
	if err := svc.CancelAgentTask(reqCtx, taskID, `{"source":"mysql_e2e","cancelled":true}`); err != nil {
		t.Fatal(err)
	}
	tasks, err := svc.ListAgentTasks(reqCtx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) == 0 || tasks[0].ID != taskID || tasks[0].Status != agenttasks.StatusCancelled {
		t.Fatalf("tasks = %+v", tasks)
	}
	events, err := svc.ListAgentTaskEvents(reqCtx, taskID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) < 2 || events[len(events)-1].EventType != agenttasks.EventCancelled {
		t.Fatalf("events = %+v", events)
	}

	if _, err := svc.RecordAudit(reqCtx, AuditRequest{Action: "tenant.mysql_e2e", ResourceType: "session", ResourceID: strconv.FormatUint(sessionID, 10)}); err != nil {
		t.Fatal(err)
	}
	audit, err := svc.ListAuditLogs(reqCtx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(audit) == 0 || audit[0].TraceID != traceID {
		t.Fatalf("audit = %+v trace=%s", audit, traceID)
	}
}

// TestMySQLE2ESaveQueryTurnRollsBackHalfWrittenSession 是 TODO-070 唯一一处
// 真正在 InnoDB 上验证「第一张表也没留下行」的测试：助手消息的 role 超过
// VARCHAR(32)，严格模式下报 1406（不是死锁，不会被 withRetryableTransaction
// 重试），事务整体回滚，会话行必须一起消失。
func TestMySQLE2ESaveQueryTurnRollsBackHalfWrittenSession(t *testing.T) {
	dsn := os.Getenv("NIUMA_MYSQL_E2E_DSN")
	if dsn == "" {
		t.Skip("set NIUMA_MYSQL_E2E_DSN to run real MySQL tenant e2e")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	migrator, err := mysqlstore.NewMigrator(mysqlstore.MigrationOptions{DSN: dsn, Path: mysqlE2EMigrationsPath(t)})
	if err != nil {
		t.Fatal(err)
	}
	if err := migrator.Up(); err != nil {
		_ = migrator.Close()
		t.Fatal(err)
	}
	if err := migrator.Close(); err != nil {
		t.Fatal(err)
	}

	repo, err := mysqlstore.OpenGormRepository(ctx, dsn, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = repo.Close()
	}()
	svc := NewService(repo, nil)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	userKey := "user-turn-rollback-" + suffix
	reqCtx := observability.WithRequestValues(ctx, "trace-turn-rollback-"+suffix, userKey, "yutang")
	if _, err := svc.SaveCurrentUser(reqCtx, UserRequest{
		Email:       userKey + "@example.test",
		DisplayName: "Turn Rollback E2E",
		Role:        "owner",
		Status:      "active",
	}); err != nil {
		t.Fatal(err)
	}

	sessionKey := "session-turn-rollback-" + suffix
	if _, err := svc.SaveQueryTurn(reqCtx, QueryTurnRequest{
		Session:   SessionRequest{SessionKey: sessionKey, Title: "Rollback", Model: "claude"},
		User:      MessageRequest{TurnIndex: 1, Role: "user", Content: "hello"},
		Assistant: MessageRequest{TurnIndex: 2, Role: strings.Repeat("x", 64), Content: "hi"},
	}); err == nil {
		t.Fatal("expected the oversized assistant role to fail the turn")
	}

	sessions, err := svc.ListSessions(reqCtx, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range sessions {
		if session.SessionKey == sessionKey {
			t.Fatalf("half-written session survived the rollback: %+v", session)
		}
	}

	// 同一批重放，这次三行都合法：确认回滚没有把这条路径写死。
	out, err := svc.SaveQueryTurn(reqCtx, QueryTurnRequest{
		Session:   SessionRequest{SessionKey: sessionKey, Title: "Rollback", Model: "claude"},
		User:      MessageRequest{TurnIndex: 1, Role: "user", Content: "hello"},
		Assistant: MessageRequest{TurnIndex: 2, Role: "assistant", Content: "hi"},
	})
	if err != nil {
		t.Fatal(err)
	}
	messages, err := svc.ListMessages(reqCtx, out.SessionID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 {
		t.Fatalf("messages = %+v", messages)
	}
}

// TestMySQLE2EForkSessionRollsBackHalfCopiedBranch 是 TODO-106 在真 InnoDB 上的
// 对照测试：第 3 条消息的 role 超过 VARCHAR(32)，严格模式下报 1406（不是死锁，
// 不会被 withRetryableTransaction 重试），前两条已复制的消息和分支会话行必须一起消失。
func TestMySQLE2EForkSessionRollsBackHalfCopiedBranch(t *testing.T) {
	dsn := os.Getenv("NIUMA_MYSQL_E2E_DSN")
	if dsn == "" {
		t.Skip("set NIUMA_MYSQL_E2E_DSN to run real MySQL tenant e2e")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	migrator, err := mysqlstore.NewMigrator(mysqlstore.MigrationOptions{DSN: dsn, Path: mysqlE2EMigrationsPath(t)})
	if err != nil {
		t.Fatal(err)
	}
	if err := migrator.Up(); err != nil {
		_ = migrator.Close()
		t.Fatal(err)
	}
	if err := migrator.Close(); err != nil {
		t.Fatal(err)
	}

	repo, err := mysqlstore.OpenGormRepository(ctx, dsn, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = repo.Close()
	}()
	svc := NewService(repo, nil)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	userKey := "user-fork-rollback-" + suffix
	reqCtx := observability.WithRequestValues(ctx, "trace-fork-rollback-"+suffix, userKey, "yutang")
	if _, err := svc.SaveCurrentUser(reqCtx, UserRequest{
		Email:       userKey + "@example.test",
		DisplayName: "Fork Rollback E2E",
		Role:        "owner",
		Status:      "active",
	}); err != nil {
		t.Fatal(err)
	}

	sessionKey := "session-fork-rollback-" + suffix
	if _, err := svc.ForkSession(reqCtx, ForkSessionRequest{
		Session: SessionRequest{SessionKey: sessionKey, Title: "Branch", Model: "claude"},
		Messages: []MessageRequest{
			{TurnIndex: 1, Role: "user", Content: "hello"},
			{TurnIndex: 2, Role: "assistant", Content: "hi"},
			{TurnIndex: 3, Role: strings.Repeat("x", 64), Content: "boom"},
		},
	}); err == nil {
		t.Fatal("expected the oversized role on message 3 to fail the fork")
	}

	sessions, err := svc.ListSessions(reqCtx, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range sessions {
		if session.SessionKey == sessionKey {
			t.Fatalf("half-copied branch session survived the rollback: %+v", session)
		}
	}

	// 同一批重放，这次全部合法：确认回滚没有把这条路径写死。
	out, err := svc.ForkSession(reqCtx, ForkSessionRequest{
		Session: SessionRequest{SessionKey: sessionKey, Title: "Branch", Model: "claude"},
		Messages: []MessageRequest{
			{TurnIndex: 1, Role: "user", Content: "hello"},
			{TurnIndex: 2, Role: "assistant", Content: "hi"},
			{TurnIndex: 3, Role: "user", Content: "again"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.CopiedMessages != 3 {
		t.Fatalf("out = %+v", out)
	}
	messages, err := svc.ListMessages(reqCtx, out.SessionID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 3 {
		t.Fatalf("messages = %+v", messages)
	}
}

func jsonFieldEquals(t *testing.T, raw, key string, want any) bool {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		t.Fatalf("decode json %q: %v", raw, err)
	}
	return value[key] == want
}

func mysqlE2EMigrationsPath(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "migrations", "mysql")
}
