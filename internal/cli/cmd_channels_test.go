package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	channelcontract "github.com/konglong87/super-niuma/internal/channel"
	"github.com/konglong87/super-niuma/internal/channel/onboarding"
	channelruntime "github.com/konglong87/super-niuma/internal/channel/runtime"
	"github.com/konglong87/super-niuma/internal/imagegen"
	"github.com/konglong87/super-niuma/internal/media"
	"github.com/konglong87/super-niuma/internal/query"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	"github.com/konglong87/super-niuma/internal/tools"
)

type channelImageAuditRecorder struct {
	inputs []mysqlstore.AuditLogInput
}

type recordingChannelDeltaSink struct {
	deltas []channelcontract.Delta
}

func (s *recordingChannelDeltaSink) OnDelta(_ context.Context, delta channelcontract.Delta) error {
	s.deltas = append(s.deltas, delta)
	return nil
}

func (r *channelImageAuditRecorder) InsertAuditLog(_ context.Context, input mysqlstore.AuditLogInput) (uint64, error) {
	r.inputs = append(r.inputs, input)
	return uint64(len(r.inputs)), nil
}

func TestParseFeishuAuthorizationScopesDefaultsToReactionScope(t *testing.T) {
	got, err := parseFeishuAuthorizationScopes(nil)
	if err != nil || len(got) != 3 || got[0] != onboarding.FeishuScopeMessage || got[1] != onboarding.FeishuScopeMessageReactionsWriteOnly || got[2] != onboarding.FeishuScopeResource {
		t.Fatalf("scopes=%+v err=%v", got, err)
	}
}

func TestParseFeishuAuthorizationScopesAcceptsRepeatedScopeFlag(t *testing.T) {
	got, err := parseFeishuAuthorizationScopes([]string{"--scope", "scope-a", "--scope", "scope-b"})
	if err != nil || len(got) != 2 || got[0] != "scope-a" || got[1] != "scope-b" {
		t.Fatalf("scopes=%+v err=%v", got, err)
	}
}

func TestChannelStreamingSettingsPreferCanonicalNamesAndKeepAliases(t *testing.T) {
	t.Setenv("NIUMA_CHANNEL_STREAMING_CARD_MODE", "on")
	t.Setenv("NIUMA_CHANNEL_STREAMING", "allowlist")
	if got := channelStreamingGlobalSetting(); got != "on" {
		t.Fatalf("canonical global setting = %q", got)
	}
	t.Setenv("NIUMA_CHANNEL_STREAMING_CARD_MODE", "")
	if got := channelStreamingGlobalSetting(); got != "allowlist" {
		t.Fatalf("legacy global setting = %q", got)
	}

	t.Setenv("NIUMA_CHANNEL_STREAMING_ACCOUNT_MODE", "enabled")
	t.Setenv("NIUMA_CHANNEL_STREAMING_ACCOUNT", "disabled")
	if got := channelStreamingAccountSetting(); got != "enabled" {
		t.Fatalf("canonical account setting = %q", got)
	}
	t.Setenv("NIUMA_CHANNEL_STREAMING_ACCOUNT_MODE", "")
	if got := channelStreamingAccountSetting(); got != "disabled" {
		t.Fatalf("legacy account setting = %q", got)
	}
}

func TestChannelStreamCallbacksEnableLiveText(t *testing.T) {
	sink := &recordingChannelDeltaSink{}
	callbacks := channelStreamCallbacks(context.Background(), sink)
	if callbacks.OnTextAmended == nil {
		t.Fatal("channel streaming must enable live text callbacks")
	}
	if err := callbacks.OnTextAmended("draft", "final"); err != nil {
		t.Fatalf("live text amend callback: %v", err)
	}
	if len(sink.deltas) != 1 || sink.deltas[0].Kind != channelcontract.DeltaTextAmend || sink.deltas[0].PreviousText != "draft" || sink.deltas[0].Text != "final" {
		t.Fatalf("text amendment delta = %+v", sink.deltas)
	}
}

