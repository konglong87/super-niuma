package computeruse

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"reflect"
	"strings"
	"testing"
	"time"

	cu "github.com/konglong87/super-niuma/internal/computeruse"
	"github.com/konglong87/super-niuma/internal/tools"
)

const (
	testComputerSession = "computer-1"
	testBeforeImageID   = "before-1"
	testAfterImageID    = "after-1"
	testFreshImageID    = "obs-1"
	testPrivateError    = "private-input-password-123"
	callExecute         = "execute"
	callObserve         = "observe"
	callImagePrefix     = "image:"
)

type stubImage struct {
	data      []byte
	mediaType string
	err       error
}

type serviceStub struct {
	observation    cu.Observation
	receipt        cu.ActionReceipt
	image          []byte
	imageType      string
	lastAction     cu.Action
	owners         []cu.SessionOwner
	calls          int
	imageID        string
	serviceErr     error
	imageErr       error
	observeErr     error
	images         map[string]stubImage
	order          []string
	contexts       []context.Context
	imageSessions  []string
	lastObserve    cu.ObserveRequest
	afterImageRead func(string)
}

func (s *serviceStub) record(owner cu.SessionOwner) { s.calls++; s.owners = append(s.owners, owner) }
func (s *serviceStub) Capabilities(_ context.Context, owner cu.SessionOwner, _ string) (cu.Capabilities, error) {
	s.record(owner)
	return s.observation.Capabilities, s.serviceErr
}
func (s *serviceStub) Observe(ctx context.Context, owner cu.SessionOwner, request cu.ObserveRequest) (cu.Observation, error) {
	s.record(owner)
	s.order = append(s.order, callObserve)
	s.contexts = append(s.contexts, ctx)
	s.lastObserve = request
	if s.observeErr != nil {
		return cu.Observation{}, s.observeErr
	}
	out := s.observation
	out.SessionID = request.SessionID
	return out, s.serviceErr
}
func (s *serviceStub) Execute(ctx context.Context, owner cu.SessionOwner, action cu.Action) (cu.ActionReceipt, error) {
	s.record(owner)
	s.order = append(s.order, callExecute)
	s.contexts = append(s.contexts, ctx)
	s.lastAction = action
	out := s.receipt
	out.ActionID = action.ID
	out.SessionID = action.SessionID
	return out, s.serviceErr
}
func (s *serviceStub) Pause(_ context.Context, owner cu.SessionOwner, _ string) error {
	s.record(owner)
	return s.serviceErr
}
func (s *serviceStub) Resume(_ context.Context, owner cu.SessionOwner, _ string) error {
	s.record(owner)
	return s.serviceErr
}
func (s *serviceStub) Stop(_ context.Context, owner cu.SessionOwner, _ string) error {
	s.record(owner)
	return s.serviceErr
}
func (s *serviceStub) ObservationImage(ctx context.Context, owner cu.SessionOwner, sessionID, id string) ([]byte, string, error) {
	s.record(owner)
	s.imageID = id
	s.order = append(s.order, callImagePrefix+id)
	s.contexts = append(s.contexts, ctx)
	s.imageSessions = append(s.imageSessions, sessionID)
	if s.afterImageRead != nil {
		s.afterImageRead(id)
	}
	if image, ok := s.images[id]; ok {
		return image.data, image.mediaType, image.err
	}
	return s.image, s.imageType, s.imageErr
}

type coordinatingService struct {
	*serviceStub
	sessionID             string
	starts                int
	forceEmptyObservation bool
}

func (s *coordinatingService) EnsureComputerSession(context.Context, cu.SessionOwner) (string, error) {
	s.starts++
	return s.sessionID, nil
}

func (s *coordinatingService) CurrentComputerSession(context.Context, cu.SessionOwner) (string, error) {
	return s.sessionID, nil
}

func (s *coordinatingService) CurrentComputerObservation(context.Context, cu.SessionOwner) (string, error) {
	if s.forceEmptyObservation {
		return "", nil
	}
	return s.observation.ID, nil
}

