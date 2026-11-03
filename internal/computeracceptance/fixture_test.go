package computeracceptance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func testFixture(t *testing.T) *Fixture {
	t.Helper()
	fixture, err := StartFixture()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := fixture.Close(); err != nil {
			t.Error(err)
		}
	})
	return fixture
}

func sampleReport() Report {
	return Report{
		Events: []DOMEvent{{Type: Click, Target: ClickTarget, IsTrusted: true, Position: true,
			Client: Point{X: 50, Y: 50}, Screen: Point{X: 80, Y: 140}}},
		Geometry: &Geometry{
			InnerWidth: 1024, InnerHeight: 768, OuterWidth: 1040, OuterHeight: 850, DevicePixelRatio: 2,
			VisualViewport: &VisualViewport{Width: 1024, Height: 768, Scale: 1},
			Calibration:    &Calibration{Client: Point{X: 50, Y: 50}, Screen: Point{X: 80, Y: 140}},
			Targets:        []TargetGeometry{{ID: ClickTarget, Rect: Rect{Width: 100, Height: 100}, Center: Point{X: 50, Y: 50}}},
		},
		State: &PageState{},
	}
}

func reportJSON(t *testing.T, report Report) []byte {
	t.Helper()
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func apiRequest(fixture *Fixture, method, path string, body []byte) *http.Request {
	request := httptest.NewRequest(method, "http://"+fixture.host+path, bytes.NewReader(body))
	request.RemoteAddr = "127.0.0.1:49100"
	request.Header.Set(TokenHeader, fixture.token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://"+fixture.host)
	return request
}

func perform(fixture *Fixture, request *http.Request) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	fixture.serveHTTP(response, request)
	return response
}

func assertStatus(t *testing.T, response *httptest.ResponseRecorder, want int) {
	t.Helper()
	if response.Code != want {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, want, response.Body)
	}
}

func TestAuthenticationAndRequestGuards(t *testing.T) {
	fixture := testFixture(t)
	other := testFixture(t)
	body := reportJSON(t, sampleReport())
	cases := []struct {
		name   string
		modify func(*http.Request)
		status int
	}{
		{"missing token", func(r *http.Request) { r.Header.Del(TokenHeader) }, http.StatusForbidden},
		{"wrong token", func(r *http.Request) { r.Header.Set(TokenHeader, "wrong") }, http.StatusForbidden},
		{"other instance token", func(r *http.Request) { r.Header.Set(TokenHeader, other.token) }, http.StatusForbidden},
		{"query token denied", func(r *http.Request) {
			r.Header.Del(TokenHeader)
			r.URL.RawQuery = "token=" + fixture.token
		}, http.StatusForbidden},
		{"cross origin", func(r *http.Request) { r.Header.Set("Origin", "https://example.org") }, http.StatusForbidden},
		{"null origin", func(r *http.Request) { r.Header.Set("Origin", "null") }, http.StatusForbidden},
		{"DNS rebinding host", func(r *http.Request) { r.Host = "example.org" }, http.StatusForbidden},
		{"non-loopback peer", func(r *http.Request) { r.RemoteAddr = "192.0.2.1:4000" }, http.StatusForbidden},
		{"invalid peer", func(r *http.Request) { r.RemoteAddr = "bad-address" }, http.StatusForbidden},
		{"wrong method", func(r *http.Request) { r.Method = http.MethodGet }, http.StatusMethodNotAllowed},
		{"preflight denied", func(r *http.Request) { r.Method = http.MethodOptions }, http.StatusMethodNotAllowed},
		{"missing content type", func(r *http.Request) { r.Header.Del("Content-Type") }, http.StatusUnsupportedMediaType},
		{"wrong content type", func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, http.StatusUnsupportedMediaType},
		{"encoded body", func(r *http.Request) { r.Header.Set("Content-Encoding", "gzip") }, http.StatusUnsupportedMediaType},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			request := apiRequest(fixture, http.MethodPost, "/events", body)
			test.modify(request)
			response := perform(fixture, request)
			assertStatus(t, response, test.status)
			if response.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("fixture must not enable CORS")
			}
		})
	}
	if fixture.Snapshot().Reports != 0 {
		t.Fatal("denied requests mutated state")
	}
	for _, path := range []string{"/snapshot", "/config"} {
		request := apiRequest(fixture, http.MethodGet, path, nil)
		request.Header.Del(TokenHeader)
		assertStatus(t, perform(fixture, request), http.StatusForbidden)
	}
}

