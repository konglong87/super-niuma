package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// sessionIDHeader / protocolVersionHeader 是 streamable-http 的两个必备回执。
// 合规的 server 在 initialize 的响应头里发一个会话 id，之后每个请求都必须带回去，
// 否则第二次调用就会被拒。旧实现两个都不管。
const (
	sessionIDHeader       = "Mcp-Session-Id"
	protocolVersionHeader = "MCP-Protocol-Version"
)

type httpRPC struct {
	serverName string
	url        string
	headers    map[string]string
	client     *http.Client
	timeouts   mcpTimeoutBudget

	mu        sync.Mutex
	nextID    int64
	sessionID string
	// negotiated 是握手后实际生效的协议版本；空表示还没握手。
	negotiated string
}

func newHTTPRPC(serverName, url string, headers map[string]string, timeouts mcpTimeoutBudget) *httpRPC {
	return &httpRPC{
		serverName: serverName,
		url:        url,
		headers:    headers,
		timeouts:   timeouts,
		// 刻意不设 http.Client.Timeout：那是一个覆盖读完整个 body 的硬 deadline，
		// 会把一次几分钟的合法 tools/call 砍断。超时改由每次调用按 budgetFor 的
		// 分段预算用 context 施加。
		client: &http.Client{},
		nextID: 1,
	}
}

func (h *httpRPC) call(ctx context.Context, method string, params any, out any) error {
	return h.callWithCallback(ctx, method, params, out, nil)
}

func (h *httpRPC) callWithCallback(ctx context.Context, method string, params any, out any, _ serverCallbackHandler) error {
	h.mu.Lock()
	id := h.nextID
	h.nextID++
	h.mu.Unlock()
	req := rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	return h.do(ctx, req, id, method, out)
}

func (h *httpRPC) notify(ctx context.Context, method string, params any) error {
	req := rpcRequest{JSONRPC: "2.0", Method: method, Params: params}
	return h.do(ctx, req, 0, method, nil)
}

func (h *httpRPC) do(ctx context.Context, rpcReq rpcRequest, id int64, method string, out any) error {
	body, err := json.Marshal(rpcReq)
	if err != nil {
		return err
	}
	budget := budgetFor(h.timeouts, method)
	if budget.limit > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, budget.limit)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "application/json, text/event-stream")
	for k, v := range h.headers {
		req.Header.Set(k, v)
	}
	h.applySession(req)

	resp, err := h.client.Do(req)
	if err != nil {
		// 只有我们自己加的那个 deadline 触发时才算超时。用户按 ESC 时 ctx.Err()
		// 同样非 nil，但那是 context.Canceled —— 一律当超时报会让上层的
		// errors.Is(err, context.Canceled) 失效，用户取消和网关卡死就分不开了。
		// （client.Do 返回的 *url.Error 本来就包着 context.Canceled，直接透传即可。）
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("mcp %s: server %s did not answer within %s: %w",
				method, h.serverName, budget.limit, errMCPTimeout)
		}
		return err
	}
	defer resp.Body.Close()
	h.rememberSession(method, resp)

	data, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusNotFound && h.hasSession() {
		return fmt.Errorf("mcp %s: server %s rejected the session (HTTP 404); the MCP session expired", method, h.serverName)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("mcp %s http status %d: %s", method, resp.StatusCode, string(data))
	}
	if id == 0 || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if strings.Contains(strings.ToLower(resp.Header.Get("content-type")), "text/event-stream") {
		data, err = sseResponseData(data, id)
		if err != nil {
			return fmt.Errorf("mcp %s sse: %w", method, err)
		}
	}
	// 同样按 id 无关的方式解：一个把 id 回成字符串的 server 不该让整帧解析失败。
	rpcResp, err := decodeResultFrame(data)
	if err != nil {
		return err
	}
	if rpcResp.Error != nil {
		return fmt.Errorf("mcp %s error %d: %s", method, rpcResp.Error.Code, rpcResp.Error.Message)
	}
	h.rememberProtocolVersion(method, rpcResp.Result)
	if out == nil {
		return nil
	}
	return json.Unmarshal(rpcResp.Result, out)
}

func (h *httpRPC) applySession(req *http.Request) {
	h.mu.Lock()
	sessionID, negotiated := h.sessionID, h.negotiated
	h.mu.Unlock()
	if sessionID != "" {
		req.Header.Set(sessionIDHeader, sessionID)
	}
	if negotiated != "" {
		req.Header.Set(protocolVersionHeader, negotiated)
	}
}

func (h *httpRPC) rememberSession(method string, resp *http.Response) {
	id := strings.TrimSpace(resp.Header.Get(sessionIDHeader))
	if id == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	// 只有 initialize 能开一个新会话；之后 server 重发同一个 id 也没关系。
	if h.sessionID == "" || method == "initialize" {
		h.sessionID = id
	}
}

func (h *httpRPC) hasSession() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sessionID != ""
}

// rememberProtocolVersion 采纳 server 在握手里回的版本。协议规定 server 可以回一个
// 和请求不同的版本；采纳它（而不是继续宣称我们请求的那个）才是 spec 要求的行为。
func (h *httpRPC) rememberProtocolVersion(method string, result json.RawMessage) {
	if method != "initialize" {
		return
	}
	var decoded initializeResult
	if err := json.Unmarshal(result, &decoded); err != nil {
		return
	}
	version := strings.TrimSpace(decoded.ProtocolVersion)
	if version == "" {
		version = protocolVersion
	}
	h.mu.Lock()
	h.negotiated = version
	h.mu.Unlock()
}

// sseResponseData 从一段 SSE 响应体里挑出回答 id 的那一帧。
//
// 不能直接拿第一个 data 事件：server 完全可以先推几条 notifications/progress
// 再给结果，那样第一帧是通知，解析出来就是一个没有 result 的空壳。匹配不到 id
// 时退回第一帧，保持对不带 id 的简化 server 的兼容。
func sseResponseData(data []byte, id int64) ([]byte, error) {
	events, err := sseDataEvents(data)
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return nil, fmt.Errorf("no data event found")
	}
	for _, event := range events {
		var frame struct {
			ID *int64 `json:"id"`
		}
		if err := json.Unmarshal(event, &frame); err != nil {
			continue
		}
		if frame.ID != nil && *frame.ID == id {
			return event, nil
		}
	}
	return events[0], nil
}

func sseDataEvents(data []byte) ([][]byte, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var events [][]byte
	var eventLines []string
	flush := func() {
		if len(eventLines) == 0 {
			return
		}
		payload := strings.TrimSpace(strings.Join(eventLines, "\n"))
		eventLines = eventLines[:0]
		if payload == "" || payload == "[DONE]" {
			return
		}
		events = append(events, []byte(payload))
	}
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, "data:") {
			eventLines = append(eventLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	flush()
	return events, nil
}