func TestChannelToolDetailsSettingPrefersCanonicalName(t *testing.T) {
	t.Setenv("NIUMA_CHANNEL_TOOL_DETAILS", "preview")
	t.Setenv("CHANNEL_TOOL_DETAILS", "off")
	if got := channelToolDetailsSetting(); got != "preview" {
		t.Fatalf("canonical tool details setting = %q", got)
	}
	t.Setenv("NIUMA_CHANNEL_TOOL_DETAILS", "")
	if got := channelToolDetailsSetting(); got != "off" {
		t.Fatalf("legacy tool details setting = %q", got)
	}
}

func TestChannelTimelineRendererPaginatesSevenTools(t *testing.T) {
	state := channelcontract.CardState{Status: channelcontract.CardCompleted}
	for index := 0; index < 7; index++ {
		tool := channelcontract.ToolProgress{ID: fmt.Sprintf("tool-%d", index), Name: "Read", Status: channelcontract.ToolStatusComplete}
		state.Timeline = append(state.Timeline, channelcontract.TimelineEntry{ID: "tool:" + tool.ID, Kind: channelcontract.TimelineTool, Tool: tool})
	}
	pages, err := channelTimelineCardRenderer(channelcontract.ToolDetailsPreview)(state)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 2 {
		t.Fatalf("timeline pages = %d, want 2", len(pages))
	}
}

func TestChannelQuestionsSettingDefaultsOnAndSupportsOff(t *testing.T) {
	t.Setenv("NIUMA_CHANNEL_QUESTIONS", "")
	t.Setenv("CHANNEL_QUESTIONS", "")
	if enabled, err := channelQuestionsEnabled(); err != nil || !enabled {
		t.Fatalf("default enabled=%v err=%v", enabled, err)
	}
	t.Setenv("NIUMA_CHANNEL_QUESTIONS", "off")
	if enabled, err := channelQuestionsEnabled(); err != nil || enabled {
		t.Fatalf("off enabled=%v err=%v", enabled, err)
	}
	t.Setenv("NIUMA_CHANNEL_QUESTIONS", "invalid")
	if _, err := channelQuestionsEnabled(); err == nil {
		t.Fatal("invalid questions mode accepted")
	}
}

func TestChannelEnvironmentAliasesSupportProvisionedWorkers(t *testing.T) {
	t.Setenv("NIUMA_CHANNEL_TENANT_ID", "")
	t.Setenv("NIUMA_CHANNEL_TENANT_ID", "17")
	if got, err := requiredUintEnv("NIUMA_CHANNEL_TENANT_ID"); err != nil || got != 17 {
		t.Fatalf("tenant alias = %d err=%v", got, err)
	}

	t.Setenv("NIUMA_FEISHU_CREDENTIAL_FILE", "")
	t.Setenv("NIUMA_FEISHU_CREDENTIAL_FILE", "/tmp/feishu-credentials.json")
	if got := channelCredentialPath(); got != "/tmp/feishu-credentials.json" {
		t.Fatalf("credential alias = %q", got)
	}

	t.Setenv("NIUMA_SQLITE_PATH", "")
	t.Setenv("NIUMA_SQLITE_PATH", "/tmp/desktop.sqlite")
	t.Setenv("NIUMA_CHANNEL_TENANT_ID", "")
	t.Setenv("NIUMA_CHANNEL_TENANT_ID", "17")
	t.Setenv("NIUMA_CHANNEL_ACCOUNT_ID", "")
	t.Setenv("NIUMA_CHANNEL_ACCOUNT_ID", "23")
	t.Setenv("NIUMA_CHANNEL_PAYLOAD_KEY", "")
	t.Setenv("NIUMA_CHANNEL_PAYLOAD_KEY", strings.Repeat("a", 64))
	if _, err := channelPayloadCodec(); err != nil {
		t.Fatalf("payload key alias: %v", err)
	}
}