func TestJSONValidation(t *testing.T) {
	fixture := testFixture(t)
	valid := string(reportJSON(t, sampleReport()))
	cases := map[string]string{
		"empty": "", "null": "null", "array": "[]", "missing required": "{}",
		"malformed": "{", "extra object": valid + "{}", "trailing garbage": valid + "x",
		"unknown field":            strings.Replace(valid, `"events":`, `"secret":1,"events":`, 1),
		"unknown nested field":     strings.Replace(valid, `"isTrusted":true`, `"isTrusted":true,"unexpected":1`, 1),
		"wrong type":               strings.Replace(valid, `"isTrusted":true`, `"isTrusted":"true"`, 1),
		"server sequence injected": strings.Replace(valid, `"isTrusted":true`, `"isTrusted":true,"sequence":500`, 1),
		"invalid UTF-8":            valid[:len(valid)-1] + string([]byte{0xff}) + "}",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			assertStatus(t, perform(fixture, apiRequest(fixture, http.MethodPost, "/events", []byte(body))), http.StatusBadRequest)
		})
	}
	if fixture.Snapshot().Reports != 0 {
		t.Fatal("invalid JSON mutated state")
	}
}

func TestReportValidationAndBounds(t *testing.T) {
	fixture := testFixture(t)
	cases := map[string]func(*Report){
		"too many events":         func(r *Report) { r.Events = make([]DOMEvent, MaxBatchEvents+1) },
		"missing geometry":        func(r *Report) { r.Geometry = nil },
		"missing state":           func(r *Report) { r.State = nil },
		"unknown event":           func(r *Report) { r.Events[0].Type = "execute" },
		"unknown target":          func(r *Report) { r.Events[0].Target = "password" },
		"long value":              func(r *Report) { r.Events[0].Value = strings.Repeat("x", MaxTextBytes+1) },
		"long data":               func(r *Report) { r.Events[0].Data = strings.Repeat("🙂", MaxTextBytes/4+1) },
		"long key":                func(r *Report) { r.Events[0].Key = strings.Repeat("k", maxLabelBytes+1) },
		"negative timestamp":      func(r *Report) { r.Events[0].TimeStamp = -1 },
		"large coordinate":        func(r *Report) { r.Events[0].Screen.X = maxCoordinate + 1 },
		"invalid button":          func(r *Report) { r.Events[0].Button = 6 },
		"invalid delta mode":      func(r *Report) { r.Events[0].DeltaMode = 3 },
		"long input state":        func(r *Report) { r.State.InputValue = strings.Repeat("x", MaxTextBytes+1) },
		"invalid selection":       func(r *Report) { r.State.SelectionEnd = -1 },
		"reversed selection":      func(r *Report) { r.State.SelectionStart = 1 },
		"negative rectangle":      func(r *Report) { r.Geometry.Targets[0].Rect.Width = -1 },
		"zero viewport":           func(r *Report) { r.Geometry.InnerWidth = 0 },
		"zero DPR":                func(r *Report) { r.Geometry.DevicePixelRatio = 0 },
		"invalid visual viewport": func(r *Report) { r.Geometry.VisualViewport.Scale = 0 },
		"invalid calibration":     func(r *Report) { r.Geometry.Calibration.TimeStamp = -1 },
		"no targets":              func(r *Report) { r.Geometry.Targets = nil },
		"unknown geometry target": func(r *Report) { r.Geometry.Targets[0].ID = "unknown" },
		"duplicate targets":       func(r *Report) { r.Geometry.Targets = append(r.Geometry.Targets, r.Geometry.Targets[0]) },
		"too many targets":        func(r *Report) { r.Geometry.Targets = make([]TargetGeometry, targetCount+1) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			report := sampleReport()
			mutate(&report)
			assertStatus(t, perform(fixture, apiRequest(fixture, http.MethodPost, "/events", reportJSON(t, report))), http.StatusBadRequest)
		})
	}
	if fixture.Snapshot().Reports != 0 {
		t.Fatal("invalid reports mutated state")
	}
}

