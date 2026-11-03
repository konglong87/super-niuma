package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/goal"
	"github.com/konglong87/super-niuma/internal/observability"
	"github.com/konglong87/super-niuma/internal/query"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	tenantservice "github.com/konglong87/super-niuma/internal/tenant"
	"github.com/konglong87/super-niuma/internal/tools"
	skilltool "github.com/konglong87/super-niuma/internal/tools/skill"
)

func TestMySQLE2ETenantAPILifecycle(t *testing.T) {
	dsn := os.Getenv("NIUMA_MYSQL_E2E_DSN")
	if dsn == "" {
		t.Skip("set NIUMA_MYSQL_E2E_DSN to run real MySQL tenant API e2e")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	migrator, err := mysqlstore.NewMigrator(mysqlstore.MigrationOptions{DSN: dsn, Path: serverMySQLE2EMigrationsPath(t)})
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
	handler := NewHandler(Options{AuthToken: "token", Workspace: "/workspace", TenantService: tenantservice.NewService(repo, nil), SessionTitleFunc: func(context.Context, string, string) (string, error) {
		return "API Query E2E", nil
	}}, func(_ context.Context, req QueryRequest) (query.Result, error) {
		return query.Result{Response: "query answer", Model: req.Model, Usage: query.Usage{InputTokens: 2, OutputTokens: 3}}, nil
	})
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	userKey := "api-mysql-e2e-" + suffix
	traceID := "trace-api-mysql-e2e-" + suffix

	userResp := tenantAPIRequest(t, handler, http.MethodPatch, "/tenant/user", `{"email":"`+userKey+`@example.test","display_name":"API E2E","role":"owner","status":"active","user_info_json":"{\"suite\":\"api_mysql_e2e\"}"}`, userKey, traceID)
	assertStatus(t, userResp, http.StatusOK)

	tenantKey := "tenant-api-mysql-e2e-" + suffix
	tenantResp := tenantAPIRequest(t, handler, http.MethodPost, "/tenant/tenants", `{"tenant_key":"`+tenantKey+`","name":"Tenant API E2E","status":"active","settings_json":"{\"suite\":\"api_mysql_e2e\"}"}`, userKey, traceID)
	assertStatus(t, tenantResp, http.StatusOK)
	if jsonUint(t, tenantResp.Body.Bytes(), "id") == 0 {
		t.Fatalf("tenant response = %s", tenantResp.Body.String())
	}
	tenantsResp := tenantAPIRequest(t, handler, http.MethodGet, "/tenant/tenants?limit=100", "", userKey, traceID)
	assertStatus(t, tenantsResp, http.StatusOK)
	if !strings.Contains(tenantsResp.Body.String(), tenantKey) {
		t.Fatalf("tenants response = %s", tenantsResp.Body.String())
	}
	filteredTenantsResp := tenantAPIRequest(t, handler, http.MethodGet, "/tenant/tenants?limit=1&search="+tenantKey, "", userKey, traceID)
	assertStatus(t, filteredTenantsResp, http.StatusOK)
	if !strings.Contains(filteredTenantsResp.Body.String(), tenantKey) || !strings.Contains(filteredTenantsResp.Body.String(), `"has_more":false`) {
		t.Fatalf("filtered tenants response = %s", filteredTenantsResp.Body.String())
	}
	archiveTenantResp := tenantAPIRequest(t, handler, http.MethodDelete, "/tenant/tenants?tenant_key="+tenantKey, "", userKey, traceID)
	assertStatus(t, archiveTenantResp, http.StatusOK)
	if !strings.Contains(archiveTenantResp.Body.String(), `"archived":true`) {
		t.Fatalf("archive tenant response = %s", archiveTenantResp.Body.String())
	}

	sessionResp := tenantAPIRequest(t, handler, http.MethodPost, "/tenant/sessions", `{"session_key":"sess-api-mysql-e2e-`+suffix+`","title":"API MySQL E2E","status":"active","model":"claude-test","cwd":"/workspace"}`, userKey, traceID)
	assertStatus(t, sessionResp, http.StatusOK)
	sessionID := jsonUint(t, sessionResp.Body.Bytes(), "id")
	if sessionID == 0 {
		t.Fatalf("session response = %s", sessionResp.Body.String())
	}

	messageResp := tenantAPIRequest(t, handler, http.MethodPost, "/tenant/messages", `{"session_id":`+strconv.FormatUint(sessionID, 10)+`,"turn_index":1,"role":"user","content":"hello from api e2e"}`, userKey, traceID)
	assertStatus(t, messageResp, http.StatusOK)
	if jsonUint(t, messageResp.Body.Bytes(), "id") == 0 {
		t.Fatalf("message response = %s", messageResp.Body.String())
	}

	sessionsResp := tenantAPIRequest(t, handler, http.MethodGet, "/tenant/sessions?limit=5", "", userKey, traceID)
	assertStatus(t, sessionsResp, http.StatusOK)
	if !strings.Contains(sessionsResp.Body.String(), "sess-api-mysql-e2e-"+suffix) {
		t.Fatalf("sessions response = %s", sessionsResp.Body.String())
	}

	messagesResp := tenantAPIRequest(t, handler, http.MethodGet, "/tenant/messages?session_id="+strconv.FormatUint(sessionID, 10)+"&limit=5", "", userKey, traceID)
	assertStatus(t, messagesResp, http.StatusOK)
	if !strings.Contains(messagesResp.Body.String(), "hello from api e2e") || !strings.Contains(messagesResp.Body.String(), traceID) {
		t.Fatalf("messages response = %s", messagesResp.Body.String())
	}

	auditResp := tenantAPIRequest(t, handler, http.MethodGet, "/tenant/audit?limit=10", "", userKey, traceID)
	assertStatus(t, auditResp, http.StatusOK)
	if !strings.Contains(auditResp.Body.String(), "tenant.message.upsert") || !strings.Contains(auditResp.Body.String(), traceID) {
		t.Fatalf("audit response = %s", auditResp.Body.String())
	}

	querySessionKey := "query-api-mysql-e2e-" + suffix
	queryResp := tenantAPIRequest(t, handler, http.MethodPost, "/query", `{"prompt":"hello query persistence","model":"claude-test","session_key":"`+querySessionKey+`"}`, userKey, traceID)
	assertStatus(t, queryResp, http.StatusOK)
	if !strings.Contains(queryResp.Body.String(), `"response":"query answer"`) {
		t.Fatalf("query response = %s", queryResp.Body.String())
	}
	querySessionsResp := tenantAPIRequest(t, handler, http.MethodGet, "/tenant/sessions?limit=20", "", userKey, traceID)
	assertStatus(t, querySessionsResp, http.StatusOK)
	querySessionID := sessionIDByKey(t, querySessionsResp.Body.Bytes(), querySessionKey)
	if querySessionID == 0 || !strings.Contains(querySessionsResp.Body.String(), "API Query E2E") {
		t.Fatalf("query sessions response = %s", querySessionsResp.Body.String())
	}
	queryMessagesResp := tenantAPIRequest(t, handler, http.MethodGet, "/tenant/messages?session_id="+strconv.FormatUint(querySessionID, 10)+"&limit=10", "", userKey, traceID)
	assertStatus(t, queryMessagesResp, http.StatusOK)
	if !strings.Contains(queryMessagesResp.Body.String(), "hello query persistence") || !strings.Contains(queryMessagesResp.Body.String(), "query answer") {
		t.Fatalf("query messages response = %s", queryMessagesResp.Body.String())
	}
}

func TestMySQLE2EP0MediaAssetAndTurnUsageReadback(t *testing.T) {
	dsn := os.Getenv("NIUMA_MYSQL_E2E_DSN")
	if dsn == "" {
		t.Skip("set NIUMA_MYSQL_E2E_DSN to run real P0 media/usage e2e")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	migrator, err := mysqlstore.NewMigrator(mysqlstore.MigrationOptions{DSN: dsn, Path: serverMySQLE2EMigrationsPath(t)})
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
	defer func() { _ = repo.Close() }()
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	tenantID, err := repo.UpsertTenant(ctx, mysqlstore.TenantInput{TenantKey: "p0-media-usage-" + suffix, Name: "P0 media usage", Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	userID, err := repo.EnsureUser(ctx, tenantID, "p0-user-"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err := repo.UpsertSession(ctx, mysqlstore.SessionInput{TenantID: tenantID, UserID: userID, SessionKey: "p0-session-" + suffix, Status: "active", Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpsertMediaAsset(ctx, mysqlstore.MediaAssetInput{AssetID: "p0-asset-" + suffix, TenantID: tenantID, UserID: userID, SessionID: sessionID, Kind: "image", MediaType: "image/png", State: "uploading", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", AccessJSON: `{"tenant_id":1}`}); err != nil {
		t.Fatal(err)
	}
	asset, err := repo.GetMediaAsset(ctx, tenantID, userID, sessionID, "p0-asset-"+suffix)
	if err != nil || asset.State != "uploading" || asset.SessionID != sessionID {
		t.Fatalf("asset=%+v err=%v", asset, err)
	}
	otherUserID, err := repo.EnsureUser(ctx, tenantID, "p0-other-"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetMediaAsset(ctx, tenantID, otherUserID, sessionID, "p0-asset-"+suffix); err == nil {
		t.Fatal("cross-user media read should fail")
	}
	requestID := "p0-usage-" + suffix
	if _, err := repo.InsertUsageLedger(ctx, mysqlstore.UsageLedgerInput{RequestID: requestID, TenantID: tenantID, UserID: userID, SessionID: sessionID, Source: "query", Model: "test-model", Provider: "local", Turn: 2, UsageSource: "provider", Status: "running", Estimated: true, StartedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateUsageLedgerSettlement(ctx, requestID, mysqlstore.UsageLedgerInput{Provider: "local", Turn: 2, UsageSource: "provider", Status: "succeeded", InputTokens: 10, OutputTokens: 4, CacheCreationEphemeral1h: 3, TotalTokens: 17}); err != nil {
		t.Fatal(err)
	}
	ledgers, err := repo.ListTenantUsageLedger(ctx, tenantID, mysqlstore.ListOptions{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	var found mysqlstore.UsageLedger
	for _, item := range ledgers {
		if item.RequestID == requestID {
			found = item
			break
		}
	}
	if found.Provider != "local" || found.Turn != 2 || found.UsageSource != "provider" || found.CacheCreationEphemeral1h != 3 || found.Status != "succeeded" {
		t.Fatalf("ledger readback = %+v", found)
	}
}

func TestMySQLE2EMobileChatLifecycle(t *testing.T) {
	dsn := os.Getenv("NIUMA_MYSQL_E2E_DSN")
	if dsn == "" {
		t.Skip("set NIUMA_MYSQL_E2E_DSN to run real MySQL mobile chat e2e")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	migrator, err := mysqlstore.NewMigrator(mysqlstore.MigrationOptions{DSN: dsn, Path: serverMySQLE2EMigrationsPath(t)})
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
	secret := "mobile-e2e-secret"
	handler := NewHandler(Options{
		MobileJWTSecret: secret,
		TenantService:   tenantservice.NewService(repo, nil),
		StreamQueryFunc: func(_ context.Context, req QueryRequest, sink io.Writer) (query.Result, error) {
			if !strings.Contains(req.Prompt, "hello mobile") {
				t.Fatalf("prompt = %q", req.Prompt)
			}
			_, _ = sink.Write([]byte("mobile answer"))
			return query.Result{Response: "mobile answer", Model: req.Model, Usage: query.Usage{OutputTokens: 2}}, nil
		},
	}, nil)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	claims := mobileClaims{TenantKey: "yutang", UserID: "mobile-mysql-e2e-" + suffix, DeviceID: "ios-e2e", ExpiresAt: time.Now().Add(time.Hour).Unix()}
	traceID := "trace-mobile-mysql-e2e-" + suffix

	createReq := mobileRequestWithClaims(t, http.MethodPost, "/mobile/chat/sessions", `{"session_key":"mobile-api-mysql-e2e-`+suffix+`","title":"Mobile API E2E","status":"active","model":"claude-test","cwd":"/workspace"}`, secret, claims)
	createReq.Header.Set("x-trace-id", traceID)
	createRec := httptest.NewRecorder()
	handler.ServeHTTP(createRec, createReq)
	assertStatus(t, createRec, http.StatusOK)
	sessionID := jsonUint(t, createRec.Body.Bytes(), "id")
	if sessionID == 0 {
		t.Fatalf("create response = %s", createRec.Body.String())
	}

	streamReq := mobileRequestWithClaims(t, http.MethodPost, "/mobile/chat/sessions/"+strconv.FormatUint(sessionID, 10)+"/messages/stream", `{"content":"hello mobile","model":"claude-test","message_key":"msg-mobile-e2e-`+suffix+`"}`, secret, claims)
	streamReq.Header.Set("x-trace-id", traceID)
	streamRec := httptest.NewRecorder()
	handler.ServeHTTP(streamRec, streamReq)
	assertStatus(t, streamRec, http.StatusOK)
	if !strings.Contains(streamRec.Body.String(), `"type":"message_stop"`) || !strings.Contains(streamRec.Body.String(), "mobile answer") {
		t.Fatalf("stream response = %s", streamRec.Body.String())
	}

	listReq := mobileRequestWithClaims(t, http.MethodGet, "/mobile/chat/sessions/"+strconv.FormatUint(sessionID, 10)+"/messages?limit=10", "", secret, claims)
	listReq.Header.Set("x-trace-id", traceID)
	listRec := httptest.NewRecorder()
	handler.ServeHTTP(listRec, listReq)
	assertStatus(t, listRec, http.StatusOK)
	if !strings.Contains(listRec.Body.String(), "hello mobile") || !strings.Contains(listRec.Body.String(), "mobile answer") || !strings.Contains(listRec.Body.String(), traceID) {
		t.Fatalf("messages response = %s", listRec.Body.String())
	}
}

func TestMySQLE2ETenantSkillsRuntime(t *testing.T) {
	dsn := os.Getenv("NIUMA_MYSQL_E2E_DSN")
	if dsn == "" {
		t.Skip("set NIUMA_MYSQL_E2E_DSN to run real MySQL tenant skills runtime e2e")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	migrator, err := mysqlstore.NewMigrator(mysqlstore.MigrationOptions{DSN: dsn, Path: serverMySQLE2EMigrationsPath(t)})
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
	svc := tenantservice.NewService(repo, nil)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	userKey := "skill-runtime-mysql-e2e-" + suffix
	traceID := "trace-skill-runtime-mysql-e2e-" + suffix
	reqCtx := tenantRequestContext(ctx, userKey, traceID)

	if _, err := svc.SaveCurrentUser(reqCtx, tenantservice.UserRequest{
		Email:       userKey + "@example.test",
		DisplayName: "Skill Runtime E2E",
		Role:        "owner",
		Status:      "active",
	}); err != nil {
		t.Fatal(err)
	}
	enabled := true
	skillKey := "tenant-runtime-" + suffix
	skillBody := "---\ndescription: Tenant runtime skill " + suffix + "\neffort: high\nallowed-tools:\n  - Skill\n---\n# Tenant Runtime\n\nTenant skill body " + suffix
	if _, err := svc.UpsertSkill(reqCtx, tenantservice.SkillRequest{
		SkillKey:  skillKey,
		Name:      "Tenant Runtime " + suffix,
		ContentMD: skillBody,
		Version:   1,
		Enabled:   &enabled,
	}); err != nil {
		t.Fatal(err)
	}
	sessionID, err := svc.UpsertSession(reqCtx, tenantservice.SessionRequest{
		SessionKey: "skill-runtime-session-" + suffix,
		Title:      "Tenant Skill Runtime E2E",
		Status:     "active",
		Model:      "claude-test",
		CWD:        "/workspace",
	})
	if err != nil {
		t.Fatal(err)
	}

	streamer := &tenantSkillRuntimeStreamer{skillKey: skillKey, suffix: suffix}
	querySession := query.New(streamer, tools.NewRegistry(skilltool.New()), query.Options{
		Model:           "claude-test",
		MaxTurns:        3,
		MaxTokens:       5000,
		CWD:             t.TempDir(),
		PromptMode:      "chat",
		TenantSessionID: sessionID,
		TraceID:         traceID,
		SkillProvider:   TenantSkillProvider{Service: svc},
	})
	var out strings.Builder
	result, err := querySession.Run(reqCtx, "use the tenant runtime skill", &out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Response, "tenant skill runtime ok") || !strings.Contains(out.String(), "tenant skill runtime ok") {
		t.Fatalf("result=%+v out=%q", result, out.String())
	}
	if streamer.calls != 2 || !streamer.sawMetadata || !streamer.sawLoadedBody || !streamer.sawThinking {
		t.Fatalf("streamer calls=%d sawMetadata=%v sawLoadedBody=%v sawThinking=%v systems=%+v", streamer.calls, streamer.sawMetadata, streamer.sawLoadedBody, streamer.sawThinking, streamer.systems)
	}

	if _, err := svc.UpsertMessage(reqCtx, tenantservice.MessageRequest{SessionID: sessionID, TurnIndex: 1, Role: "user", Content: "use the tenant runtime skill", TraceID: traceID}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpsertMessage(reqCtx, tenantservice.MessageRequest{SessionID: sessionID, TurnIndex: 2, Role: "assistant", Content: result.Response, TraceID: traceID, Model: result.Model}); err != nil {
		t.Fatal(err)
	}
	messages, err := svc.ListMessages(reqCtx, sessionID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || !strings.Contains(messages[1].Content, "tenant skill runtime ok") || messages[1].TraceID != traceID {
		t.Fatalf("messages = %+v", messages)
	}

	sqlDB, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = sqlDB.Close()
	}()
	var storedSkillContent, storedAssistant string
	row := sqlDB.QueryRowContext(ctx, `
SELECT s.content_md, m.content
FROM tenant_skills s
JOIN tenants t ON t.id = s.tenant_id
JOIN tenant_session_messages m ON m.tenant_id = t.id
JOIN tenant_sessions sess ON sess.id = m.session_id
JOIN tenant_users u ON u.id = m.user_id
WHERE t.tenant_key = ? AND u.user_key = ? AND s.skill_key = ? AND sess.id = ? AND m.role = 'assistant'
ORDER BY m.id DESC LIMIT 1`, "yutang", userKey, skillKey, sessionID)
	if err := row.Scan(&storedSkillContent, &storedAssistant); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(storedSkillContent, "Tenant skill body "+suffix) || !strings.Contains(storedAssistant, "tenant skill runtime ok") {
		t.Fatalf("stored skill=%q assistant=%q", storedSkillContent, storedAssistant)
	}
}

func TestMySQLE2EOpenAIStructuredTenantSkillRouting(t *testing.T) {
	dsn := os.Getenv("NIUMA_MYSQL_E2E_DSN")
	if dsn == "" {
		t.Skip("set NIUMA_MYSQL_E2E_DSN to run real MySQL structured tenant skill routing e2e")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	migrator, err := mysqlstore.NewMigrator(mysqlstore.MigrationOptions{DSN: dsn, Path: serverMySQLE2EMigrationsPath(t)})
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
	svc := tenantservice.NewService(repo, nil)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	userKey := "structured-routing-mysql-e2e-" + suffix
	traceID := "trace-structured-routing-mysql-e2e-" + suffix
	reqCtx := tenantRequestContext(ctx, userKey, traceID)

	if _, err := svc.SaveCurrentUser(reqCtx, tenantservice.UserRequest{
		Email:       userKey + "@example.test",
		DisplayName: "Structured Routing E2E",
		Role:        "owner",
		Status:      "active",
	}); err != nil {
		t.Fatal(err)
	}
	skillKey := "teach-v2-" + suffix
	if _, err := svc.SaveTenant(reqCtx, tenantservice.TenantRequest{
		TenantKey:    "yutang",
		Name:         "Yutang",
		Status:       "active",
		SettingsJSON: `{"structured_skill_routes":[{"schema_name":"teach_decision_v1","skill_key":"` + skillKey + `"}]}`,
	}); err != nil {
		t.Fatal(err)
	}
	packageDir := filepath.Join(t.TempDir(), "teach-v2-package")
	if err := os.MkdirAll(filepath.Join(packageDir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageDir, "SKILL.md"), []byte("# Teach V2\n\nCalibration Slots\n\nKnowledge Transfer Teaching\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageDir, "docs", "runtime.md"), []byte("unique structured body "+suffix+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(packageDir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageDir, "scripts", "ignored.sh"), []byte("echo do-not-inline\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	enabled := true
	published, err := svc.PublishSkillPackage(reqCtx, tenantservice.SkillPackageRequest{
		SkillKey:    skillKey,
		Name:        "Teach V2 " + suffix,
		Description: "Structured teach skill",
		SourcePath:  packageDir,
		StoreRoot:   t.TempDir(),
		Enabled:     &enabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	if published.PackageSHA256 == "" || published.PackageRef == "" || published.RuntimeRef == "" {
		t.Fatalf("published package missing refs: %+v", published)
	}

	streamer := &structuredTenantSkillStreamer{suffix: suffix}
	handler := NewHandler(Options{
		AuthToken:     "token",
		Workspace:     "/workspace",
		TenantService: svc,
	}, func(ctx context.Context, req QueryRequest) (query.Result, error) {
		querySession := query.New(streamer, tools.NewRegistry(), query.Options{
			Model:                   req.Model,
			MaxTurns:                req.MaxTurns,
			MaxTokens:               req.MaxTokens,
			CWD:                     t.TempDir(),
			PromptMode:              req.PromptMode,
			TraceID:                 observability.TraceID(ctx),
			SkillProvider:           TenantSkillProvider{Service: svc},
			DisableTools:            req.DisableTools,
			InlineTenantSkills:      req.InlineTenantSkills,
			InlineTenantSkillSource: req.InlineTenantSkillSource,
			ResponseFormat:          serverResponseFormatForE2E(req.ResponseFormat),
			TenantContextManifest:   query.TenantContextManifest{Active: true, Addendum: true},
		})
		return querySession.Run(ctx, req.Prompt, io.Discard)
	})

	body := strings.NewReader(`{
		"model":"claude-test",
		"messages":[
			{"role":"system","content":"Use teach_context_v1."},
			{"role":"user","content":"{\"contract_version\":\"teach_context_v1\",\"authoritative_state\":{\"calibration\":{\"missing_slots\":[\"familiar_domain\"]}}}"}
		],
		"response_format":{
			"type":"json_schema",
			"json_schema":{"name":"teach_decision_v1","strict":true,"schema":{"type":"object"}}
		}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body)
	req.Header.Set("authorization", "Bearer token")
	req.Header.Set("X-Tenant-Key", "yutang")
	req.Header.Set("X-User-Id", userKey)
	req.Header.Set("X-Trace-Id", traceID)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)
	var response struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		TenantRuntime *query.TenantRuntimeManifest `json:"tenant_runtime"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Choices) != 1 || !strings.Contains(response.Choices[0].Message.Content, `"skill_version":2`) {
		t.Fatalf("response = %s", rec.Body.String())
	}
	if response.TenantRuntime == nil || !response.TenantRuntime.Active || !response.TenantRuntime.Resolved {
		t.Fatalf("missing tenant_runtime response metadata: %s", rec.Body.String())
	}
	if got := strings.Join(response.TenantRuntime.LoadedKeys, ","); got != skillKey {
		t.Fatalf("tenant_runtime.loaded_keys = %q", got)
	}
	if got := strings.Join(response.TenantRuntime.PackageSHA256, ","); got != published.PackageSHA256 {
		t.Fatalf("tenant_runtime.package_sha256 = %q want %q", got, published.PackageSHA256)
	}
	if got := rec.Header().Get("X-Tenant-Skill-Keys"); got != skillKey {
		t.Fatalf("X-Tenant-Skill-Keys = %q", got)
	}
	if got := rec.Header().Get("X-Tenant-Skill-Package-SHA256"); got != published.PackageSHA256 {
		t.Fatalf("X-Tenant-Skill-Package-SHA256 = %q want %q", got, published.PackageSHA256)
	}
	if !streamer.sawTeachV2 || !streamer.sawStructuredBody {
		t.Fatalf("streamer did not see inline teach-v2 skill: %+v systems=%+v", streamer, streamer.systems)
	}

	sqlDB, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = sqlDB.Close()
	}()
	var promptContextCount int
	row := sqlDB.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM tenant_telemetry_events e
JOIN tenants t ON t.id = e.tenant_id
WHERE t.tenant_key = ? AND e.trace_id = ? AND e.event_name = 'query.prompt_context'
  AND e.properties_json LIKE ? AND e.properties_json LIKE ? AND e.properties_json LIKE ? AND e.properties_json LIKE ?`,
		"yutang", traceID, "%tenant_skill_inline%", "%"+skillKey+"%", "%"+published.PackageSHA256+"%", "%file://%")
	if err := row.Scan(&promptContextCount); err != nil {
		t.Fatal(err)
	}
	if promptContextCount == 0 {
		t.Fatal("missing tenant prompt_context telemetry for structured inline skill")
	}
	var runtimeActive, runtimeResolved, tenantContextActive bool
	var runtimeKeys, runtimeVersions, runtimeSHA string
	row = sqlDB.QueryRowContext(ctx, `
SELECT
  JSON_EXTRACT(e.properties_json, '$.context_manifest.tenant_runtime.active'),
  JSON_EXTRACT(e.properties_json, '$.context_manifest.tenant_runtime.resolved'),
  JSON_EXTRACT(e.properties_json, '$.context_manifest.tenant_context.active'),
  JSON_EXTRACT(e.properties_json, '$.context_manifest.tenant_runtime.loaded_keys'),
  JSON_EXTRACT(e.properties_json, '$.context_manifest.tenant_runtime.versions'),
  JSON_EXTRACT(e.properties_json, '$.context_manifest.tenant_runtime.package_sha256')
FROM tenant_telemetry_events e
JOIN tenants t ON t.id = e.tenant_id
WHERE t.tenant_key = ? AND e.trace_id = ? AND e.event_name = 'query.prompt_context'
ORDER BY e.id DESC LIMIT 1`,
		"yutang", traceID)
	if err := row.Scan(&runtimeActive, &runtimeResolved, &tenantContextActive, &runtimeKeys, &runtimeVersions, &runtimeSHA); err != nil {
		t.Fatal(err)
	}
	if !runtimeActive || !runtimeResolved || !tenantContextActive {
		t.Fatalf("runtime telemetry flags active=%v resolved=%v tenant_context_active=%v", runtimeActive, runtimeResolved, tenantContextActive)
	}
	if !strings.Contains(runtimeKeys, skillKey) || !strings.Contains(runtimeVersions, `"1"`) || !strings.Contains(runtimeSHA, published.PackageSHA256) {
		t.Fatalf("runtime telemetry metadata keys=%s versions=%s sha=%s", runtimeKeys, runtimeVersions, runtimeSHA)
	}
}

func TestMySQLE2ETenantSkillPackageVerifyRuntime(t *testing.T) {
	dsn := os.Getenv("NIUMA_MYSQL_E2E_DSN")
	if dsn == "" {
		t.Skip("set NIUMA_MYSQL_E2E_DSN to run real MySQL skill package verify runtime e2e")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	migrator, err := mysqlstore.NewMigrator(mysqlstore.MigrationOptions{DSN: dsn, Path: serverMySQLE2EMigrationsPath(t)})
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
	svc := tenantservice.NewService(repo, nil)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	userKey := "verify-runtime-mysql-e2e-" + suffix
	traceID := "trace-verify-runtime-mysql-e2e-" + suffix
	reqCtx := tenantRequestContext(ctx, userKey, traceID)

	if _, err := svc.SaveCurrentUser(reqCtx, tenantservice.UserRequest{
		Email:       userKey + "@example.test",
		DisplayName: "Verify Runtime E2E",
		Role:        "owner",
		Status:      "active",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveTenant(reqCtx, tenantservice.TenantRequest{
		TenantKey: "yutang",
		Name:      "Yutang",
		Status:    "active",
	}); err != nil {
		t.Fatal(err)
	}
	skillKey := "teach-v2-" + suffix
	packageDir := filepath.Join(t.TempDir(), "teach-v2-package")
	if err := os.MkdirAll(filepath.Join(packageDir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageDir, "SKILL.md"), []byte("# Teach V2\n\nCalibration Slots\n\nKnowledge Transfer Teaching\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageDir, "docs", "runtime.md"), []byte("unique structured body "+suffix+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	enabled := true
	published, err := svc.PublishSkillPackage(reqCtx, tenantservice.SkillPackageRequest{
		SkillKey:    skillKey,
		Name:        "Teach V2 " + suffix,
		Description: "Structured teach skill",
		SourcePath:  packageDir,
		StoreRoot:   t.TempDir(),
		Enabled:     &enabled,
	})
	if err != nil {
		t.Fatal(err)
	}

	streamer := &structuredTenantSkillStreamer{suffix: suffix}
	handler := NewHandler(Options{
		AuthToken:     "token",
		Workspace:     "/workspace",
		TenantService: svc,
	}, func(ctx context.Context, req QueryRequest) (query.Result, error) {
		querySession := query.New(streamer, tools.NewRegistry(), query.Options{
			Model:                   req.Model,
			MaxTurns:                req.MaxTurns,
			MaxTokens:               req.MaxTokens,
			CWD:                     t.TempDir(),
			PromptMode:              req.PromptMode,
			TraceID:                 observability.TraceID(ctx),
			SkillProvider:           TenantSkillProvider{Service: svc},
			DisableTools:            req.DisableTools,
			InlineTenantSkills:      req.InlineTenantSkills,
			InlineTenantSkillSource: req.InlineTenantSkillSource,
			ResponseFormat:          serverResponseFormatForE2E(req.ResponseFormat),
			TenantContextManifest:   query.TenantContextManifest{Active: true, Addendum: true},
		})
		return querySession.Run(ctx, req.Prompt, io.Discard)
	})

	verifyResp := tenantAPIRequest(t, handler, http.MethodPost, "/tenant/skill-packages/verify-runtime", `{"skill_key":"`+skillKey+`","schema_name":"teach_decision_v1","expected_package_sha256":"`+published.PackageSHA256+`","expected_version":`+strconv.FormatUint(uint64(published.Version), 10)+`}`, userKey, traceID)
	assertStatus(t, verifyResp, http.StatusOK)
	var response SkillPackageVerifyResponse
	if err := json.Unmarshal(verifyResp.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.OK || response.TenantRuntime == nil || !response.TenantRuntime.HasMetadata() {
		t.Fatalf("verify response = %s", verifyResp.Body.String())
	}
	if !stringSliceContains(response.TenantRuntime.LoadedKeys, skillKey) || !stringSliceContains(response.TenantRuntime.PackageSHA256, published.PackageSHA256) || !stringSliceContains(response.TenantRuntime.Versions, strconv.FormatUint(uint64(published.Version), 10)) {
		t.Fatalf("verify tenant runtime metadata = %+v", response.TenantRuntime)
	}
	if !streamer.sawTeachV2 || !streamer.sawStructuredBody {
		t.Fatalf("streamer did not see verify-runtime inline skill: %+v systems=%+v", streamer, streamer.systems)
	}

	sqlDB, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = sqlDB.Close()
	}()
	var promptContextCount int
	row := sqlDB.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM tenant_telemetry_events e
JOIN tenants t ON t.id = e.tenant_id
WHERE t.tenant_key = ? AND e.trace_id = ? AND e.event_name = 'query.prompt_context'
  AND e.properties_json LIKE ? AND e.properties_json LIKE ? AND e.properties_json LIKE ?`,
		"yutang", traceID, "%verify_runtime%", "%"+skillKey+"%", "%"+published.PackageSHA256+"%")
	if err := row.Scan(&promptContextCount); err != nil {
		t.Fatal(err)
	}
	if promptContextCount == 0 {
		t.Fatal("missing tenant prompt_context telemetry for verify-runtime skill")
	}
}

func TestMySQLE2ETenantDataIsolation(t *testing.T) {
	dsn := os.Getenv("NIUMA_MYSQL_E2E_DSN")
	if dsn == "" {
		t.Skip("set NIUMA_MYSQL_E2E_DSN to run real MySQL tenant isolation e2e")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	migrator, err := mysqlstore.NewMigrator(mysqlstore.MigrationOptions{DSN: dsn, Path: serverMySQLE2EMigrationsPath(t)})
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
	handler := NewHandler(Options{AuthToken: "token", Workspace: "/workspace", TenantService: tenantservice.NewService(repo, nil)}, nil)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	tenantA := "yutang"
	tenantB := "tenant-isolation-b-" + suffix
	userA := "isolation-a-" + suffix
	userB := "isolation-b-" + suffix
	userC := "isolation-c-" + suffix
	traceID := "trace-isolation-mysql-e2e-" + suffix
	skillKey := "isolation-skill-" + suffix

	assertStatus(t, tenantAPIRequestForTenant(t, handler, http.MethodPatch, "/tenant/user", `{"email":"`+userA+`@example.test","display_name":"Isolation A","role":"owner","status":"active"}`, tenantA, userA, traceID), http.StatusOK)
	assertStatus(t, tenantAPIRequestForTenant(t, handler, http.MethodPost, "/tenant/tenants", `{"tenant_key":"`+tenantB+`","name":"Isolation Tenant B","status":"active"}`, tenantA, userA, traceID), http.StatusOK)
	assertStatus(t, tenantAPIRequestForTenant(t, handler, http.MethodPatch, "/tenant/user", `{"email":"`+userB+`@example.test","display_name":"Isolation B","role":"owner","status":"active"}`, tenantB, userB, traceID), http.StatusOK)
	assertStatus(t, tenantAPIRequestForTenant(t, handler, http.MethodPatch, "/tenant/user", `{"email":"`+userC+`@example.test","display_name":"Isolation C","role":"member","status":"active"}`, tenantA, userC, traceID), http.StatusOK)

	enabled := true
	skillPayload, err := json.Marshal(tenantservice.SkillRequest{
		SkillKey:    skillKey,
		Name:        "Isolation Skill " + suffix,
		Description: "tenant A only",
		ContentMD:   "# Isolation\nTenant A only " + suffix,
		Version:     1,
		Enabled:     &enabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertStatus(t, tenantAPIRequestForTenant(t, handler, http.MethodPost, "/tenant/skills", string(skillPayload), tenantA, userA, traceID), http.StatusOK)

	sessionResp := tenantAPIRequestForTenant(t, handler, http.MethodPost, "/tenant/sessions", `{"session_key":"isolation-session-`+suffix+`","title":"Isolation Session","status":"active","model":"claude-test","cwd":"/workspace"}`, tenantA, userA, traceID)
	assertStatus(t, sessionResp, http.StatusOK)
	sessionID := jsonUint(t, sessionResp.Body.Bytes(), "id")
	if sessionID == 0 {
		t.Fatalf("session response = %s", sessionResp.Body.String())
	}
	ownedMessage := `{"session_id":` + strconv.FormatUint(sessionID, 10) + `,"turn_index":1,"role":"user","content":"tenant A private message"}`
	assertStatus(t, tenantAPIRequestForTenant(t, handler, http.MethodPost, "/tenant/messages", ownedMessage, tenantA, userA, traceID), http.StatusOK)

	crossTenantList := tenantAPIRequestForTenant(t, handler, http.MethodGet, "/tenant/messages?session_id="+strconv.FormatUint(sessionID, 10)+"&limit=10", "", tenantB, userB, traceID)
	assertStatus(t, crossTenantList, http.StatusOK)
	if strings.Contains(crossTenantList.Body.String(), "tenant A private message") {
		t.Fatalf("cross tenant message list leaked data: %s", crossTenantList.Body.String())
	}
	crossTenantWrite := tenantAPIRequestForTenant(t, handler, http.MethodPost, "/tenant/messages", `{"session_id":`+strconv.FormatUint(sessionID, 10)+`,"turn_index":1,"role":"assistant","content":"cross tenant overwrite"}`, tenantB, userB, traceID)
	assertStatus(t, crossTenantWrite, http.StatusNotFound)
	sameTenantDifferentUserWrite := tenantAPIRequestForTenant(t, handler, http.MethodPost, "/tenant/messages", `{"session_id":`+strconv.FormatUint(sessionID, 10)+`,"turn_index":1,"role":"assistant","content":"same tenant overwrite"}`, tenantA, userC, traceID)
	assertStatus(t, sameTenantDifferentUserWrite, http.StatusNotFound)

	crossTenantSkill := tenantAPIRequestForTenant(t, handler, http.MethodGet, "/tenant/skills?skill_key="+skillKey, "", tenantB, userB, traceID)
	assertStatus(t, crossTenantSkill, http.StatusNotFound)
	crossTenantEffective := tenantAPIRequestForTenant(t, handler, http.MethodGet, "/tenant/effective-skills?enabled=true&limit=50", "", tenantB, userB, traceID)
	assertStatus(t, crossTenantEffective, http.StatusOK)
	if strings.Contains(crossTenantEffective.Body.String(), skillKey) {
		t.Fatalf("cross tenant effective skills leaked %s: %s", skillKey, crossTenantEffective.Body.String())
	}

	sqlDB, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = sqlDB.Close()
	}()
	var role, content string
	var messageCount int
	row := sqlDB.QueryRowContext(ctx, `
SELECT COUNT(*), MAX(role), MAX(content)
FROM tenant_session_messages
WHERE session_id = ?`, sessionID)
	if err := row.Scan(&messageCount, &role, &content); err != nil {
		t.Fatal(err)
	}
	if messageCount != 1 || role != "user" || content != "tenant A private message" {
		t.Fatalf("message row was modified count=%d role=%q content=%q", messageCount, role, content)
	}
	var tenantBSkillCount int
	row = sqlDB.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM tenant_skills s
JOIN tenants t ON t.id = s.tenant_id
WHERE t.tenant_key = ? AND s.skill_key = ?`, tenantB, skillKey)
	if err := row.Scan(&tenantBSkillCount); err != nil {
		t.Fatal(err)
	}
	if tenantBSkillCount != 0 {
		t.Fatalf("tenant B unexpectedly has skill %s count=%d", skillKey, tenantBSkillCount)
	}
}

func TestMySQLE2ETenantSkillRollbackConcurrent(t *testing.T) {
	dsn := os.Getenv("NIUMA_MYSQL_E2E_DSN")
	if dsn == "" {
		t.Skip("set NIUMA_MYSQL_E2E_DSN to run real MySQL tenant rollback concurrency e2e")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	migrator, err := mysqlstore.NewMigrator(mysqlstore.MigrationOptions{DSN: dsn, Path: serverMySQLE2EMigrationsPath(t)})
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
	svc := tenantservice.NewService(repo, nil)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	userKey := "rollback-concurrent-" + suffix
	traceID := "trace-rollback-concurrent-" + suffix
	reqCtx := tenantRequestContext(ctx, userKey, traceID)

	if _, err := svc.SaveCurrentUser(reqCtx, tenantservice.UserRequest{
		Email:       userKey + "@example.test",
		DisplayName: "Rollback Concurrent E2E",
		Role:        "owner",
		Status:      "active",
	}); err != nil {
		t.Fatal(err)
	}
	enabled := true
	skillKey := "rollback-concurrent-" + suffix
	for _, seed := range []struct {
		version uint
		content string
	}{
		{version: 1, content: "# Rollback\nversion one " + suffix},
		{version: 2, content: "# Rollback\nversion two " + suffix},
	} {
		if _, err := svc.UpsertSkill(reqCtx, tenantservice.SkillRequest{
			SkillKey:  skillKey,
			Name:      "Rollback Concurrent " + suffix,
			ContentMD: seed.content,
			Version:   seed.version,
			Enabled:   &enabled,
		}); err != nil {
			t.Fatal(err)
		}
	}

	start := make(chan struct{})
	results := make(chan tenantservice.SkillRollbackResult, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, err := svc.RollbackSkill(reqCtx, tenantservice.SkillRollbackRequest{SkillKey: skillKey, Version: 1})
			if err != nil {
				errs <- err
				return
			}
			results <- result
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	versions := map[uint]bool{}
	for result := range results {
		versions[result.Version] = true
		if result.FromVersion != 1 || result.SkillKey != skillKey {
			t.Fatalf("rollback result = %+v", result)
		}
	}
	if !versions[3] || !versions[4] || len(versions) != 2 {
		t.Fatalf("rollback versions = %+v", versions)
	}

	sqlDB, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = sqlDB.Close()
	}()
	var versionCount int
	var versionsCSV, contents string
	row := sqlDB.QueryRowContext(ctx, `
SELECT COUNT(*), GROUP_CONCAT(version ORDER BY version SEPARATOR ','), GROUP_CONCAT(content_md ORDER BY version SEPARATOR '||')
FROM tenant_skills s
JOIN tenants t ON t.id = s.tenant_id
WHERE t.tenant_key = ? AND s.skill_key = ?`, "yutang", skillKey)
	if err := row.Scan(&versionCount, &versionsCSV, &contents); err != nil {
		t.Fatal(err)
	}
	if versionCount != 4 || versionsCSV != "1,2,3,4" {
		t.Fatalf("stored versions count=%d versions=%s", versionCount, versionsCSV)
	}
	if strings.Count(contents, "version one "+suffix) != 3 || strings.Count(contents, "version two "+suffix) != 1 {
		t.Fatalf("stored rollback contents = %q", contents)
	}
}

type tenantSkillRuntimeStreamer struct {
	skillKey      string
	suffix        string
	calls         int
	systems       []string
	sawMetadata   bool
	sawLoadedBody bool
	sawThinking   bool
}

type structuredTenantSkillStreamer struct {
	suffix            string
	systems           []string
	sawTeachV2        bool
	sawStructuredBody bool
}

func (s *structuredTenantSkillStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.systems = append(s.systems, req.System)
	system := req.System
	s.sawTeachV2 = strings.Contains(system, "# Tenant Skill: "+("teach-v2-"+s.suffix))
	s.sawStructuredBody = strings.Contains(system, "unique structured body "+s.suffix)
	if err := cb.OnText(`{"skill_version":2,"decision_schema_version":"teach_decision_v1","reply":"ok"}`); err != nil {
		return nil, err
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: `{"skill_version":2,"decision_schema_version":"teach_decision_v1","reply":"ok"}`}}},
		StopReason: "end_turn",
	}, nil
}

func serverResponseFormatForE2E(format *OpenAIResponseFormat) *anthropic.ResponseFormat {
	if format == nil {
		return nil
	}
	out := &anthropic.ResponseFormat{Type: format.Type}
	if format.JSONSchema != nil {
		out.JSONSchema = &anthropic.ResponseFormatSchema{
			Name:        format.JSONSchema.Name,
			Description: format.JSONSchema.Description,
			Strict:      format.JSONSchema.Strict,
			Schema:      format.JSONSchema.Schema,
		}
	}
	return out
}

func (s *tenantSkillRuntimeStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.calls++
	s.systems = append(s.systems, req.System)
	if req.Thinking != nil && req.Thinking.Effort == "high" {
		s.sawThinking = true
	}
	switch s.calls {
	case 1:
		system := req.System
		if strings.Contains(system, s.skillKey+": Tenant runtime skill "+s.suffix) && !strings.Contains(system, "Tenant skill body "+s.suffix) {
			s.sawMetadata = true
		}
		return &anthropic.StreamResult{Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
			Type: "tool_use", ID: "toolu_skill", Name: "Skill", Input: json.RawMessage(`{"name":"` + s.skillKey + `"}`),
		}}}, StopReason: "tool_use"}, nil
	default:
		if e2eMessagesContainText(req.Messages, "Tenant skill body "+s.suffix) {
			s.sawLoadedBody = true
		}
		if err := cb.OnText("tenant skill runtime ok"); err != nil {
			return nil, err
		}
		return &anthropic.StreamResult{Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "tenant skill runtime ok"}}}, StopReason: "end_turn"}, nil
	}
}

func e2eMessagesContainText(messages []anthropic.MessageParam, want string) bool {
	for _, message := range messages {
		for _, block := range message.Content {
			if strings.Contains(block.Text, want) || strings.Contains(block.Content, want) {
				return true
			}
		}
	}
	return false
}

func tenantRequestContext(ctx context.Context, userKey, traceID string) context.Context {
	return observability.WithRequestValues(ctx, traceID, userKey, "yutang")
}

func TestMySQLE2EGoalAPILifecycle(t *testing.T) {
	dsn := os.Getenv("NIUMA_MYSQL_E2E_DSN")
	if dsn == "" {
		t.Skip("set NIUMA_MYSQL_E2E_DSN to run real MySQL goal API e2e")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	migrator, err := mysqlstore.NewMigrator(mysqlstore.MigrationOptions{DSN: dsn, Path: serverMySQLE2EMigrationsPath(t)})
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

	handler := NewHandler(Options{AuthToken: "token", Workspace: "/workspace", TenantService: tenantservice.NewService(repo, nil)}, nil)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	userKey := "goal-api-mysql-e2e-" + suffix
	traceID := "trace-goal-api-mysql-e2e-" + suffix

	userResp := tenantAPIRequest(t, handler, http.MethodPatch, "/tenant/user", `{"email":"`+userKey+`@example.test","display_name":"Goal API E2E","role":"owner","status":"active"}`, userKey, traceID)
	assertStatus(t, userResp, http.StatusOK)

	createResp := tenantAPIRequest(t, handler, http.MethodPost, "/tenant/goals", `{"objective":"goal api mysql e2e `+suffix+`","session_id":"session-goal-e2e-`+suffix+`","cwd":"/workspace","model":"claude-test","turn_budget":5,"token_budget":2000}`, userKey, traceID)
	assertStatus(t, createResp, http.StatusOK)
	goalID := jsonString(t, createResp.Body.Bytes(), "id")
	if goalID == "" || !strings.Contains(createResp.Body.String(), `"status":"active"`) {
		t.Fatalf("create response = %s", createResp.Body.String())
	}

	listResp := tenantAPIRequest(t, handler, http.MethodGet, "/tenant/goals?active=true&limit=5", "", userKey, traceID)
	assertStatus(t, listResp, http.StatusOK)
	if !strings.Contains(listResp.Body.String(), goalID) {
		t.Fatalf("list response = %s", listResp.Body.String())
	}

	getResp := tenantAPIRequest(t, handler, http.MethodGet, "/tenant/goals/"+goalID, "", userKey, traceID)
	assertStatus(t, getResp, http.StatusOK)
	if !strings.Contains(getResp.Body.String(), "goal api mysql e2e "+suffix) {
		t.Fatalf("get response = %s", getResp.Body.String())
	}

	eventsResp := tenantAPIRequest(t, handler, http.MethodGet, "/tenant/goals/"+goalID+"/events?limit=10", "", userKey, traceID)
	assertStatus(t, eventsResp, http.StatusOK)
	if !strings.Contains(eventsResp.Body.String(), `"type":"goal_started"`) {
		t.Fatalf("events response = %s", eventsResp.Body.String())
	}

	stopResp := tenantAPIRequest(t, handler, http.MethodPost, "/tenant/goals/"+goalID+"/stop", "", userKey, traceID)
	assertStatus(t, stopResp, http.StatusOK)
	if !strings.Contains(stopResp.Body.String(), `"status":"stopped"`) {
		t.Fatalf("stop response = %s", stopResp.Body.String())
	}

	resumeResp := tenantAPIRequest(t, handler, http.MethodPost, "/tenant/goals/"+goalID+"/resume", "", userKey, traceID)
	assertStatus(t, resumeResp, http.StatusOK)
	if !strings.Contains(resumeResp.Body.String(), `"status":"active"`) {
		t.Fatalf("resume response = %s", resumeResp.Body.String())
	}

	var storedStatus string
	var eventCount int
	var eventTypes string
	sqlDB, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = sqlDB.Close()
	}()
	row := sqlDB.QueryRowContext(ctx, `
SELECT g.status, COUNT(e.id), GROUP_CONCAT(e.event_type ORDER BY e.id SEPARATOR ',')
FROM tenant_goals g
JOIN tenants t ON t.id = g.tenant_id
JOIN tenant_users u ON u.id = g.user_id
LEFT JOIN tenant_goal_events e ON e.goal_id = g.id
WHERE t.tenant_key = ? AND u.user_key = ? AND g.goal_key = ?
GROUP BY g.id, g.status`, "yutang", userKey, goalID)
	if err := row.Scan(&storedStatus, &eventCount, &eventTypes); err != nil {
		t.Fatal(err)
	}
	if storedStatus != string(goal.StatusActive) || eventCount != 3 || !strings.Contains(eventTypes, string(goal.EventGoalStarted)) || !strings.Contains(eventTypes, string(goal.EventGoalStopped)) || !strings.Contains(eventTypes, string(goal.EventGoalResumed)) {
		t.Fatalf("stored goal status=%s event_count=%d event_types=%s", storedStatus, eventCount, eventTypes)
	}
}

func tenantAPIRequest(t *testing.T, handler http.Handler, method, path, body, userKey, traceID string) *httptest.ResponseRecorder {
	t.Helper()
	return tenantAPIRequestForTenant(t, handler, method, path, body, "yutang", userKey, traceID)
}

func tenantAPIRequestForTenant(t *testing.T, handler http.Handler, method, path, body, tenantKey, userKey, traceID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("authorization", "Bearer token")
	req.Header.Set("x-tenant-key", tenantKey)
	req.Header.Set("x-user-id", userKey)
	req.Header.Set("x-trace-id", traceID)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func assertStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d want=%d body=%s", rec.Code, want, rec.Body.String())
	}
}

func jsonUint(t *testing.T, data []byte, key string) uint64 {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("decode json: %v\n%s", err, data)
	}
	raw, ok := value[key].(float64)
	if !ok {
		return 0
	}
	return uint64(raw)
}

func jsonString(t *testing.T, data []byte, key string) string {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("decode json: %v\n%s", err, data)
	}
	raw, _ := value[key].(string)
	return raw
}

func sessionIDByKey(t *testing.T, data []byte, sessionKey string) uint64 {
	t.Helper()
	var payload struct {
		Data []struct {
			ID         uint64 `json:"id"`
			SessionKey string `json:"session_key"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("decode sessions json: %v\n%s", err, data)
	}
	for _, item := range payload.Data {
		if item.SessionKey == sessionKey {
			return item.ID
		}
	}
	return 0
}

func serverMySQLE2EMigrationsPath(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "migrations", "mysql")
}