func testContext(service cu.Service) tools.Context {
	return tools.Context{TenantID: 7, UserID: 11, SessionID: 13, ComputerUse: service, ComputerUseImageSupported: true, Invocation: tools.Invocation{RunID: "run-1", ToolUseID: "tool-1"}}
}
func realPNG(t *testing.T) []byte {
	t.Helper()
	return sizedPNG(t, 10, 10)
}
func sizedPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, width, height))); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
func screenshotService(t *testing.T) *serviceStub {
	return &serviceStub{observation: cu.Observation{ID: testFreshImageID, Width: 10, Height: 10}, image: realPNG(t), imageType: pngMediaType}
}
func runRequest(service cu.Service, input string) tools.Result {
	return New().Run(context.Background(), json.RawMessage(input), testContext(service))
}
func assertImage(t *testing.T, result tools.Result, want []byte) {
	t.Helper()
	if len(result.ContextMessages) != 1 || len(result.ContextMessages[0].Content) != 2 {
		t.Fatalf("missing image: %+v", result)
	}
	block := result.ContextMessages[0].Content[1]
	if block.Type != "image" || block.Source == nil || block.Source.MediaType != pngMediaType {
		t.Fatalf("invalid block: %+v", block)
	}
	data, err := base64.StdEncoding.DecodeString(block.Source.Data)
	if err != nil || !bytes.Equal(data, want) {
		t.Fatalf("image mismatch: %v", err)
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
}
func TestObserveLazilyCoordinatesMissingSessionID(t *testing.T) {
	base := screenshotService(t)
	service := &coordinatingService{serviceStub: base, sessionID: testComputerSession}
	result := New().Run(context.Background(), json.RawMessage(`{"action":"observe"}`), testContext(service))
	if result.IsError {
		t.Fatal(result.Content)
	}
	if service.starts != 1 || service.lastObserve.SessionID != testComputerSession {
		t.Fatalf("lazy session binding did not run: starts=%d observe=%+v", service.starts, service.lastObserve)
	}
}

func TestSubsequentActionBindsCurrentSessionWhenModelOmitsID(t *testing.T) {
	base := screenshotService(t)
	base.receipt = cu.ActionReceipt{Outcome: cu.OutcomeRejected, Verification: cu.VerificationNotChecked}
	service := &coordinatingService{serviceStub: base, sessionID: testComputerSession}
	result := New().Run(context.Background(), json.RawMessage(`{"action":"move","x":10,"y":10}`), testContext(service))
	if service.lastAction.SessionID != testComputerSession {
		t.Fatalf("bound session=%q, want %q; result=%s", service.lastAction.SessionID, testComputerSession, result.Content)
	}
	if service.lastAction.ObservationID != testFreshImageID {
		t.Fatalf("bound observation=%q, want %q; result=%s", service.lastAction.ObservationID, testFreshImageID, result.Content)
	}
}

func TestObservePassesRealScreenshotImageContext(t *testing.T) {
	service := screenshotService(t)
	result := runRequest(service, `{"session_id":"computer-1","action":"observe"}`)
	if result.IsError {
		t.Fatal(result.Content)
	}
	assertImage(t, result, service.image)
	for _, owner := range service.owners {
		if owner != (cu.SessionOwner{TenantID: 7, UserID: 11, SessionID: 13}) {
			t.Fatalf("untrusted owner: %+v", owner)
		}
	}
}
func TestClonedToolUsesOnlyCurrentTrustedCapability(t *testing.T) {
	registry := tools.NewRegistry(New()).Clone()
	tool, ok := registry.Get(ToolName)
	if !ok {
		t.Fatal("missing tool")
	}
	first, second := screenshotService(t), screenshotService(t)
	input := json.RawMessage(`{"session_id":"computer-1","action":"observe"}`)
	for _, service := range []*serviceStub{first, second} {
		if result := tool.Run(context.Background(), input, testContext(service)); result.IsError {
			t.Fatal(result.Content)
		}
	}
	if first.calls != 2 || second.calls != 2 {
		t.Fatalf("wrong services: %d / %d", first.calls, second.calls)
	}
	if result := tool.Run(context.Background(), input, testContext(nil)); !result.IsError {
		t.Fatal("reused previous capability")
	}
	if first.calls != 2 || second.calls != 2 {
		t.Fatal("retained service called")
	}
}
func TestRejectsUntrustedContextBeforeServiceCall(t *testing.T) {
	cases := []struct {
		name   string
		change func(*tools.Context)
	}{
		{"subagent", func(tc *tools.Context) { tc.SubagentDepth = 1 }},
		{"deep subagent", func(tc *tools.Context) { tc.SubagentDepth = 5 }},
		{"no service", func(tc *tools.Context) { tc.ComputerUse = nil }},
		{"no image support", func(tc *tools.Context) { tc.ComputerUseImageSupported = false }},
		{"no tenant", func(tc *tools.Context) { tc.TenantID = 0 }},
		{"no user", func(tc *tools.Context) { tc.UserID = 0 }},
		{"no conversation", func(tc *tools.Context) { tc.SessionID = 0 }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			service := screenshotService(t)
			tc := testContext(service)
			test.change(&tc)
			result := New().Run(context.Background(), json.RawMessage(`{"session_id":"computer-1","action":"observe"}`), tc)
			if !result.IsError || service.calls != 0 {
				t.Fatalf("not fail closed: %+v calls=%d", result, service.calls)
			}
		})
	}
}
func TestRejectsModelAuthorityAndMalformedInput(t *testing.T) {
	for _, input := range []string{
		`{"session_id":"computer-1","action":"resume"}`,
		`{"session_id":"computer-1","action":"start"}`,
		`{"session_id":"computer-1","action":"approve"}`,
		`{"session_id":"computer-1","action":"type","action_id":"model-id"}`,
		`{"session_id":"computer-1","action":"observe","unknown":true}`,
		`{"session_id":"computer-1","action":"observe"} {}`,
		`{"action":"observe"}`, `null`,
	} {
		t.Run(input, func(t *testing.T) {
			service := screenshotService(t)
			result := runRequest(service, input)
			if !result.IsError || service.calls != 0 {
				t.Fatalf("not rejected: %+v", result)
			}
		})
	}
	schema := string(New().InputSchema())
	for _, forbidden := range []string{`"resume"`, `"start"`, `"approve"`, `"action_id"`} {
		if strings.Contains(schema, forbidden) {
			t.Fatalf("model authority in schema: %s", forbidden)
		}
	}
}
func TestInvocationActionIDRequiredUniqueAndStable(t *testing.T) {
	service := screenshotService(t)
	service.receipt = cu.ActionReceipt{Outcome: cu.OutcomeExecuted, AfterObservationID: testAfterImageID}
	input := json.RawMessage(`{"session_id":"computer-1","action":"type","observation_id":"obs-1","text":"password-123"}`)
	base := testContext(service)
	for _, invocation := range []tools.Invocation{{}, {RunID: "run-1"}, {ToolUseID: "tool-1"}} {
		tc := base
		tc.Invocation = invocation
		if result := New().Run(context.Background(), input, tc); !result.IsError || service.calls != 0 {
			t.Fatalf("missing invocation accepted: %+v", result)
		}
	}
	contexts := []tools.Context{base, base, base, base, base}
	contexts[1].Invocation.RunID = "run-2"
	contexts[2].Invocation.ToolUseID = "tool-2"
	contexts[3].Invocation.BatchID = "batch-2"
	contexts[4].SessionID++
	ids := map[string]bool{}
	for _, tc := range contexts {
		result := New().Run(context.Background(), input, tc)
		if result.IsError || service.lastAction.ID == "" || ids[service.lastAction.ID] {
			t.Fatalf("invalid identity: %+v %+v", result, service.lastAction)
		}
		ids[service.lastAction.ID] = true
		if strings.Contains(result.Content, "password-123") {
			t.Fatal("leaked input")
		}
		id := service.lastAction.ID
		New().Run(context.Background(), input, tc)
		if service.lastAction.ID != id {
			t.Fatal("same invocation changed ID")
		}
		if service.lastAction.Text != "password-123" {
			t.Fatal("lost input")
		}
	}
}
func TestRejectsFakeCorruptAndWrongTypePNG(t *testing.T) {
	valid := realPNG(t)
	for _, test := range []struct {
		name      string
		data      []byte
		mediaType string
	}{
		{"fake", []byte("png"), pngMediaType}, {"signature", []byte("\x89PNG\r\n\x1a\n"), pngMediaType},
		{"truncated body", valid[:len(valid)/2], pngMediaType}, {"empty", nil, pngMediaType},
		{"wrong MIME", valid, "image/jpeg"}, {"oversized", make([]byte, maxPNGBytes+1), pngMediaType},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := screenshotService(t)
			service.image = test.data
			service.imageType = test.mediaType
			result := runRequest(service, `{"session_id":"computer-1","action":"observe"}`)
			if !result.IsError || len(result.ContextMessages) != 0 {
				t.Fatalf("bad image accepted: %+v", result)
			}
		})
	}
}
func TestExecutePreservesReceiptAndSelectsFreshOrEvidenceImage(t *testing.T) {
	for _, outcome := range []cu.Outcome{cu.OutcomeExecuted, cu.OutcomeUnknown} {
		t.Run(string(outcome), func(t *testing.T) {
			service := screenshotService(t)
			service.receipt = cu.ActionReceipt{Outcome: outcome, AfterObservationID: "after-1", BeforeObservationID: "before-1", ErrorMessage: "password-123", RedactedActionSummary: "password-123"}
			if outcome == cu.OutcomeUnknown {
				service.serviceErr = errors.New("password-123")
			}
			result := runRequest(service, `{"session_id":"computer-1","action":"type","observation_id":"obs-1","text":"password-123"}`)
			if result.IsError != (outcome == cu.OutcomeUnknown) || strings.Contains(result.Content, "password-123") {
				t.Fatalf("wrong error: %+v", result)
			}
			var payload struct {
				Receipt cu.ActionReceipt `json:"receipt"`
			}
			if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Receipt.Outcome != outcome || payload.Receipt.BeforeObservationID != "before-1" || payload.Receipt.AfterObservationID != "after-1" || payload.Receipt.ActionID != service.lastAction.ID {
				t.Fatalf("lost receipt: %+v", payload)
			}
			wantImageID := testAfterImageID
			wantOrder := []string{callExecute, callImagePrefix + testAfterImageID}
			if outcome == cu.OutcomeExecuted {
				wantImageID = testFreshImageID
				wantOrder = append(wantOrder, callObserve, callImagePrefix+testFreshImageID)
			}
			if service.imageID != wantImageID {
				t.Fatalf("image ID = %q, want %q", service.imageID, wantImageID)
			}
			assertServiceOrder(t, service, wantOrder...)
			assertImage(t, result, service.image)
		})
	}
}
func TestServiceErrorsNeverExposeRawStrings(t *testing.T) {
	for _, action := range []string{"observe", "type", "pause", "stop"} {
		t.Run(action, func(t *testing.T) {
			service := screenshotService(t)
			service.serviceErr = errors.New("secret-input")
			request := `{"session_id":"computer-1","action":"` + action + `"}`
			if action == "type" {
				request = `{"session_id":"computer-1","action":"type","observation_id":"obs-1"}`
			}
			result := runRequest(service, request)
			if !result.IsError || strings.Contains(result.Content, "secret-input") {
				t.Fatalf("unsafe error: %+v", result)
			}
		})
	}
	service := screenshotService(t)
	service.imageErr = errors.New("secret-input")
	result := runRequest(service, `{"session_id":"computer-1","action":"observe"}`)
	if !result.IsError || strings.Contains(result.Content, "secret-input") {
		t.Fatalf("unsafe image error: %+v", result)
	}
}
func TestAfterImageFailureDoesNotDiscardUnknownReceipt(t *testing.T) {
	service := screenshotService(t)
	service.receipt = cu.ActionReceipt{Outcome: cu.OutcomeUnknown, AfterObservationID: "after-1"}
	service.serviceErr = errors.New("input-secret")
	service.imageErr = errors.New("image-secret")
	result := runRequest(service, `{"session_id":"computer-1","action":"click","observation_id":"obs-1"}`)
	if !result.IsError || !strings.Contains(result.Content, string(cu.OutcomeUnknown)) || strings.Contains(result.Content, "secret") || len(result.ContextMessages) != 0 {
		t.Fatalf("unsafe receipt: %+v", result)
	}
	assertServiceOrder(t, service, callExecute, callImagePrefix+testAfterImageID)
	assertOriginalReceipt(t, result, service)
}
func TestToolIsSerial(t *testing.T) {
	if got := New().ExecutionPolicy().Concurrency; got != tools.ConcurrencySerial {
		t.Fatalf("concurrency = %s", got)
	}
}