func TestRequestByteLimit(t *testing.T) {
	fixture := testFixture(t)
	body := reportJSON(t, sampleReport())
	padded := append(body, bytes.Repeat([]byte(" "), MaxRequestBytes-len(body))...)
	assertStatus(t, perform(fixture, apiRequest(fixture, http.MethodPost, "/events", padded)), http.StatusNoContent)
	for _, length := range []int64{-1, int64(MaxRequestBytes + 1)} {
		request := apiRequest(fixture, http.MethodPost, "/events", append(padded, ' '))
		request.ContentLength = length // Check streamed/chunked bodies, not just the header.
		assertStatus(t, perform(fixture, request), http.StatusRequestEntityTooLarge)
	}
	if fixture.Snapshot().Reports != 1 {
		t.Fatal("oversize requests mutated state")
	}
}

func TestRecordingUnicodeTrustAndSnapshotIsolation(t *testing.T) {
	fixture := testFixture(t)
	report := sampleReport()
	report.Events = []DOMEvent{
		{Type: Input, Target: TextTarget, IsTrusted: true, Value: "中文🙂", Data: "🙂", InputType: "insertText"},
		{Type: KeyDown, Target: TextTarget, IsTrusted: false, Key: "a", Code: "KeyA", Modifiers: Modifiers{Meta: true}},
	}
	report.State = &PageState{InputValue: "中文🙂", SelectionStart: 4, SelectionEnd: 4, ScrollTop: 100}
	report.ClientDropped = 3
	assertStatus(t, perform(fixture, apiRequest(fixture, http.MethodPost, "/events", reportJSON(t, report))), http.StatusNoContent)
	snapshot := fixture.Snapshot()
	if snapshot.TotalEvents != 2 || snapshot.Reports != 1 || snapshot.ClientDropped != 3 || snapshot.UpdatedAt.IsZero() {
		t.Fatalf("unexpected counters: %+v", snapshot)
	}
	if snapshot.Events[0].Value != "中文🙂" || !snapshot.Events[0].IsTrusted || snapshot.Events[1].IsTrusted {
		t.Fatalf("text/trust changed: %+v", snapshot.Events)
	}
	if snapshot.State != *report.State || snapshot.Events[1].Sequence != 2 || snapshot.Events[0].ReceivedAt.IsZero() {
		t.Fatalf("state or metadata changed: %+v", snapshot)
	}
	snapshot.Events[0].Value = "mutated"
	snapshot.Geometry.Targets[0].ID = "mutated"
	snapshot.Geometry.VisualViewport.Scale = 500
	snapshot.Geometry.Calibration.Screen.X = 999
	fresh := fixture.Snapshot()
	if fresh.Events[0].Value != "中文🙂" || fresh.Geometry.Targets[0].ID != ClickTarget ||
		fresh.Geometry.VisualViewport.Scale != 1 || fresh.Geometry.Calibration.Screen.X != 80 {
		t.Fatal("Snapshot leaked mutable storage")
	}
	events := fixture.Events()
	events[0].Value = "mutated"
	if fixture.Events()[0].Value != "中文🙂" {
		t.Fatal("Events leaked mutable storage")
	}
	response := perform(fixture, apiRequest(fixture, http.MethodGet, "/snapshot", nil))
	assertStatus(t, response, http.StatusOK)
	var wire Snapshot
	if err := json.Unmarshal(response.Body.Bytes(), &wire); err != nil || wire.TotalEvents != 2 {
		t.Fatalf("invalid snapshot response: %v", err)
	}
}