func TestChannelToolCollectorKeepsCommandAndOutput(t *testing.T) {
	collector := newChannelToolCollector()
	collector.onCall(query.ToolCallEvent{ID: "tool-1", Name: "Bash", Input: []byte(`{"command":"go test ./..."}`)})
	collector.onResult(query.ToolTrace{ID: "tool-1", Name: "Bash", Input: `{"command":"go test ./..."}`, Output: "ok"})
	tools := collector.snapshot()
	if len(tools) != 1 || tools[0].Status != "complete" || tools[0].Command != "go test ./..." || tools[0].OutputPreview != "ok" {
		t.Fatalf("collected tools = %+v", tools)
	}
	if tools[0].IsError {
		t.Fatal("successful tool should not be marked as error")
	}
}

func TestChannelToolCollectorPreservesNonStreamingTimelineOrder(t *testing.T) {
	collector := newChannelToolCollector()
	if _, err := collector.Write([]byte("before")); err != nil {
		t.Fatal(err)
	}
	collector.onCall(query.ToolCallEvent{ID: "tool-1", Name: "Read", Input: []byte(`{"file_path":"README.md"}`)})
	collector.onResult(query.ToolTrace{ID: "tool-1", Name: "Read", Output: "done"})
	if _, err := collector.Write([]byte("after")); err != nil {
		t.Fatal(err)
	}
	timeline := collector.timelineSnapshot()
	if len(timeline) != 3 || timeline[0].Kind != channelcontract.TimelineText || timeline[0].Text != "before" || timeline[1].Kind != channelcontract.TimelineTool || timeline[1].Tool.OutputPreview != "done" || timeline[2].Kind != channelcontract.TimelineText || timeline[2].Text != "after" {
		t.Fatalf("non-streaming timeline = %+v", timeline)
	}
}

func TestAttachChannelTimelineDoesNotRestoreUnsafeFailedText(t *testing.T) {
	result := channelRunResultOnError(query.Result{Response: "request failed: provider secret token"}, nil, errors.New("provider secret token"), false)
	timeline := []channelcontract.TimelineEntry{
		{Kind: channelcontract.TimelineText, Text: "request failed: provider secret token"},
		{ID: "tool:tool-1", Kind: channelcontract.TimelineTool, Tool: channelcontract.ToolProgress{ID: "tool-1", Name: "Read", Status: channelcontract.ToolStatusFailed}},
	}
	got := attachChannelTimeline(result, timeline)
	encoded, err := json.Marshal(got.Timeline)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "provider secret token") || len(got.Timeline) != 2 || got.Timeline[0].Kind != channelcontract.TimelineTool || got.Timeline[1].Text != got.FinalText {
		t.Fatalf("safe failed timeline = %+v final=%q", got.Timeline, got.FinalText)
	}
}

func TestChannelRunResultExtractsImageArtifactMetadata(t *testing.T) {
	result := query.Result{Response: "done", ToolCalls: []query.ToolTrace{{Name: "GenerateImage", Output: `{"asset_id":"asset-1","generation_id":"gen-1","tenant_id":7,"user_id":11,"session_id":13,"operation":"generate","media_type":"image/png","name":"asset.png","size_bytes":12,"url":"/tenant/media/assets/asset-1","provider":"jiuan","model":"gpt-image-2"}`}}}
	got := channelRunResult(result, nil)
	if len(got.Attachments) != 1 {
		t.Fatalf("attachments = %+v", got.Attachments)
	}
	attachment := got.Attachments[0]
	if attachment.ID != "asset-1" || attachment.Type != "image" || attachment.TenantID != 7 || attachment.UserID != 11 || attachment.SessionID != 13 || attachment.MediaType != "image/png" {
		t.Fatalf("attachment = %+v", attachment)
	}
}