// Only a successful action refreshes the actionable Controller observation.
// Unknown outcomes retain evidence-only guidance and must never replay input.
func TestScreenshotContextDistinguishesObservationFromActionEvidence(t *testing.T) {
	for _, action := range []string{"observe", "click", "unknown"} {
		t.Run(action, func(t *testing.T) {
			service := screenshotService(t)
			service.receipt = cu.ActionReceipt{Outcome: cu.OutcomeExecuted, AfterObservationID: "after-1"}
			requestAction := action
			if action == "unknown" {
				requestAction = "click"
				service.receipt.Outcome = cu.OutcomeUnknown
			}
			request := `{"session_id":"computer-1","action":"` + requestAction + `"}`
			if requestAction != "observe" {
				request = `{"session_id":"computer-1","action":"` + requestAction + `","observation_id":"obs-1"}`
			}
			result := runRequest(service, request)
			if result.IsError != (action == "unknown") {
				t.Fatal(result.Content)
			}
			assertImage(t, result, service.image)
			caption := result.ContextMessages[0].Content[0].Text
			for _, want := range []string{"10x10 pixels", "top-left", "Do not divide by scale_factor"} {
				if !strings.Contains(caption, want) {
					t.Errorf("caption missing %q: %s", want, caption)
				}
			}
			if action != "unknown" {
				if !strings.Contains(caption, `"obs-1"`) || strings.Contains(caption, "evidence only") {
					t.Errorf("wrong observation caption: %s", caption)
				}
			} else {
				for _, want := range []string{`"after-1"`, "evidence only", "fresh authoritative observation", "not an actionable observation"} {
					if !strings.Contains(caption, want) {
						t.Errorf("caption missing %q: %s", want, caption)
					}
				}
			}
		})
	}
}