func TestRingRetentionAndHeartbeat(t *testing.T) {
	fixture := testFixture(t)
	const total = MaxEvents + 3
	for i := 0; i < total; i++ {
		report := sampleReport()
		report.Events[0].TimeStamp = float64(i)
		assertStatus(t, perform(fixture, apiRequest(fixture, http.MethodPost, "/events", reportJSON(t, report))), http.StatusNoContent)
	}
	heartbeat := sampleReport()
	heartbeat.Events = nil
	heartbeat.Geometry.WindowScreen.X = -100 // Secondary displays may have negative coordinates.
	heartbeat.Geometry.Calibration = nil
	assertStatus(t, perform(fixture, apiRequest(fixture, http.MethodPost, "/events", reportJSON(t, heartbeat))), http.StatusNoContent)
	snapshot := fixture.Snapshot()
	if len(snapshot.Events) != MaxEvents || snapshot.TotalEvents != total || snapshot.Dropped != 3 || snapshot.Reports != total+1 {
		t.Fatalf("bad bounded storage: events=%d total=%d dropped=%d reports=%d", len(snapshot.Events), snapshot.TotalEvents, snapshot.Dropped, snapshot.Reports)
	}
	for i, event := range snapshot.Events {
		if event.Sequence != uint64(i+4) || event.TimeStamp != float64(i+3) {
			t.Fatalf("ring order corrupted at %d: %+v", i, event)
		}
	}
	if snapshot.Geometry.WindowScreen.X != -100 || snapshot.Geometry.Calibration != nil {
		t.Fatal("heartbeat did not update geometry")
	}
}

func TestConcurrentReportsAndReaders(t *testing.T) {
	fixture := testFixture(t)
	body := reportJSON(t, sampleReport())
	const workers, iterations = 8, 25
	var group sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		group.Go(func() {
			for i := 0; i < iterations; i++ {
				response := perform(fixture, apiRequest(fixture, http.MethodPost, "/events", body))
				if response.Code != http.StatusNoContent {
					t.Errorf("unexpected status: %d", response.Code)
				}
				_ = fixture.Snapshot()
				_ = fixture.Events()
			}
		})
	}
	group.Wait()
	if got := fixture.Snapshot().TotalEvents; got != workers*iterations {
		t.Fatalf("lost concurrent events: %d", got)
	}
}

func TestAssetsAndConfig(t *testing.T) {
	fixture := testFixture(t)
	for path, contentType := range map[string]string{"/": "text/html", "/fixture.css": "text/css", "/fixture.js": "text/javascript"} {
		request := apiRequest(fixture, http.MethodGet, path, nil)
		request.Header.Del(TokenHeader) // Assets are public; never contain the token.
		response := perform(fixture, request)
		assertStatus(t, response, http.StatusOK)
		if !strings.HasPrefix(response.Header().Get("Content-Type"), contentType) || response.Body.Len() == 0 {
			t.Fatalf("bad embedded asset %s", path)
		}
		if strings.Contains(response.Body.String(), fixture.token) {
			t.Fatal("capability leaked into public asset")
		}
		if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Content-Security-Policy") == "" {
			t.Fatal("missing security headers")
		}
	}
	assertStatus(t, perform(fixture, apiRequest(fixture, http.MethodGet, "/not-found", nil)), http.StatusNotFound)
	assertStatus(t, perform(fixture, apiRequest(fixture, http.MethodPost, "/", nil)), http.StatusMethodNotAllowed)
	assertStatus(t, perform(fixture, apiRequest(fixture, http.MethodPost, "/snapshot", nil)), http.StatusMethodNotAllowed)
	response := perform(fixture, apiRequest(fixture, http.MethodGet, "/config", nil))
	assertStatus(t, response, http.StatusOK)
	var config map[string]int
	if err := json.Unmarshal(response.Body.Bytes(), &config); err != nil {
		t.Fatal(err)
	}
	if config["maxBatchEvents"] != MaxBatchEvents || config["maxQueueEvents"] != MaxEvents || config["maxRequestBytes"] != MaxRequestBytes {
		t.Fatalf("browser bounds out of sync: %v", config)
	}
}