func TestChannelRunResultExtractsArtifactWhenToolResultHasTrailingText(t *testing.T) {
	result := query.Result{ToolCalls: []query.ToolTrace{{Name: "EditImage", Output: `{"asset_id":"asset-2","generation_id":"gen-2","tenant_id":7,"user_id":11,"session_id":13,"operation":"edit","media_type":"image/png","name":"asset.png"}
Image artifact persisted.`}}}
	got := channelRunResult(result, nil)
	if len(got.Attachments) != 1 || got.Attachments[0].ID != "asset-2" {
		t.Fatalf("attachments = %+v", got.Attachments)
	}
}

func TestChannelRunOptionsPropagateRunAndStrictVersionedImageOrigin(t *testing.T) {
	runner := cliChannelRunner{imageGenerator: &wiringImageGenerator{}, imageScheduler: &wiringImageScheduler{}, asyncChannelImages: true}
	input := channelcontract.RunInput{
		TenantID: 7, AccountID: 3, UserID: 11, TenantSessionID: 13, ConversationID: 9, RunID: "run-1",
		Scope: channelcontract.Scope{ThreadID: "thread-1"}, ReplyToMessageID: "om-1", Prompt: "do not persist this prompt",
	}
	opts := runner.optionsForRun(input)
	if opts.runID != "run-1" || opts.imageScheduler == nil || !opts.asyncChannelImages || opts.imageOriginFactory == nil {
		t.Fatalf("channel options=%+v", opts)
	}
	origin, err := opts.imageOriginFactory(tools.Context{Invocation: tools.Invocation{RunID: "run-1", ToolUseID: "tool-1", BatchID: "batch-1"}})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(origin.RefJSON), &decoded); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"version": float64(2), "tenant_id": float64(7), "account_id": float64(3), "conversation_id": float64(9), "run_id": "run-1", "session_id": float64(13), "user_id": float64(11), "reply_message_id": "om-1", "thread_id": "thread-1"}
	if origin.Type != imagegen.OriginTypeChannel || len(decoded) != len(want) {
		t.Fatalf("origin=%+v decoded=%+v", origin, decoded)
	}
	for key, value := range want {
		if decoded[key] != value {
			t.Fatalf("origin[%s]=%v want %v; json=%s", key, decoded[key], value, origin.RefJSON)
		}
	}
	if strings.Contains(origin.RefJSON, input.Prompt) {
		t.Fatalf("origin leaked prompt: %s", origin.RefJSON)
	}
}

func TestChannelAsyncAcknowledgementReportsCountsAndIDsWithoutAttachmentOrSecrets(t *testing.T) {
	acceptedAt := time.Date(2026, time.September, 3, 1, 2, 3, 0, time.UTC)
	receipt, err := json.Marshal(imagegen.JobReceipt{GenerationID: "gen-accepted", BatchID: "batch-1", Status: imagegen.JobStatus(imagegen.GenerationStatusQueued), AcceptedAt: acceptedAt})
	if err != nil {
		t.Fatal(err)
	}
	result := query.Result{Response: "model text", ToolCalls: []query.ToolTrace{
		{Name: "GenerateImage", Output: string(receipt)},
		{Name: "EditImage", IsError: true, Output: "provider rejected secret-prompt"},
	}}
	got := channelRunResultWithAsyncImages(result, nil, true)
	for _, want := range []string{"已受理 1 个", "拒绝 1 个", "gen-accepted"} {
		if !strings.Contains(got.FinalText, want) {
			t.Fatalf("ack missing %q: %s", want, got.FinalText)
		}
	}
	if strings.Contains(got.FinalText, "secret-prompt") || len(got.Attachments) != 0 {
		t.Fatalf("unsafe async result=%+v", got)
	}
}

