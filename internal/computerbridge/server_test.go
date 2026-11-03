package computerbridge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cu "github.com/konglong87/super-niuma/internal/computeruse"
)

type serverSpy struct {
	Service
	calls            int
	owner            cu.SessionOwner
	receipt          cu.ActionReceipt
	err              error
	runtimeActive    bool
	runtimeState     cu.RuntimeState
	runtimeFault     *cu.RuntimeFault
	localSnapshot    SessionSnapshotResponse
	startApproved    bool
	runtimeStopOK    bool
	runtimeStopCalls int
}

func (s *serverSpy) Lookup(_ context.Context, owner cu.SessionOwner) (string, error) {
	s.calls++
	s.owner = owner
	return "approved-session", s.err
}
func (s *serverSpy) Execute(_ context.Context, owner cu.SessionOwner, a cu.Action) (cu.ActionReceipt, error) {
	s.calls++
	s.owner = owner
	return s.receipt, s.err
}
func serverCall(h http.Handler, body, token, origin string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "http://localhost"+CommandPath, strings.NewReader(body))
	r.Header.Set(AuthorizationHeader, BearerPrefix+token)
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

const serverTestToken = "test-server-token-not-a-real-credential"
const serverLookupJSON = `{"op":"lookup","owner":{"tenant_id":7,"user_id":11,"session_id":13}}`

func TestHostHandlerAuthenticationAndStrictBoundary(t *testing.T) {
	s := &serverSpy{}
	h, err := NewHandler(serverTestToken, s)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		body, token, origin string
		status              int
	}{
		{serverLookupJSON, "wrong", "", http.StatusUnauthorized},
		{serverLookupJSON, serverTestToken, "null", http.StatusUnauthorized},
		{serverLookupJSON, serverTestToken, "http://localhost", http.StatusUnauthorized},
		{`{"op":"start","approved":true}`, serverTestToken, "", http.StatusBadRequest},
		{`{"op":"lookup","owner":{"tenant_id":7,"user_id":11}}`, serverTestToken, "", http.StatusBadRequest},
		{serverLookupJSON + ` {}`, serverTestToken, "", http.StatusBadRequest},
		{strings.Repeat("x", MaxRequestBytes+1), serverTestToken, "", http.StatusBadRequest},
	} {
		r := serverCall(h, tt.body, tt.token, tt.origin)
		if r.Code != tt.status {
			t.Fatalf("code %d want %d", r.Code, tt.status)
		}
	}
	if s.calls != 0 {
		t.Fatal("bad request reached authority")
	}
	good := serverCall(h, serverLookupJSON, serverTestToken, "")
	if good.Code != 200 || s.calls != 1 || s.owner.SessionID != 13 {
		t.Fatal(good.Code, s)
	}
	if !strings.Contains(good.Body.String(), "approved-session") {
		t.Fatal(good.Body.String())
	}
}
func TestHostHandlerSanitizesBackendErrorsAndAmbiguousExecution(t *testing.T) {
	s := &serverSpy{err: errors.New("SECRET")}
	h, _ := NewHandler(serverTestToken, s)
	response := serverCall(h, serverLookupJSON, serverTestToken, "")
	if strings.Contains(response.Body.String(), "SECRET") || strings.Contains(response.Body.String(), "approved-session") {
		t.Fatal("leaked failed lookup")
	}
	action := cu.Action{ID: "a", SessionID: "s", ObservationID: "o", Kind: cu.ActionWait, DurationMS: 1}
	body, _ := json.Marshal(Request{Op: OpExecute, Owner: cu.SessionOwner{TenantID: 7, UserID: 11, SessionID: 13}, SessionID: "s", Action: &action})
	s.receipt = cu.ActionReceipt{ActionID: "wrong", Outcome: cu.OutcomeExecuted}
	response = serverCall(h, string(body), serverTestToken, "")
	var out Response
	if json.Unmarshal(response.Body.Bytes(), &out) != nil {
		t.Fatal(response.Body.String())
	}
	var r cu.ActionReceipt
	if json.Unmarshal(out.Data, &r) != nil || r.Outcome != cu.OutcomeUnknown || r.ActionID != "a" || r.SessionID != "s" || out.Error == "" {
		t.Fatal(string(out.Data), out.Error)
	}
	s.receipt = cu.ActionReceipt{ActionID: "a", SessionID: "s", Outcome: cu.OutcomeRejected, Verification: cu.VerificationNotChecked, ErrorMessage: "SECRET", ErrorCode: "SECRET", RedactedActionSummary: "SECRET"}
	response = serverCall(h, string(body), serverTestToken, "")
	if strings.Contains(response.Body.String(), "SECRET") {
		t.Fatal("leaked error/summary")
	}
	if !strings.Contains(response.Body.String(), "rejected") {
		t.Fatal(response.Body.String())
	}
}
func TestHostHandlerDisallowsOtherMethodsHostsAndPaths(t *testing.T) {
	s := &serverSpy{}
	h, _ := NewHandler(serverTestToken, s)
	for _, target := range []string{"http://elsewhere/command", "http://localhost/command?token=x", "http://localhost/unknown"} {
		req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(serverLookupJSON))
		req.Header.Set(AuthorizationHeader, BearerPrefix+serverTestToken)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code == 200 {
			t.Fatal("accepted", target)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "http://localhost/command", nil)
	req.Header.Set(AuthorizationHeader, BearerPrefix+serverTestToken)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatal(w.Code)
	}
	if s.calls != 0 {
		t.Fatal("reached service")
	}
}

type codedServerError struct{ code string }

func (e codedServerError) Error() string { return "secret host detail" }
func (e codedServerError) Code() string  { return e.code }

func TestHostHandlerPreservesSanitizedControlledErrorCode(t *testing.T) {
	s := &serverSpy{err: codedServerError{code: cu.ErrorCodeFocusChanged}}
	h, err := NewHandler(serverTestToken, s)
	if err != nil {
		t.Fatal(err)
	}
	response := serverCall(h, serverLookupJSON, serverTestToken, "")
	var out Response
	if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.ErrorCode != cu.ErrorCodeFocusChanged || strings.Contains(response.Body.String(), "secret host detail") {
		t.Fatalf("response=%s", response.Body.String())
	}
}

func (s *serverSpy) RuntimeStatus(_ context.Context, owner cu.SessionOwner) (RuntimeStatusResponse, error) {
	s.calls++
	s.owner = owner
	return RuntimeStatusResponse{ActiveSession: s.runtimeActive, State: s.runtimeState, Fault: s.runtimeFault}, nil
}

func testSessionSnapshot() SessionSnapshotResponse {
	return SessionSnapshotResponse{SessionID: "local-session", State: cu.SessionReady,
		Owner: cu.SessionOwner{TenantID: 1, UserID: 1}, Approved: true, Capabilities: testCapabilities()}
}

func (s *serverSpy) StartLocal(_ context.Context, _ cu.SessionOwner, approved bool) (SessionSnapshotResponse, error) {
	s.calls++
	s.startApproved = approved
	return s.localSnapshot, s.err
}

func (s *serverSpy) Snapshot(_ context.Context, _ cu.SessionOwner, _ string) (SessionSnapshotResponse, error) {
	s.calls++
	return s.localSnapshot, s.err
}

func (s *serverSpy) StopRuntime(_ context.Context, owner cu.SessionOwner) error {
	s.runtimeStopCalls++
	s.owner = owner
	if !s.runtimeStopOK {
		return errors.New("runtime stop denied")
	}
	return nil
}
