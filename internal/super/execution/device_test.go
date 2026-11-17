package execution

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/konglong87/super-niuma/internal/config"
)

type fakeDevice struct {
	mu      sync.Mutex
	actions []DeviceAction
	respond func(DeviceAction) (string, error)
}

func (d *fakeDevice) Act(_ context.Context, a DeviceAction) (string, error) {
	d.mu.Lock()
	d.actions = append(d.actions, a)
	d.mu.Unlock()
	if d.respond != nil {
		return d.respond(a)
	}
	return "app: com.example.clock\n[1] Button \"Add alarm\" @540,2000 click", nil
}

// scriptedChat answers each chat completion with the next scripted message
// and records every request body.
func scriptedChat(t *testing.T, script ...string) (*httptest.Server, *[]map[string]any) {
	t.Helper()
	var mu sync.Mutex
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Error(err)
		}
		mu.Lock()
		n := len(bodies)
		bodies = append(bodies, body)
		mu.Unlock()
		if n >= len(script) {
			t.Errorf("unexpected request %d", n+1)
			http.Error(w, "no more script", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, script[n])
	}))
	t.Cleanup(server.Close)
	return server, &bodies
}

func toolCall(id, name, args string) string {
	data, _ := json.Marshal(args)
	return `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"` + id + `","type":"function","function":{"name":"` + name + `","arguments":` + string(data) + `}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":20,"completion_tokens":5}}`
}

const finalText = `{"choices":[{"message":{"role":"assistant","content":"Alarm set."},"finish_reason":"stop"}],"usage":{"prompt_tokens":30,"completion_tokens":3}}`

func deviceRequest(t *testing.T, device Device, authority func(context.Context) error) Request {
	t.Helper()
	req := fixtureRequest(t)
	req.ReadPaths, req.WritePaths = nil, nil
	req.Device = device
	req.MaxTurns = MaxDeviceTurns
	if authority != nil {
		req.CheckAuthority = authority
	}
	return req
}

func deviceRunner(server *httptest.Server) *Runner {
	cfg := fixtureConfig()
	cfg.Provider = "openai"
	cfg.ProviderProtocol = config.ProviderProtocolOpenAIChatCompletions
	cfg.BaseURL = server.URL + "/v1"
	return New(func(context.Context) (config.Config, error) { return cfg, nil }, nil)
}

func toolNames(body map[string]any) []string {
	var names []string
	list, _ := body["tools"].([]any)
	for _, item := range list {
		fn, _ := item.(map[string]any)["function"].(map[string]any)
		if name, ok := fn["name"].(string); ok {
			names = append(names, name)
		}
	}
	return names
}

func lastToolResult(body map[string]any) string {
	messages, _ := body["messages"].([]any)
	for i := len(messages) - 1; i >= 0; i-- {
		m, _ := messages[i].(map[string]any)
		if m["role"] == "tool" {
			s, _ := m["content"].(string)
			return s
		}
	}
	return ""
}

func TestDeviceActionsRouteThroughAuthority(t *testing.T) {
	server, bodies := scriptedChat(t,
		toolCall("call-1", ToolLaunch, `{"app":"com.example.clock","intent":"Open the clock"}`),
		toolCall("call-2", ToolTap, `{"ref":1,"intent":"Add an alarm"}`),
		finalText,
	)
	device := &fakeDevice{}
	var checks int
	req := deviceRequest(t, device, func(context.Context) error { checks++; return nil })
	got, err := deviceRunner(server).Execute(context.Background(), req)
	if err != nil || got.Text != "Alarm set." || got.ToolCalls != 2 {
		t.Fatalf("result=%+v err=%v", got, err)
	}
	if len(device.actions) != 2 || device.actions[0] != (DeviceAction{Kind: "launch", App: "com.example.clock", Intent: "Open the clock"}) ||
		device.actions[1] != (DeviceAction{Kind: "tap", Ref: 1, Intent: "Add an alarm"}) {
		t.Fatalf("actions %+v", device.actions)
	}
	if checks < 2+3 {
		t.Fatalf("authority checked %d times, want before each action and request", checks)
	}
	names := strings.Join(toolNames((*bodies)[0]), ",")
	for _, want := range []string{ToolScreen, ToolTap, ToolType, ToolSwipe, ToolKey, ToolLaunch} {
		if !strings.Contains(names, want) {
			t.Fatalf("tool %s not offered: %s", want, names)
		}
	}
	if strings.Contains(names, ToolRead) || strings.Contains(names, ToolWrite) {
		t.Fatalf("file tools offered without paths: %s", names)
	}
	if !strings.Contains(lastToolResult((*bodies)[2]), "Add alarm") {
		t.Fatal("screen after the action not returned to the model")
	}
}