func TestChannelQueryErrorPreservesSafePartialTextAndSuccessfulAttachment(t *testing.T) {
	result := query.Result{Response: "第一张图片已经完成。", ToolCalls: []query.ToolTrace{{Name: "GenerateImage", Output: `{"asset_id":"asset-1","generation_id":"gen-1","tenant_id":7,"user_id":11,"session_id":13,"operation":"generate","media_type":"image/png","name":"asset.png"}`}}}
	got := channelRunResultOnError(result, nil, errors.New("provider secret token"), false)
	if got.FinalText != result.Response || len(got.Attachments) != 1 || got.Attachments[0].ID != "asset-1" {
		t.Fatalf("partial result=%+v", got)
	}
	empty := channelRunResultOnError(query.Result{}, nil, errors.New("provider secret token"), false)
	if strings.Contains(empty.FinalText, "provider secret token") || strings.TrimSpace(empty.FinalText) == "" {
		t.Fatalf("unsafe empty fallback=%q", empty.FinalText)
	}
	unsafe := channelRunResultOnError(query.Result{Response: "request failed: provider secret token"}, nil, errors.New("provider secret token"), false)
	if strings.Contains(unsafe.FinalText, "provider secret token") {
		t.Fatalf("unsafe error response=%q", unsafe.FinalText)
	}
}

func TestChannelNaturalLanguageImageEnqueueWritesPromptFreeAudit(t *testing.T) {
	recorder := &channelImageAuditRecorder{}
	receipt, err := json.Marshal(imagegen.JobReceipt{GenerationID: "gen-accepted", BatchID: "batch-1", Status: imagegen.JobStatus(imagegen.GenerationStatusQueued), AcceptedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	input := channelcontract.RunInput{TenantID: 7, UserID: 11, TenantSessionID: 13, RunID: "run-1", Prompt: "private prompt"}
	auditChannelImageToolReceipts(context.Background(), recorder, input, []query.ToolTrace{{Name: "GenerateImage", Output: string(receipt)}})
	if len(recorder.inputs) != 1 {
		t.Fatalf("audit inputs=%+v", recorder.inputs)
	}
	audit := recorder.inputs[0]
	if audit.Action != channelruntime.ChannelImageAuditEnqueued || audit.TenantID != 7 || audit.ActorUserID != 11 || audit.ResourceID != "gen-accepted" || !strings.Contains(audit.MetadataJSON, "batch-1") || strings.Contains(audit.MetadataJSON, input.Prompt) {
		t.Fatalf("unsafe audit=%+v", audit)
	}
}

func TestResolveChannelImageAttachmentEnforcesAssetScope(t *testing.T) {
	mediaStore := media.NewMemoryStore()
	blobs := imagegen.NewMemoryBlobStore()
	policy := media.AccessPolicy{TenantID: 7, UserID: 11, SessionID: 13}
	key := "tenant-7/user-11/session-13/asset-a.png"
	blob, err := blobs.Put(context.Background(), imagegen.PutBlobRequest{Policy: policy, Key: key, MediaType: "image/png", Name: "asset.png", Data: bytes.NewReader([]byte("png"))})
	if err != nil {
		t.Fatal(err)
	}
	if err := mediaStore.Put(context.Background(), media.Asset{AssetID: "asset-a", Kind: media.KindImage, MediaType: "image/png", Name: "asset.png", SizeBytes: blob.SizeBytes, TenantID: 7, UserID: 11, SessionID: 13, State: media.StateReady, Original: media.Variant{Path: key}, Access: policy}); err != nil {
		t.Fatal(err)
	}
	got, err := resolveChannelImageAttachment(context.Background(), mediaStore, blobs, channelcontract.Attachment{ID: "asset-a", Type: "image", TenantID: 7, UserID: 11, SessionID: 13})
	if err != nil || string(got) != "png" {
		t.Fatalf("resolved bytes=%q err=%v", got, err)
	}
	if _, err := resolveChannelImageAttachment(context.Background(), mediaStore, blobs, channelcontract.Attachment{ID: "asset-a", Type: "image", TenantID: 8, UserID: 11, SessionID: 13}); err == nil {
		t.Fatal("cross-tenant asset was resolved")
	}
}