func TestListenerAndCleanup(t *testing.T) {
	fixture := testFixture(t)
	pageURL, err := url.Parse(fixture.URL())
	if err != nil {
		t.Fatal(err)
	}
	if pageURL.Hostname() != "127.0.0.1" || pageURL.Port() == "0" || len(pageURL.Fragment) != tokenBytes*2 || pageURL.RawQuery != "" {
		t.Fatal("URL must use ephemeral IPv4 loopback and a fragment capability")
	}
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	response, err := client.Get(fixture.URL())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatal(response.Status)
	}
	// Keep an incomplete HTTP request active; Close must not wait for its timeout.
	connection, err := net.DialTimeout("tcp4", pageURL.Host, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_, _ = fmt.Fprintf(connection, "POST /events HTTP/1.1\r\nHost: %s\r\n", pageURL.Host)
	var group sync.WaitGroup
	for range 3 {
		group.Go(func() {
			if err := fixture.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	select {
	case <-fixture.done:
	default:
		t.Fatal("server goroutine still running")
	}
	_ = connection.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := connection.Read(make([]byte, 1)); err == nil {
		t.Fatal("active connection survived Close")
	} else if netError, ok := err.(net.Error); ok && netError.Timeout() {
		t.Fatal("active connection was not closed")
	}
	if response, err := client.Get(fixture.URL()); err == nil {
		_ = response.Body.Close()
		t.Fatal("listener still accepting after Close")
	}
}

func TestAllEventTypesAndTargetsMatchAssets(t *testing.T) {
	fixture := testFixture(t)
	page, err := assets.ReadFile("assets/index.html")
	if err != nil {
		t.Fatal(err)
	}
	script, err := assets.ReadFile("assets/fixture.js")
	if err != nil {
		t.Fatal(err)
	}
	targets := []TargetID{ClickTarget, DoubleClickTarget, ContextMenuTarget, MouseMoveTarget, ScrollTarget, TextTarget, DragSourceTarget, DragDropTarget}
	for _, target := range targets {
		if !strings.Contains(string(page), `id="`+string(target)+`" data-target`) {
			t.Fatalf("missing embedded target %q", target)
		}
	}
	types := []EventType{Click, DoubleClick, ContextMenu, MouseMove, MouseDown, MouseUp, Wheel, Scroll,
		KeyDown, KeyUp, BeforeInput, Input, CompositionStart, CompositionUpdate, CompositionEnd, Focus, Blur}
	for _, kind := range types {
		if !strings.Contains(string(script), `"`+string(kind)+`"`) {
			t.Fatalf("missing embedded event listener %q", kind)
		}
		report := sampleReport()
		report.Events = make([]DOMEvent, MaxBatchEvents)
		for i := range report.Events {
			report.Events[i] = DOMEvent{Type: kind, Target: targets[i%len(targets)], IsTrusted: i%2 == 0}
		}
		assertStatus(t, perform(fixture, apiRequest(fixture, http.MethodPost, "/events", reportJSON(t, report))), http.StatusNoContent)
	}
	for _, forbidden := range []string{"dispatchEvent(", ".click(", ".focus(", "new MouseEvent(", "new KeyboardEvent("} {
		if strings.Contains(string(script), forbidden) {
			t.Fatalf("fixture must not synthesize input: %s", forbidden)
		}
	}
}

func TestTokenFragmentScrubbedBeforeBootstrap(t *testing.T) {
	script, err := assets.ReadFile("assets/fixture.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(script)
	capture := strings.Index(source, `const token = location.hash.slice(1);`)
	scrub := strings.Index(source, `history.replaceState(history.state, "", location.pathname + location.search);`)
	bootstrap := strings.Index(source, `limits = await request("/config");`)
	if capture < 0 || scrub <= capture || bootstrap <= scrub {
		t.Fatal("capture the token, scrub the current history entry without reloading, then authenticate")
	}
}