func TestDeviceToolsAbsentWithoutDevice(t *testing.T) {
	server, bodies := scriptedChat(t, finalText)
	req := fixtureRequest(t)
	if _, err := deviceRunner(server).Execute(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	for _, name := range toolNames((*bodies)[0]) {
		if deviceTool(name) {
			t.Fatalf("phone tool %s offered without a device", name)
		}
	}
}

func TestDeviceRefusalReachesModelAndAttemptContinues(t *testing.T) {
	server, bodies := scriptedChat(t,
		toolCall("call-1", ToolTap, `{"ref":7,"intent":"Send the message"}`),
		finalText,
	)
	device := &fakeDevice{respond: func(DeviceAction) (string, error) {
		return "", DeviceRefusal{Reason: "approval_denied: the person said no"}
	}}
	got, err := deviceRunner(server).Execute(context.Background(), deviceRequest(t, device, nil))
	if err != nil || got.Text != "Alarm set." {
		t.Fatalf("result=%+v err=%v", got, err)
	}
	if !strings.Contains(lastToolResult((*bodies)[1]), "approval_denied") {
		t.Fatal("refusal not shown to the model")
	}
}

func TestDeviceStopEndsAttemptAsRevokedAuthority(t *testing.T) {
	server, bodies := scriptedChat(t,
		toolCall("call-1", ToolTap, `{"ref":1,"intent":"x"}`),
		finalText,
	)
	device := &fakeDevice{respond: func(DeviceAction) (string, error) { return "", ErrDeviceStopped }}
	_, err := deviceRunner(server).Execute(context.Background(), deviceRequest(t, device, nil))
	if !errors.Is(err, ErrAuthority) || len(*bodies) != 1 {
		t.Fatalf("stop did not end the attempt: err=%v requests=%d", err, len(*bodies))
	}
}

func TestRevokedAuthorityRunsNoDeviceAction(t *testing.T) {
	server, _ := scriptedChat(t, toolCall("call-1", ToolTap, `{"ref":1,"intent":"x"}`), finalText)
	device := &fakeDevice{}
	calls := 0
	authority := func(context.Context) error {
		calls++
		if calls > 2 { // the first model request passes, the tool check fails
			return errors.New("revoked")
		}
		return nil
	}
	_, err := deviceRunner(server).Execute(context.Background(), deviceRequest(t, device, authority))
	if !errors.Is(err, ErrAuthority) || len(device.actions) != 0 {
		t.Fatalf("action ran after revocation: err=%v actions=%d", err, len(device.actions))
	}
}

func TestMalformedDeviceArgumentsAreReportedNotRun(t *testing.T) {
	cases := []struct{ name, args string }{
		{ToolTap, `{"intent":"no ref"}`},
		{ToolTap, `{"ref":-1,"intent":"x"}`},
		{ToolTap, `{"ref":"3","intent":"x"}`},
		{ToolSwipe, `{"direction":"diagonal","intent":"x"}`},
		{ToolKey, `{"key":"power","intent":"x"}`},
		{ToolLaunch, `{"app":"","intent":"x"}`},
		{ToolScreen, `{"ref":1}`},
		{ToolTap, `{"ref":1,"intent":"x","extra":true}`},
	}
	for _, tc := range cases {
		t.Run(tc.name+tc.args, func(t *testing.T) {
			server, bodies := scriptedChat(t, toolCall("call-1", tc.name, tc.args), finalText)
			device := &fakeDevice{}
			got, err := deviceRunner(server).Execute(context.Background(), deviceRequest(t, device, nil))
			if err != nil || got.Text != "Alarm set." || len(device.actions) != 0 {
				t.Fatalf("err=%v actions=%d", err, len(device.actions))
			}
			if !strings.Contains(lastToolResult((*bodies)[1]), "invalid arguments") {
				t.Fatal("model not told the arguments were invalid")
			}
		})
	}
}

func TestOversizedDeviceResultFailsAttempt(t *testing.T) {
	server, _ := scriptedChat(t, toolCall("call-1", ToolScreen, `{}`), finalText)
	device := &fakeDevice{respond: func(DeviceAction) (string, error) {
		return strings.Repeat("x", MaxDeviceResultBytes+1), nil
	}}
	if _, err := deviceRunner(server).Execute(context.Background(), deviceRequest(t, device, nil)); !errors.Is(err, ErrFailed) {
		t.Fatalf("oversized screen accepted: %v", err)
	}
}

func TestDeviceTurnLimit(t *testing.T) {
	req := fixtureRequest(t)
	req.MaxTurns = MaxTurns + 1
	if _, _, err := validateRequest(req); !errors.Is(err, ErrInvalid) {
		t.Fatal("file-only attempt allowed more than MaxTurns")
	}
	req.Device = &fakeDevice{}
	if _, _, err := validateRequest(req); err != nil {
		t.Fatalf("phone attempt refused %d turns: %v", req.MaxTurns, err)
	}
	req.MaxTurns = MaxDeviceTurns + 1
	if _, _, err := validateRequest(req); !errors.Is(err, ErrInvalid) {
		t.Fatal("phone attempt allowed more than MaxDeviceTurns")
	}
}
