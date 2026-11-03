package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/imagegen"
	"github.com/konglong87/super-niuma/internal/media"
	"github.com/konglong87/super-niuma/internal/pendinginput"
	"github.com/konglong87/super-niuma/internal/query"
	"github.com/konglong87/super-niuma/internal/sessioncontrol"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

func sessionControlImageFixture(t *testing.T) (agentTaskAttachmentRequest, []byte) {
	t.Helper()
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewNRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return agentTaskAttachmentRequest{Type: "image", MediaType: "image/png", Name: "image.png", SizeBytes: int64(data.Len()), InlineData: base64.StdEncoding.EncodeToString(data.Bytes())}, data.Bytes()
}

func sessionControlImageOptions() (Options, *sessionControlServiceFake) {
	service := &sessionControlServiceFake{snapshot: sessioncontrol.SessionSnapshot{ID: 41, Ref: sessioncontrol.SessionRef{Source: sessioncontrol.SourceTenant, Key: "alpha"}}}
	return Options{AuthToken: "token", TenantService: &fakeTenantService{tenantID: 7, userID: 11}, SessionControl: service, ImageBlobStore: imagegen.NewMemoryBlobStore(), MediaAssetStore: media.NewMemoryStore()}, service
}

func TestSessionControlInlineImagePersistsReferenceAndRehydratesActualQuery(t *testing.T) {
	input, data := sessionControlImageFixture(t)
	opts, service := sessionControlImageOptions()
	handler := NewHandler(opts, nil)
	body, _ := json.Marshal(sessionControlSendDTO{Content: "describe", Attachments: []agentTaskAttachmentRequest{input}})
	request := sessionControlRequest(http.MethodPost, "/tenant/session-control/sessions/tenant/alpha/messages", string(body))
	request.Header.Set("Idempotency-Key", "image-key")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || len(service.sendRequest.Attachments) != 1 {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	attachment := service.sendRequest.Attachments[0]
	if attachment.InlineData != "" || !strings.HasPrefix(attachment.AttachmentID, sessionControlImagePrefix) || attachment.URL != sessionControlImageURLPrefix+attachment.AttachmentID || attachment.SHA256 == "" {
		t.Fatalf("attachment=%+v", attachment)
	}
	stored, _ := json.Marshal(agenttasks.MessageInput{TaskID: 51, Content: "describe", Attachments: service.sendRequest.Attachments})
	if bytes.Contains(stored, []byte(input.InlineData)) || bytes.Contains(stored, []byte("inline_data")) {
		t.Fatal("image bytes entered durable message")
	}
	var restored agenttasks.MessageInput
	if err := json.Unmarshal(stored, &restored); err != nil {
		t.Fatal(err)
	}
	task := mysqlstore.AgentTask{ID: 51, TenantID: 7, UserID: 11, ParentSessionID: 41, AgentName: agenttasks.AgentNameWeb, Model: "test-model", MetadataJSON: `{}`}
	called := false
	_, err := runAgentTaskMessage(context.Background(), opts, func(_ context.Context, request QueryRequest) (query.Result, error) {
		called = true
		if len(request.Attachments) != 1 || request.Attachments[0].InlineData != input.InlineData || request.Attachments[0].URL != "" {
			t.Fatalf("actual attachments=%+v", request.Attachments)
		}
		return query.Result{Response: "done"}, nil
	}, task, restored)
	if err != nil || !called {
		t.Fatalf("actual run called=%v err=%v", called, err)
	}
	get := httptest.NewRecorder()
	handler.ServeHTTP(get, sessionControlRequest(http.MethodGet, attachment.URL, ""))
	if get.Code != http.StatusOK || !bytes.Equal(get.Body.Bytes(), data) {
		t.Fatalf("asset read status=%d", get.Code)
	}
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, attachment.URL, nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatal("asset read skipped authentication")
	}
}