func assertServiceOrder(t *testing.T, service *serviceStub, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(service.order, want) || service.calls != len(want) {
		t.Fatalf("service calls = %v (%d total), want %v; input must never be replayed", service.order, service.calls, want)
	}
}

func resultPayload(t *testing.T, result tools.Result) map[string]json.RawMessage {
	t.Helper()
	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func assertOriginalReceipt(t *testing.T, result tools.Result, service *serviceStub) {
	t.Helper()
	var got cu.ActionReceipt
	if err := json.Unmarshal(resultPayload(t, result)["receipt"], &got); err != nil {
		t.Fatal(err)
	}
	want := service.receipt
	want.ActionID = service.lastAction.ID
	want.SessionID = service.lastAction.SessionID
	want.ErrorMessage = ""
	if want.ErrorCode != "" {
		want.ErrorCode = "action_failed"
	}
	want.RedactedActionSummary = string(service.lastAction.Kind)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("receipt changed beyond expected privacy redactions:\ngot  %+v\nwant %+v", got, want)
	}
}

func assertSanitizedResult(t *testing.T, result tools.Result) {
	t.Helper()
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), testPrivateError) {
		t.Fatalf("private service/input data leaked: %s", encoded)
	}
}

func assertPayloadCode(t *testing.T, result tools.Result, key, want string) {
	t.Helper()
	var got string
	if err := json.Unmarshal(resultPayload(t, result)[key], &got); err != nil {
		t.Fatalf("missing %s in %s: %v", key, result.Content, err)
	}
	if got != want {
		t.Fatalf("%s = %q, want %q", key, got, want)
	}
}

func intPtr(value int) *int { return &value }

func executedScreenshotService(t *testing.T) *serviceStub {
	t.Helper()
	service := screenshotService(t)
	service.receipt = cu.ActionReceipt{
		Outcome: cu.OutcomeExecuted, BeforeObservationID: testBeforeImageID, AfterObservationID: testAfterImageID,
		Verification: cu.VerificationNotChecked, ActualPoint: &cu.Point{X: 3, Y: 4},
		ErrorMessage: testPrivateError, RedactedActionSummary: testPrivateError,
	}
	// Different bytes and dimensions ensure assertions cannot accidentally accept
	// the receipt's evidence image as the new actionable observation image.
	service.observation.Width, service.observation.Height = 12, 8
	service.images = map[string]stubImage{
		testAfterImageID: {data: service.image, mediaType: pngMediaType},
		testFreshImageID: {data: sizedPNG(t, 12, 8), mediaType: pngMediaType},
	}
	return service
}

func TestExecutedActionAutomaticallyCapturesFreshObservation(t *testing.T) {
	for _, kind := range []cu.ActionKind{
		cu.ActionClick, cu.ActionDoubleClick, cu.ActionRightClick, cu.ActionMove, cu.ActionDrag,
		cu.ActionType, cu.ActionKey, cu.ActionHotkey, cu.ActionScroll, cu.ActionWait,
	} {
		t.Run(string(kind), func(t *testing.T) {
			service := executedScreenshotService(t)
			const displayID, windowID = "display-2", "window-3"
			service.observation.DisplayID, service.observation.WindowID = displayID, windowID
			input, err := json.Marshal(request{
				SessionID: testComputerSession, Action: string(kind), ObservationID: testBeforeImageID,
				DisplayID: displayID, WindowID: windowID, Text: testPrivateError,
				StartX: intPtr(1), StartY: intPtr(2), X: intPtr(3), Y: intPtr(4), Button: "left",
			})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := New().Run(ctx, input, testContext(service))
			if result.IsError {
				t.Fatal(result.Content)
			}
			assertServiceOrder(t, service, callExecute, callImagePrefix+testAfterImageID, callObserve, callImagePrefix+testFreshImageID)
			assertOriginalReceipt(t, result, service)
			assertSanitizedResult(t, result)
			wantRequest := cu.ObserveRequest{SessionID: testComputerSession, DisplayID: displayID, WindowID: windowID}
			if service.lastObserve != wantRequest {
				t.Fatalf("Observe request = %+v, want %+v", service.lastObserve, wantRequest)
			}
			if action := service.lastAction; action.SessionID != testComputerSession || action.ObservationID != testBeforeImageID || action.DisplayID != displayID || action.WindowID != windowID || action.Kind != kind || action.Text != testPrivateError {
				t.Fatalf("input target or payload changed: %+v", action)
			}
			for i, owner := range service.owners {
				if owner != (cu.SessionOwner{TenantID: 7, UserID: 11, SessionID: 13}) || service.contexts[i] != ctx {
					t.Fatalf("call %d lost trusted owner/context: %+v", i, owner)
				}
			}
			if !reflect.DeepEqual(service.imageSessions, []string{testComputerSession, testComputerSession}) {
				t.Fatalf("image reads lost session: %v", service.imageSessions)
			}
			var observation cu.Observation
			if err := json.Unmarshal(resultPayload(t, result)["observation"], &observation); err != nil {
				t.Fatal(err)
			}
			wantObservation := service.observation
			wantObservation.SessionID = testComputerSession
			if !reflect.DeepEqual(observation, wantObservation) {
				t.Fatalf("fresh observation metadata = %+v, want %+v", observation, wantObservation)
			}
			assertImage(t, result, service.images[testFreshImageID].data)
			caption := result.ContextMessages[0].Content[0].Text
			for _, want := range []string{`"` + testFreshImageID + `"`, "12x8 pixels", "Reference this observation ID", "top-left", "Do not divide by scale_factor"} {
				if !strings.Contains(caption, want) {
					t.Errorf("actionable caption missing %q: %s", want, caption)
				}
			}
			if strings.Contains(caption, testAfterImageID) || strings.Contains(caption, "evidence only") {
				t.Fatalf("fresh image mislabeled as receipt evidence: %s", caption)
			}
		})
	}
}

