package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/config"
)

// stubServerEnv switches the test binary into "be an MCP server" mode.
//
// Re-execing the test binary beats writing shell scripts: it is portable, and
// on macOS a freshly written executable costs ~2s of Gatekeeper scanning on its
// first exec, which every temp-file fixture would pay on every run.
const stubServerEnv = "NIUMA_MCP_STUB_SERVER"

func TestMain(m *testing.M) {
	if mode := os.Getenv(stubServerEnv); mode != "" {
		os.Exit(runStubServer(mode))
	}
	os.Exit(m.Run())
}

// stubServer returns a StdioConfig that launches the test binary in the given
// stub mode.
func stubServer(mode string) StdioConfig {
	return StdioConfig{
		Command: os.Args[0],
		Env:     map[string]string{stubServerEnv: mode},
	}
}

func runStubServer(mode string) int {
	out := bufio.NewWriter(os.Stdout)
	reply := func(id int64, result string) {
		fmt.Fprintf(out, `{"jsonrpc":"2.0","id":%d,"result":%s}`+"\n", id, result)
		_ = out.Flush()
	}
	replyError := func(id int64, code int, message string) {
		fmt.Fprintf(out, `{"jsonrpc":"2.0","id":%d,"error":{"code":%d,"message":%q}}`+"\n", id, code, message)
		_ = out.Flush()
	}

	if mode == "stderr-then-exit" {
		fmt.Fprintln(os.Stderr, "config error: MISSING_TOKEN is not set")
		return 1
	}
	if mode == "report-env" {
		// Report over stderr and fail the handshake: StartStdio surfaces the
		// server's stderr in the returned error, which is the cheapest way to
		// observe the environment the child actually received.
		fmt.Fprintf(os.Stderr, "A=%s G=%s W=%s O=%s P=%s",
			os.Getenv("ANTHROPIC_API_KEY"), os.Getenv("GITHUB_TOKEN"),
			os.Getenv("AWS_SECRET_ACCESS_KEY"), os.Getenv("OPENAI_API_KEY"),
			os.Getenv("GOPATH"))
		return 1
	}
	if mode == "leak-stderr-to-grandchild" {
		// A grandchild inheriting fd 2 keeps the stderr pipe open after we are
		// killed -- exactly what `npx` -> node or a wrapper script does.
		grandchild := exec.Command(os.Args[0])
		grandchild.Env = append(os.Environ(), stubServerEnv+"=sleep-forever")
		grandchild.Stderr = os.Stderr
		grandchild.Stdout = os.Stdout
		if err := grandchild.Start(); err != nil {
			return 1
		}
	}
	if mode == "sleep-forever" {
		time.Sleep(10 * time.Minute)
		return 0
	}

	const okHandshake = `{"protocolVersion":"2024-11-05","capabilities":{"tools":{"listChanged":true}},"serverInfo":{"name":"stub","version":"1"}}`
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var frame struct {
			ID     *int64 `json:"id"`
			Method string `json:"method"`
			Params struct {
				Cursor string `json:"cursor"`
			} `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &frame); err != nil || frame.ID == nil {
			continue // notification or noise
		}
		id := *frame.ID
		switch frame.Method {
		case "initialize":
			switch mode {
			case "with-resources":
				reply(id, `{"protocolVersion":"2024-11-05","capabilities":{"tools":{},"resources":{}},"serverInfo":{"name":"stub","version":"1"}}`)
			default:
				reply(id, okHandshake)
			}
			if mode == "ignore-stdin" {
				// Never read stdin again: closing it must not be enough to stop us.
				time.Sleep(10 * time.Minute)
				return 0
			}
		case "tools/list":
			switch mode {
			case "crash-on-tools-list":
				fmt.Fprintln(os.Stderr, "fatal: index corrupted")
				return 3
			case "tools-list-error":
				replyError(id, -32000, "tools are disabled")
			case "paginated-tools":
				if frame.Params.Cursor == "" {
					reply(id, `{"tools":[{"name":"first"}],"nextCursor":"page2"}`)
				} else {
					reply(id, `{"tools":[{"name":"second"}]}`)
				}
			case "colliding-tools":
				reply(id, `{"tools":[{"name":"get-item"},{"name":"get.item"}]}`)
			default:
				reply(id, `{"tools":[{"name":"echo"}]}`)
			}
		case "resources/list":
			reply(id, `{"resources":[{"uri":"file://a","name":"a"}]}`)
		case methodToolsCall:
			switch mode {
			case "answer-then-exit":
				// The last frame lands and the process exits immediately after:
				// the response must not be lost to the pipe teardown.
				reply(id, `{"content":[{"type":"text","text":"last words"}]}`)
				return 0
			case "flood-one-stderr-line":
				// One line far longer than any scanner cap, then a normal answer.
				fmt.Fprintln(os.Stderr, strings.Repeat("x", 512*1024))
				reply(id, `{"content":[{"type":"text","text":"survived"}]}`)
			case "image-tool":
				reply(id, `{"content":[{"type":"text","text":"here it is"},{"type":"image","mimeType":"image/png","data":"AAAA"}]}`)
			case "image-error-tool":
				reply(id, `{"isError":true,"content":[{"type":"text","text":"capture failed"},{"type":"image","mimeType":"image/png","data":"AAAA"}]}`)
			default:
				reply(id, `{"content":[{"type":"text","text":"ok"}]}`)
			}
		default:
			reply(id, `{}`)
		}
	}
	return 0
}

// startStub boots a stub server and registers cleanup.
func startStub(t *testing.T, name, mode string) *Client {
	t.Helper()
	client, err := StartStdio(t.Context(), name, stubServer(mode))
	if err != nil {
		t.Fatalf("start stub %q: %v", mode, err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// stubServerConfig is the config.MCPServerConfig form of stubServer, for LoadTools.
func stubServerConfig(mode string) config.MCPServerConfig {
	return config.MCPServerConfig{Command: os.Args[0], Env: map[string]string{stubServerEnv: mode}}
}