func TestSessionControlImageQueueRetryAndSideChatRetainExactScope(t *testing.T) {
	input, _ := sessionControlImageFixture(t)
	opts, _ := sessionControlImageOptions()
	ctx := context.Background()
	policy := media.AccessPolicy{TenantID: 7, UserID: 11, SessionID: 41}
	attachment, err := archiveSessionControlImage(ctx, opts, policy, input)
	if err != nil {
		t.Fatal(err)
	}
	queue := pendinginput.NewMemoryQueue()
	queued, err := queue.Add(ctx, pendinginput.NewInput{Scope: pendinginput.Scope{TenantID: 7, UserID: 11, SessionID: "41", BaseTaskID: 51}, ClientInputID: "queue-image", Content: "describe", Attachments: []agenttasks.Attachment{attachment}})
	if err != nil {
		t.Fatal(err)
	}
	persisted, _ := json.Marshal(queued)
	var recovered pendinginput.PendingInput
	if err := json.Unmarshal(persisted, &recovered); err != nil {
		t.Fatal(err)
	}
	source := mysqlstore.AgentTask{ID: 51, TenantID: 7, UserID: 11, ParentSessionID: 41}
	for attempt := 0; attempt < 2; attempt++ {
		hydrated, err := agentTaskQueryAttachments(ctx, opts, source, recovered.Attachments)
		if err != nil || len(hydrated) != 1 || hydrated[0].InlineData != input.InlineData {
			t.Fatalf("attempt=%d err=%v attachments=%+v", attempt, err, hydrated)
		}
	}
	target := source
	target.ParentSessionID = 42
	if _, err := agentTaskQueryAttachments(ctx, opts, target, recovered.Attachments); err == nil {
		t.Fatal("original attachment crossed session boundary")
	}
	cloned, err := cloneSessionControlAttachments(ctx, opts, source, target.ParentSessionID, recovered.Attachments)
	if err != nil || cloned[0].AttachmentID == attachment.AttachmentID || cloned[0].SHA256 != attachment.SHA256 {
		t.Fatalf("clone=%+v err=%v", cloned, err)
	}
	if hydrated, err := agentTaskQueryAttachments(ctx, opts, target, cloned); err != nil || hydrated[0].InlineData != input.InlineData {
		t.Fatalf("cloned image err=%v", err)
	}
	asset, err := opts.MediaAssetStore.Get(ctx, policy, attachment.AttachmentID)
	if err != nil || asset.SHA256 != "" || asset.Original.SHA256 != attachment.SHA256 {
		t.Fatalf("content hash ownership asset=%+v err=%v", asset, err)
	}
	foreign := source
	foreign.UserID = 12
	if _, err := agentTaskQueryAttachments(ctx, opts, foreign, recovered.Attachments); err == nil {
		t.Fatal("image crossed user boundary")
	}
	foreign.TenantID = 8
	if _, err := agentTaskQueryAttachments(ctx, opts, foreign, recovered.Attachments); err == nil {
		t.Fatal("image crossed tenant boundary")
	}
}