func TestExecuteDoesNotObserveNonExecutedOrErroredReceipt(t *testing.T) {
	for _, outcome := range []cu.Outcome{cu.OutcomeExecuted, cu.OutcomeUnknown, cu.OutcomeRejected, cu.OutcomeFailed, cu.OutcomeNotStarted, ""} {
		for _, hasError := range []bool{false, true} {
			if outcome == cu.OutcomeExecuted && !hasError {
				continue
			}
			name := string(outcome) + "/nil-error"
			if hasError {
				name = string(outcome) + "/service-error"
			}
			t.Run(name, func(t *testing.T) {
				service := executedScreenshotService(t)
				service.receipt.Outcome = outcome
				service.receipt.ErrorCode = testPrivateError
				if hasError {
					service.serviceErr = errors.New(testPrivateError)
				}
				result := runRequest(service, `{"session_id":"computer-1","action":"click","observation_id":"obs-1"}`)
				if !result.IsError {
					t.Fatalf("non-successful receipt accepted: %s", result.Content)
				}
				assertPayloadCode(t, result, "error_code", "action_failed")
				assertServiceOrder(t, service, callExecute, callImagePrefix+testAfterImageID)
				assertOriginalReceipt(t, result, service)
				assertSanitizedResult(t, result)
				assertImage(t, result, service.images[testAfterImageID].data)
				if _, exists := resultPayload(t, result)["observation"]; exists {
					t.Fatalf("unrefreshed observation published: %s", result.Content)
				}
				caption := result.ContextMessages[0].Content[0].Text
				if !strings.Contains(caption, "evidence only") || !strings.Contains(caption, testAfterImageID) {
					t.Fatalf("error receipt image marked actionable: %s", caption)
				}
			})
		}
	}
}

func TestExecuteRequiresValidAfterImageBeforeAutomaticObserve(t *testing.T) {
	valid := realPNG(t)
	for _, test := range []struct {
		name    string
		afterID string
		image   stubImage
	}{
		{name: "missing ID"},
		{name: "blank ID", afterID: " \t "},
		{name: "read error", afterID: testAfterImageID, image: stubImage{err: errors.New(testPrivateError)}},
		{name: "empty", afterID: testAfterImageID, image: stubImage{mediaType: pngMediaType}},
		{name: "fake", afterID: testAfterImageID, image: stubImage{data: []byte(testPrivateError), mediaType: pngMediaType}},
		{name: "signature only", afterID: testAfterImageID, image: stubImage{data: []byte("\x89PNG\r\n\x1a\n"), mediaType: pngMediaType}},
		{name: "truncated", afterID: testAfterImageID, image: stubImage{data: valid[:len(valid)/2], mediaType: pngMediaType}},
		{name: "wrong MIME", afterID: testAfterImageID, image: stubImage{data: valid, mediaType: "image/jpeg"}},
		{name: "oversized", afterID: testAfterImageID, image: stubImage{data: make([]byte, maxPNGBytes+1), mediaType: pngMediaType}},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := executedScreenshotService(t)
			service.receipt.AfterObservationID = test.afterID
			service.images[test.afterID] = test.image
			result := runRequest(service, `{"session_id":"computer-1","action":"click","observation_id":"obs-1"}`)
			if !result.IsError || len(result.ContextMessages) != 0 {
				t.Fatalf("invalid after evidence accepted: %+v", result)
			}
			assertPayloadCode(t, result, "image_error_code", "observation_image_failed")
			wantOrder := []string{callExecute}
			if strings.TrimSpace(test.afterID) != "" {
				wantOrder = append(wantOrder, callImagePrefix+test.afterID)
			}
			assertServiceOrder(t, service, wantOrder...)
			assertOriginalReceipt(t, result, service)
			assertSanitizedResult(t, result)
			if _, exists := resultPayload(t, result)["observation"]; exists {
				t.Fatalf("invalid after image triggered observation: %s", result.Content)
			}
		})
	}
}

func TestPostActionCaptureFailurePreservesReceiptAndEvidenceWithoutReplay(t *testing.T) {
	valid := realPNG(t)
	for _, test := range []struct {
		name       string
		observeErr error
		freshID    string
		image      stubImage
	}{
		{name: "Observe error", observeErr: errors.New(testPrivateError)},
		{name: "missing fresh ID"},
		{name: "fresh read error", freshID: testFreshImageID, image: stubImage{err: errors.New(testPrivateError)}},
		{name: "fresh empty image", freshID: testFreshImageID, image: stubImage{mediaType: pngMediaType}},
		{name: "fresh fake image", freshID: testFreshImageID, image: stubImage{data: []byte(testPrivateError), mediaType: pngMediaType}},
		{name: "fresh truncated image", freshID: testFreshImageID, image: stubImage{data: valid[:len(valid)/2], mediaType: pngMediaType}},
		{name: "fresh wrong MIME", freshID: testFreshImageID, image: stubImage{data: valid, mediaType: "image/jpeg"}},
		{name: "fresh oversized image", freshID: testFreshImageID, image: stubImage{data: make([]byte, maxPNGBytes+1), mediaType: pngMediaType}},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := executedScreenshotService(t)
			service.observeErr = test.observeErr
			service.observation.ID = test.freshID
			service.images[test.freshID] = test.image
			result := runRequest(service, `{"session_id":"computer-1","action":"type","observation_id":"obs-1","text":"`+testPrivateError+`"}`)
			if !result.IsError {
				t.Fatalf("post-action capture failure ignored: %s", result.Content)
			}
			assertPayloadCode(t, result, "error_code", "post_action_observation_failed")
			wantOrder := []string{callExecute, callImagePrefix + testAfterImageID, callObserve}
			if test.observeErr == nil && test.freshID != "" {
				wantOrder = append(wantOrder, callImagePrefix+test.freshID)
			}
			assertServiceOrder(t, service, wantOrder...)
			assertOriginalReceipt(t, result, service)
			assertSanitizedResult(t, result)
			assertImage(t, result, service.images[testAfterImageID].data)
			if _, exists := resultPayload(t, result)["observation"]; exists {
				t.Fatalf("failed refresh published actionable metadata: %s", result.Content)
			}
			caption := result.ContextMessages[0].Content[0].Text
			if !strings.Contains(caption, "evidence only") || !strings.Contains(caption, testAfterImageID) || strings.Contains(caption, testFreshImageID) {
				t.Fatalf("failed refresh lost evidence-only guidance: %s", caption)
			}
		})
	}
}

func TestCanceledExecutionDoesNotRefreshOrReplayInput(t *testing.T) {
	service := screenshotService(t)
	service.receipt.Outcome = cu.OutcomeUnknown
	service.serviceErr = context.Canceled
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := New().Run(ctx, json.RawMessage(`{"session_id":"computer-1","action":"click","observation_id":"obs-1"}`), testContext(service))
	if !result.IsError {
		t.Fatal("canceled execution accepted")
	}
	assertServiceOrder(t, service, callExecute)
	assertOriginalReceipt(t, result, service)
}

func TestCancellationAfterEvidenceReadDoesNotAutomaticallyObserve(t *testing.T) {
	service := executedScreenshotService(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.afterImageRead = func(id string) {
		if id == testAfterImageID {
			cancel()
		}
	}
	result := New().Run(ctx, json.RawMessage(`{"session_id":"computer-1","action":"click","observation_id":"obs-1"}`), testContext(service))
	if !result.IsError {
		t.Fatalf("canceled post-action capture accepted: %s", result.Content)
	}
	assertPayloadCode(t, result, "error_code", "post_action_observation_failed")
	assertServiceOrder(t, service, callExecute, callImagePrefix+testAfterImageID)
	assertOriginalReceipt(t, result, service)
	assertImage(t, result, service.images[testAfterImageID].data)
	if _, exists := resultPayload(t, result)["observation"]; exists {
		t.Fatalf("canceled refresh published observation: %s", result.Content)
	}
}

func TestAutomaticObserveUsesOnlyCurrentTrustedService(t *testing.T) {
	tool := New()
	first, second := executedScreenshotService(t), executedScreenshotService(t)
	second.observation.ID = "second-fresh"
	second.images[second.observation.ID] = stubImage{data: sizedPNG(t, 9, 7), mediaType: pngMediaType}
	input := json.RawMessage(`{"session_id":"computer-1","action":"click","observation_id":"obs-1"}`)
	for _, service := range []*serviceStub{first, second} {
		result := tool.Run(context.Background(), input, testContext(service))
		if result.IsError {
			t.Fatal(result.Content)
		}
		assertServiceOrder(t, service, callExecute, callImagePrefix+testAfterImageID, callObserve, callImagePrefix+service.observation.ID)
		assertImage(t, result, service.images[service.observation.ID].data)
	}
	if result := tool.Run(context.Background(), input, testContext(nil)); !result.IsError {
		t.Fatal("missing service reused a previous capability")
	}
	if first.calls != 4 || second.calls != 4 {
		t.Fatalf("retained service called: %d / %d", first.calls, second.calls)
	}
}

// Embedding only Service intentionally hides the optional ObservationImage
// capability without changing any of the trusted execution methods.
type serviceWithoutImages struct{ cu.Service }

func TestExecuteWithoutImageProviderDoesNotAutomaticallyObserve(t *testing.T) {
	service := executedScreenshotService(t)
	result := runRequest(serviceWithoutImages{Service: service}, `{"session_id":"computer-1","action":"click","observation_id":"obs-1"}`)
	if !result.IsError || len(result.ContextMessages) != 0 {
		t.Fatalf("missing image provider accepted: %+v", result)
	}
	assertPayloadCode(t, result, "image_error_code", "observation_image_failed")
	assertServiceOrder(t, service, callExecute)
	assertOriginalReceipt(t, result, service)
	assertSanitizedResult(t, result)
	if _, exists := resultPayload(t, result)["observation"]; exists {
		t.Fatalf("missing image provider triggered observation: %s", result.Content)
	}
}

func TestFastPathSkipsReceiptEvidenceAndUsesFreshObservation(t *testing.T) {
	service := executedScreenshotService(t)
	tc := testContext(service)
	tc.ComputerUseFastPath = true

	result := New().Run(context.Background(), json.RawMessage(`{"session_id":"computer-1","action":"click","observation_id":"before-1"}`), tc)
	if result.IsError {
		t.Fatal(result.Content)
	}
	assertServiceOrder(t, service, callExecute, callObserve, callImagePrefix+testFreshImageID)
	assertOriginalReceipt(t, result, service)
	assertImage(t, result, service.images[testFreshImageID].data)
	if service.imageID == testAfterImageID {
		t.Fatalf("fast path fetched discarded receipt image %q", service.imageID)
	}
	if _, exists := resultPayload(t, result)["observation"]; !exists {
		t.Fatal("fast path omitted fresh observation metadata")
	}
}

func TestFastPathRetainsEvidenceForUnknownOutcome(t *testing.T) {
	service := screenshotService(t)
	service.receipt = cu.ActionReceipt{Outcome: cu.OutcomeUnknown, AfterObservationID: testAfterImageID}
	service.serviceErr = errors.New(testPrivateError)
	tc := testContext(service)
	tc.ComputerUseFastPath = true

	result := New().Run(context.Background(), json.RawMessage(`{"session_id":"computer-1","action":"click","observation_id":"before-1"}`), tc)
	if !result.IsError {
		t.Fatal("unknown outcome was accepted")
	}
	assertServiceOrder(t, service, callExecute, callImagePrefix+testAfterImageID)
	assertOriginalReceipt(t, result, service)
	assertImage(t, result, service.image)
	if _, exists := resultPayload(t, result)["observation"]; exists {
		t.Fatal("unknown outcome published a fresh observation")
	}
}

func TestFastPathFreshObservationFailureFailsClosedWithoutReplay(t *testing.T) {
	service := executedScreenshotService(t)
	service.observeErr = errors.New(testPrivateError)
	tc := testContext(service)
	tc.ComputerUseFastPath = true

	result := New().Run(context.Background(), json.RawMessage(`{"session_id":"computer-1","action":"click","observation_id":"before-1"}`), tc)
	if !result.IsError {
		t.Fatal("fresh observation failure was accepted")
	}
	assertPayloadCode(t, result, "error_code", "post_action_observation_failed")
	assertServiceOrder(t, service, callExecute, callObserve)
	assertOriginalReceipt(t, result, service)
	if len(result.ContextMessages) != 0 {
		t.Fatalf("failed fast-path refresh returned discarded evidence: %+v", result.ContextMessages)
	}
	if _, exists := resultPayload(t, result)["observation"]; exists {
		t.Fatal("failed refresh published actionable metadata")
	}
}

type launchingService struct {
	*coordinatingService
	launchReceipt cu.LaunchReceipt
	launchErr     error
	launchCalls   int
}

func (s *launchingService) LaunchTarget(_ context.Context, _ cu.SessionOwner, sessionID string, targetID cu.TargetID) (cu.LaunchReceipt, error) {
	s.launchCalls++
	if sessionID != s.sessionID || targetID == "" {
		return cu.LaunchReceipt{TargetID: targetID, Outcome: cu.OutcomeRejected, ErrorCode: cu.ErrorCodeUnsupportedTarget}, errors.New("invalid launch binding")
	}
	if s.launchErr == nil && s.launchReceipt.Outcome == cu.OutcomeExecuted {
		s.observation.ActiveWindow = s.launchReceipt.Window
		s.observation.WindowID = s.launchReceipt.Window.ID
	}
	s.launchReceipt.TargetID = targetID
	return s.launchReceipt, s.launchErr
}

func TestDescriptionPrioritizesAtomicLaunchForClosedTarget(t *testing.T) {
	description := New().Description()
	for _, want := range []string{"first call", "action=launch_app", "registered target_id", "atomically launches, binds", "new authoritative observation"} {
		if !strings.Contains(description, want) {
			t.Errorf("description missing %q", want)
		}
	}
	if strings.Contains(description, "prefer observe") || strings.Contains(description, "Use launch_app only") {
		t.Fatal("description contradicts the explicit atomic launch contract")
	}
}

func TestFastPathObservationAutoLaunchesWhenAnotherAppIsFrontmost(t *testing.T) {
	service := &launchingService{
		coordinatingService: &coordinatingService{serviceStub: screenshotService(t), sessionID: testComputerSession},
		launchReceipt: cu.LaunchReceipt{
			Application: cu.ApplicationWorkBuddy, BundleID: cu.WorkBuddyBundleID,
			Window:  cu.WindowRef{ID: "workbuddy-window", OwnerPID: 84, BundleID: cu.WorkBuddyBundleID},
			Outcome: cu.OutcomeExecuted, Duration: time.Millisecond, CompletedAt: time.Now(),
		},
	}
	service.forceEmptyObservation = true
	service.observation.ActiveWindow.BundleID = "com.microsoft.edgemac"
	tc := testContext(service)
	tc.ComputerUseFastPath = true
	result := New().Run(context.Background(), json.RawMessage(`{"action":"observe","target_id":"workbuddy"}`), tc)
	if result.IsError || service.launchCalls != 1 || service.lastObserve.WindowID != "workbuddy-window" {
		t.Fatalf("launchCalls=%d observe=%+v result=%s", service.launchCalls, service.lastObserve, result.Content)
	}
}

func TestFastPathObservationAutoLaunchesHostWindow(t *testing.T) {
	service := &launchingService{
		coordinatingService: &coordinatingService{serviceStub: screenshotService(t), sessionID: testComputerSession},
		launchReceipt: cu.LaunchReceipt{
			Application: cu.ApplicationWorkBuddy, BundleID: cu.WorkBuddyBundleID,
			Window:  cu.WindowRef{ID: "workbuddy-window", OwnerPID: 84, BundleID: cu.WorkBuddyBundleID},
			Outcome: cu.OutcomeExecuted, Duration: time.Millisecond, CompletedAt: time.Now(),
		},
	}
	service.forceEmptyObservation = true
	service.observation.ActiveWindow.BundleID = cu.NiumaHostBundleID
	tc := testContext(service)
	tc.ComputerUseFastPath = true
	result := New().Run(context.Background(), json.RawMessage(`{"action":"observe","target_id":"workbuddy"}`), tc)
	if result.IsError {
		t.Fatalf("result=%s", result.Content)
	}
	if service.launchCalls != 1 || !strings.Contains(result.Content, "workbuddy-window") || service.lastObserve.WindowID != "workbuddy-window" {
		t.Fatalf("launchCalls=%d observe=%+v result=%s", service.launchCalls, service.lastObserve, result.Content)
	}
}

func TestLaunchAppUsesCoordinatorAndReturnsBoundWindowReceipt(t *testing.T) {
	service := &launchingService{
		coordinatingService: &coordinatingService{serviceStub: screenshotService(t), sessionID: testComputerSession},
		launchReceipt: cu.LaunchReceipt{
			Application: cu.ApplicationWorkBuddy,
			BundleID:    cu.WorkBuddyBundleID,
			Window:      cu.WindowRef{ID: "workbuddy-window", OwnerPID: 84, BundleID: cu.WorkBuddyBundleID},
			Outcome:     cu.OutcomeExecuted,
			Duration:    time.Millisecond,
			CompletedAt: time.Now(),
		},
	}
	result := New().Run(context.Background(), json.RawMessage(`{"action":"launch_app","target_id":"workbuddy"}`), testContext(service))
	if result.IsError {
		t.Fatalf("launch result=%s", result.Content)
	}
	var payload struct {
		Receipt cu.LaunchReceipt `json:"launch_receipt"`
		Window  string           `json:"window_id"`
	}
	if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
		t.Fatal(err)
	}
	if service.starts != 0 || service.launchCalls != 1 || payload.Receipt.Window.ID != "workbuddy-window" || payload.Window != "workbuddy-window" {
		t.Fatalf("starts=%d launchCalls=%d payload=%+v", service.starts, service.launchCalls, payload)
	}
	if _, ok := resultPayload(t, result)["observation"]; !ok {
		t.Fatalf("launch result did not include the authoritative observation: %s", result.Content)
	}
	assertImage(t, result, service.image)
	assertServiceOrder(t, service.serviceStub, callObserve, callImagePrefix+testFreshImageID)
}