func TestSessionControlSideChatHTTPClonesImageAndFirstRunHydratesCandidate(t *testing.T) {
	ctx := context.Background()
	input, _ := sessionControlImageFixture(t)
	opts, _ := sessionControlImageOptions()
	attachment, err := archiveSessionControlImage(ctx, opts, media.AccessPolicy{TenantID: 7, UserID: 11, SessionID: 41}, input)
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeTenantService{tenantID: 7, userID: 11, sessionID: 42, agentTasks: []mysqlstore.AgentTask{{ID: 51, TenantID: 7, UserID: 11, ParentSessionID: 41, AgentName: agenttasks.AgentNameWeb, Model: "model-a", Status: agenttasks.StatusRunning, MetadataJSON: `{"cwd":"/repo","provider":"provider-a"}`}}}
	opts.TenantService = fake
	opts.PendingInputQueue = pendinginput.NewMemoryQueue()
	scope := pendinginput.Scope{TenantID: 7, UserID: 11, SessionID: "41", BaseTaskID: 51}
	item, err := opts.PendingInputQueue.Add(ctx, pendinginput.NewInput{Scope: scope, ClientInputID: "side-image", Content: "original candidate", Attachments: []agenttasks.Attachment{attachment}})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	NewHandler(opts, nil).ServeHTTP(response, sessionControlRequest(http.MethodPost, "/tenant/agent-tasks/51/pending-inputs/"+item.ID+"/side-chat", ""))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if fake.lastSession.Model != "model-a" || fake.lastSession.CWD != "/repo" {
		t.Fatalf("side session lost runtime defaults: %+v", fake.lastSession)
	}
	var result struct {
		TaskID uint64 `json:"task_id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	task, err := fake.GetAgentTask(ctx, result.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	task.TenantID, task.UserID = 7, 11
	candidate, err := webAgentTaskMessageFromEvents(ctx, fake, task.ID)
	if err != nil || len(candidate.Attachments) != 1 || candidate.Attachments[0].AttachmentID == attachment.AttachmentID {
		t.Fatalf("candidate=%+v err=%v", candidate, err)
	}
	called := false
	run, err := runAgentTaskMessage(ctx, opts, func(_ context.Context, request QueryRequest) (query.Result, error) {
		called = true
		if len(request.Attachments) != 1 || request.Attachments[0].InlineData != input.InlineData || len(request.InitialMessages) != 1 || request.InitialMessages[0].Content[0].Text != item.Content {
			t.Fatalf("side-chat query lost candidate image/context: %+v", request)
		}
		return query.Result{Response: "done"}, nil
	}, task, agenttasks.MessageInput{TaskID: task.ID, Content: "inspect separately"})
	if err != nil || !called || run.Status != agenttasks.StatusCompleted {
		t.Fatalf("run=%+v called=%v err=%v", run, called, err)
	}
	items, err := opts.PendingInputQueue.List(ctx, scope)
	if err != nil || len(items) != 1 || items[0].Attachments[0].AttachmentID != attachment.AttachmentID {
		t.Fatalf("original queue changed: %+v err=%v", items, err)
	}
}

func TestSessionControlImageIntegrityFailureStopsBeforeModelAndPersistsFailure(t *testing.T) {
	ctx := context.Background()
	input, data := sessionControlImageFixture(t)
	opts, _ := sessionControlImageOptions()
	policy := media.AccessPolicy{TenantID: 7, UserID: 11, SessionID: 41}
	attachment, err := archiveSessionControlImage(ctx, opts, policy, input)
	if err != nil {
		t.Fatal(err)
	}
	asset, err := opts.MediaAssetStore.Get(ctx, policy, attachment.AttachmentID)
	if err != nil {
		t.Fatal(err)
	}
	data[0] ^= 1
	if _, err := opts.ImageBlobStore.Put(ctx, imagegen.PutBlobRequest{Policy: policy, Key: asset.Original.Path, MediaType: asset.MediaType, Data: bytes.NewReader(data)}); err != nil {
		t.Fatal(err)
	}
	called := false
	task := mysqlstore.AgentTask{ID: 51, TenantID: 7, UserID: 11, ParentSessionID: 41, AgentName: agenttasks.AgentNameWeb, MetadataJSON: `{}`}
	result, err := runAgentTaskMessage(ctx, opts, func(context.Context, QueryRequest) (query.Result, error) {
		called = true
		return query.Result{}, nil
	}, task, agenttasks.MessageInput{TaskID: task.ID, Content: "inspect", Attachments: []agenttasks.Attachment{attachment}})
	fake := opts.TenantService.(*fakeTenantService)
	if err != nil || called || result.Status != agenttasks.StatusFailed || fake.lastFinishedAgentStatus != agenttasks.StatusFailed || !strings.Contains(fake.lastFinishedAgentResult, "integrity check failed") {
		t.Fatalf("result=%+v called=%v finish=%s err=%v", result, called, fake.lastFinishedAgentResult, err)
	}
}

func TestSessionControlAdoptedSideChatFollowupSurvivesNextRunHistory(t *testing.T) {
	ctx := context.Background()
	previous := mysqlstore.AgentTask{ID: 51, ParentSessionID: 41, AgentName: agenttasks.AgentNameWeb, Status: agenttasks.StatusCompleted, MetadataJSON: `{"source":"pending-input-side-chat"}`, ResultJSON: `{"response":"answer"}`}
	fake := &fakeTenantService{agentTasks: []mysqlstore.AgentTask{previous}, agentTaskEvents: []mysqlstore.AgentTaskEvent{
		{ID: 1, TaskID: 51, EventType: agenttasks.EventMessage, PayloadJSON: `{"content":"original candidate"}`},
		{ID: 2, TaskID: 51, EventType: agenttasks.EventMessage, PayloadJSON: `{"content":"specific followup"}`},
	}}
	initial := webAgentConversationInitialMessages(ctx, fake, mysqlstore.AgentTask{ID: 52, ParentSessionID: 41})
	if len(initial) != 2 || initial[0].Content[0].Text != "original candidate\n\nspecific followup" || initial[1].Content[0].Text != "answer" {
		t.Fatalf("side-chat history lost followup: %+v", initial)
	}
	fake.agentTasks[0].MetadataJSON = `{}`
	initial = webAgentConversationInitialMessages(ctx, fake, mysqlstore.AgentTask{ID: 52, ParentSessionID: 41})
	if len(initial) != 2 || initial[0].Content[0].Text != "original candidate" {
		t.Fatalf("normal task history changed: %+v", initial)
	}
}

func TestSessionControlInlineImageRejectsInvalidContentBeforeArchive(t *testing.T) {
	input, _ := sessionControlImageFixture(t)
	for _, mutate := range []func(*agentTaskAttachmentRequest){func(v *agentTaskAttachmentRequest) { v.InlineData = "invalid" }, func(v *agentTaskAttachmentRequest) { v.SizeBytes++ }, func(v *agentTaskAttachmentRequest) { v.MediaType = "image/jpeg" }, func(v *agentTaskAttachmentRequest) { v.SHA256 = "wrong" }} {
		opts, _ := sessionControlImageOptions()
		invalid := input
		mutate(&invalid)
		if _, err := archiveSessionControlImage(context.Background(), opts, media.AccessPolicy{TenantID: 7, UserID: 11, SessionID: 41}, invalid); err == nil {
			t.Fatal("invalid image was accepted")
		}
		blobs, _ := opts.ImageBlobStore.(imagegen.BlobInventory).ListTenantBlobs(context.Background(), 7)
		if len(blobs) != 0 {
			t.Fatal("invalid image reached blob store")
		}
	}
	opts, service := sessionControlImageOptions()
	service.err = &sessioncontrol.ServiceError{Code: sessioncontrol.CodeForbidden, Message: "not owned"}
	if _, err := prepareSessionControlAttachments(context.Background(), opts, sessioncontrol.RequestContext{TenantID: 7, UserID: 11, ActorUserID: 11}, service.snapshot.Ref, []agentTaskAttachmentRequest{input}); err == nil {
		t.Fatal("unauthorized image was accepted")
	}
	blobs, _ := opts.ImageBlobStore.(imagegen.BlobInventory).ListTenantBlobs(context.Background(), 7)
	if len(blobs) != 0 {
		t.Fatal("unauthorized image reached blob store")
	}
}

func TestSessionControlInlineImageConcurrentRetriesUseOneContentAddress(t *testing.T) {
	input, _ := sessionControlImageFixture(t)
	opts, _ := sessionControlImageOptions()
	policy := media.AccessPolicy{TenantID: 7, UserID: 11, SessionID: 41}
	results := make(chan agenttasks.Attachment, 4)
	errors := make(chan error, 4)
	var workers sync.WaitGroup
	for i := 0; i < 4; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			result, err := archiveSessionControlImage(context.Background(), opts, policy, input)
			results <- result
			errors <- err
		}()
	}
	workers.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	var id string
	for result := range results {
		if id != "" && id != result.AttachmentID {
			t.Fatal("same image got different IDs")
		}
		id = result.AttachmentID
	}
	blobs, _ := opts.ImageBlobStore.(imagegen.BlobInventory).ListTenantBlobs(context.Background(), 7)
	if len(blobs) != 1 {
		t.Fatalf("blobs=%d", len(blobs))
	}
}

func TestSessionControlImageBodyLimitIsLocalAndHonorsExplicitLimit(t *testing.T) {
	for _, test := range []struct {
		path   string
		opts   Options
		length int64
		want   int
	}{
		{"/tenant/session-control/sessions/tenant/alpha/messages", Options{}, defaultMaxRequestBodyBytes + 1, http.StatusNoContent},
		{"/api/tenant/session-control/sessions/tenant/alpha/messages", Options{}, defaultMaxRequestBodyBytes + 1, http.StatusNoContent},
		{"/query", Options{}, defaultMaxRequestBodyBytes + 1, http.StatusRequestEntityTooLarge},
		{"/tenant/session-control/sessions/tenant/alpha/messages", Options{MaxRequestBodyBytes: 1024}, 1025, http.StatusRequestEntityTooLarge},
		{"/tenant/session-control/sessions/tenant/alpha/messages", Options{}, sessionControlImageMaxRequestBytes + 1, http.StatusRequestEntityTooLarge},
	} {
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
		handler := requestGuardHandler(next, newStreamRegistry(), test.opts)
		request := httptest.NewRequest(http.MethodPost, test.path, io.NopCloser(strings.NewReader("")))
		request.ContentLength = test.length
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.want {
			t.Fatalf("path=%s status=%d want=%d", test.path, response.Code, test.want)
		}
	}
}