func TestLaunchAppRejectsUnknownApplicationBeforeService(t *testing.T) {
	service := &launchingService{
		coordinatingService: &coordinatingService{serviceStub: screenshotService(t), sessionID: testComputerSession},
		launchReceipt:       cu.LaunchReceipt{Outcome: cu.OutcomeRejected, ErrorCode: cu.ErrorCodeUnsupportedTarget, CompletedAt: time.Now()},
	}
	result := New().Run(context.Background(), json.RawMessage(`{"action":"launch_app","target_id":"safari"}`), testContext(service))
	if !result.IsError || !strings.Contains(result.Content, "unsupported_target") || service.starts != 0 || service.launchCalls != 1 {
		t.Fatalf("result=%+v starts=%d launchCalls=%d", result, service.starts, service.launchCalls)
	}
}

// Successful launch is independent evidence even when the bound capture fails.
func TestAtomicObserveRetainsLaunchReceiptOnBoundCaptureFailure(t *testing.T) {
	service := &launchingService{
		coordinatingService: &coordinatingService{serviceStub: screenshotService(t), sessionID: testComputerSession},
		launchReceipt:       cu.LaunchReceipt{BundleID: "fixture.app", Window: cu.WindowRef{ID: "fixture-window", OwnerPID: 84, BundleID: "fixture.app"}, Outcome: cu.OutcomeExecuted},
	}
	service.forceEmptyObservation = true
	service.afterImageRead = func(string) { service.observeErr = errors.New(testPrivateError) }
	tc := testContext(service)
	tc.ComputerUseFastPath = true
	result := New().Run(context.Background(), json.RawMessage(`{"action":"observe","target_id":"fixture"}`), tc)
	if !result.IsError || service.launchCalls != 1 {
		t.Fatalf("result=%s launch calls=%d", result.Content, service.launchCalls)
	}
	var payload struct {
		Launch cu.LaunchReceipt `json:"launch_receipt"`
		Window string           `json:"window_id"`
		Code   string           `json:"error_code"`
	}
	if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Launch.Outcome != cu.OutcomeExecuted || payload.Window != "fixture-window" || payload.Code == "" {
		t.Fatalf("lost launch/error evidence: %s", result.Content)
	}
	assertSanitizedResult(t, result)
}

func TestStopReturnsAuthoritativeControlReceipt(t *testing.T) {
	service := &coordinatingService{serviceStub: screenshotService(t), sessionID: testComputerSession}
	result := New().Run(context.Background(), json.RawMessage(`{"action":"stop","session_id":"invented-by-model"}`), testContext(service))
	if result.IsError {
		t.Fatalf("stop failed: %s", result.Content)
	}
	var payload struct {
		Receipt cu.ActionReceipt `json:"receipt"`
		Status  string           `json:"status"`
	}
	if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Status != "ok" || payload.Receipt.SessionID != testComputerSession ||
		payload.Receipt.ActionID == "" || payload.Receipt.Outcome != cu.OutcomeExecuted ||
		payload.Receipt.DispatchState != cu.DispatchComplete || payload.Receipt.CompletedAt.IsZero() {
		t.Fatalf("stop result lost authoritative control receipt: %s", result.Content)
	}
}

func TestEveryActionUsesTrustedSessionBindingInsteadOfModelSessionID(t *testing.T) {
	for _, action := range []string{"observe", "wait", "stop", "pause"} {
		t.Run(action, func(t *testing.T) {
			service := &coordinatingService{serviceStub: screenshotService(t), sessionID: testComputerSession}
			service.receipt.Outcome = cu.OutcomeExecuted
			input := json.RawMessage(`{"action":"` + action + `","session_id":"invented-by-model","observation_id":"obs-1","duration_ms":1}`)
			_ = New().Run(context.Background(), input, testContext(service))
			if action == "observe" && service.lastObserve.SessionID != testComputerSession {
				t.Fatal("model session used for observe")
			}
			if action == "wait" && service.lastAction.SessionID != testComputerSession {
				t.Fatal("model session used for action")
			}
		})
	}
}

func TestCompleteDispatchWithoutEvidenceRecoversOnlyObservation(t *testing.T) {
	service := screenshotService(t)
	service.receipt = cu.ActionReceipt{Outcome: cu.OutcomeExecuted, DispatchState: cu.DispatchComplete, ErrorCode: cu.ErrorCodeScreenshotFailed}
	tc := testContext(service)
	tc.ComputerUseFastPath = true
	result := New().Run(context.Background(), json.RawMessage(`{"action":"type","session_id":"computer-1","observation_id":"obs-1","text":"test"}`), tc)
	if result.IsError {
		t.Fatal(result.Content)
	}
	assertServiceOrder(t, service, callExecute, callObserve, callImagePrefix+testFreshImageID)
	if strings.Count(strings.Join(service.order, ","), callExecute) != 1 {
		t.Fatal("input was replayed")
	}
}
