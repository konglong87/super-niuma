package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"

	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/observability"
	"github.com/konglong87/super-niuma/internal/procenv"
)

type Client struct {
	ServerName string
	timeouts   mcpTimeoutBudget
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	stderr     *stderrTail
	rpc        rpcCaller
	conn       *rpcConn

	// negotiated 是握手的结果：server 报的协议版本和它声明的 capabilities。
	negotiated initializeResult

	closing atomic.Bool
	exited  chan struct{}
	exitErr atomic.Pointer[error]
}

type StdioConfig struct {
	Command string
	Args    []string
	Env     map[string]string
}

type HTTPConfig struct {
	URL     string
	Headers map[string]string
}

func StartConfigured(ctx context.Context, serverName string, cfg config.MCPServerConfig) (*Client, error) {
	switch cfg.Type {
	case "", "stdio":
		return StartStdio(ctx, serverName, StdioConfig{Command: cfg.Command, Args: cfg.Args, Env: cfg.Env})
	case "http", "streamable-http":
		return StartHTTP(ctx, serverName, HTTPConfig{URL: cfg.URL, Headers: cfg.Headers})
	case "sse":
		// 旧版 HTTP+SSE 传输是两个端点（GET 开流 + POST 发消息），和 streamable-http
		// 不是一回事，硬套过去只会以难懂的方式失败。宁可明确说不支持，见 AUDIT-P1-18。
		return nil, fmt.Errorf("mcp server %s: the legacy HTTP+SSE transport is not implemented; "+
			"use type \"http\" if the server also speaks streamable-http", serverName)
	default:
		return nil, fmt.Errorf("unsupported MCP server type %q for %s", cfg.Type, serverName)
	}
}

func StartStdio(ctx context.Context, serverName string, cfg StdioConfig) (*Client, error) {
	if cfg.Command == "" {
		return nil, fmt.Errorf("mcp server %s command is empty", serverName)
	}
	timeouts := snapshotMCPTimeouts()
	cmd := exec.CommandContext(ctx, cfg.Command, cfg.Args...)
	// Always set Env: leaving it nil inherits the parent environment wholesale,
	// so a stdio server got every credential we had (AUDIT-P1-16). cfg.Env is
	// applied on top and is not filtered — that one the user configured.
	cmd.Env = procenv.Sanitized(envPairs(cfg.Env)...)
	// WaitDelay 兜住「孙进程继承了管道」这种情况：kill 只杀直接子进程，孙进程
	// （npx → node、包装脚本 → 真正的 server）攥着写端不放，管道就永远不会 EOF，
	// 排空 goroutine 和 cmd.Wait 都会永久挂住。WaitDelay 让 Wait 在进程退出后
	// 强制关掉这些管道。
	cmd.WaitDelay = timeouts.shutdown

	// os/exec 只在 Start 失败或 Wait 里关这些管道，所以在 Start 之前的错误路径上
	// 得自己收；否则 EMFILE 这种「正缺 fd」的时候还继续漏 fd。
	var pipes []io.Closer
	closePipes := func() {
		for _, pipe := range pipes {
			_ = pipe.Close()
		}
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	pipes = append(pipes, stdin)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		closePipes()
		return nil, err
	}
	pipes = append(pipes, stdout)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		closePipes()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp server %s failed to start %q: %w", serverName, cfg.Command, err)
	}

	client := &Client{
		ServerName: serverName,
		timeouts:   timeouts,
		cmd:        cmd,
		stdin:      stdin,
		stderr:     &stderrTail{},
		exited:     make(chan struct{}),
	}
	// exec 文档要求在 cmd.Wait 之前读完 StderrPipe，所以收尸的 goroutine 等排空完成。
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		client.stderr.drain(serverName, stderr)
	}()
	conn := newRPCConnWithOptions(serverName, stdout, stdin, rpcConnOptions{
		closeReason:      client.explainStreamEnd,
		setWriteDeadline: writeDeadlineSetter(stdin),
		timeouts:         timeouts,
	})
	client.rpc = conn
	client.conn = conn
	go client.supervise(drained)

	if err := client.Initialize(ctx); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("mcp server %s initialize failed: %w%s", serverName, err, client.stderr.suffix())
	}
	return client, nil
}

// supervise 给子进程收尸。旧实现从不调用 cmd.Wait：server 崩溃后工具还挂在注册表
// 里、每次调用只能等到一个语焉不详的 EOF，进程本身还留成僵尸。
func (c *Client) supervise(drained <-chan struct{}) {
	// cmd.Wait 返回时会关掉 StdoutPipe / StderrPipe 的读端，所以两个读者都停了
	// 才能调它 —— 抢在读循环前面调用，会把 server 的最后一帧（往往正是解释它
	// 为何要退出的那一帧）连同管道一起撕掉。
	//
	// 但这个等待必须有界：孙进程可能一直攥着管道写端，那样两个读者永远等不到
	// EOF。超过宽限期就照样进 Wait，由 cmd.WaitDelay 去强制收管。
	waitAll(c.timeouts.shutdown, drained, c.conn.readStopped)
	err := c.cmd.Wait()
	c.exitErr.Store(&err)
	close(c.exited)
	if c.closing.Load() {
		return
	}
	observability.Warn(context.Background(), nil, "mcp.server.exited", "mcp.Client.supervise",
		"MCP server exited unexpectedly",
		"server", c.ServerName, "error", errText(err), "stderr", truncateForLog(c.stderr.String()))
}

// explainStreamEnd 把「stdout 断了」升级成带退出码和 stderr 的具体错误。读循环在
// 进程退出的瞬间就会看到 EOF，而退出码要等 cmd.Wait 返回，所以这里给它一个有界的
// 等待窗口 —— 多等这一下换来的是用户真正能拿去排障的一句话。
func (c *Client) explainStreamEnd(streamErr error) error {
	if c.closing.Load() {
		return errors.New("client closed the connection")
	}
	select {
	case <-c.exited:
	case <-time.After(c.timeouts.shutdown):
		return fmt.Errorf("server %s: %w%s", c.ServerName, streamErr, c.stderr.suffix())
	}
	if c.closing.Load() {
		return errors.New("client closed the connection")
	}
	status := "exited"
	if err := c.exitErr.Load(); err != nil && *err != nil {
		status = "exited with " + (*err).Error()
	}
	return fmt.Errorf("server %s %s%s", c.ServerName, status, c.stderr.suffix())
}

func StartHTTP(ctx context.Context, serverName string, cfg HTTPConfig) (*Client, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("mcp server %s URL is empty", serverName)
	}
	timeouts := snapshotMCPTimeouts()
	client := &Client{
		ServerName: serverName,
		timeouts:   timeouts,
		rpc:        newHTTPRPC(serverName, cfg.URL, cfg.Headers, timeouts),
	}
	if err := client.Initialize(ctx); err != nil {
		return nil, fmt.Errorf("mcp server %s initialize failed: %w", serverName, err)
	}
	return client, nil
}

func (c *Client) Initialize(ctx context.Context) error {
	var result initializeResult
	if err := c.rpc.call(ctx, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "niuma",
			"version": "0.1.0-go",
		},
	}, &result); err != nil {
		return err
	}
	c.negotiated = result
	return c.rpc.notify(ctx, "notifications/initialized", map[string]any{})
}

// requireCapability 在 server 没声明某个能力时提前给出一句人话。否则用户看到的是
// 裸的 "-32601 Method not found" —— ListMcpResources 会遍历所有配置的 server，
// 只提供工具的那些每次都贡献一行没头没尾的 ERROR。
func (c *Client) requireCapability(declared *json.RawMessage, name string) error {
	if declared != nil {
		return nil
	}
	// 完全没回 capabilities 的 server（不合规但存在）不拦，照旧发请求。
	if c.negotiated.Capabilities.Tools == nil &&
		c.negotiated.Capabilities.Resources == nil &&
		c.negotiated.Capabilities.Prompts == nil {
		return nil
	}
	return fmt.Errorf("mcp server %s does not support %s", c.ServerName, name)
}

func (c *Client) ListTools() ([]ToolInfo, error) {
	return c.ListToolsContext(context.Background())
}

func (c *Client) ListToolsContext(ctx context.Context) ([]ToolInfo, error) {
	var all []ToolInfo
	err := paginate(func(cursor string) (string, error) {
		var result listToolsResult
		if err := c.rpc.call(ctx, "tools/list", pageParams(cursor), &result); err != nil {
			return "", err
		}
		all = append(all, result.Tools...)
		return result.NextCursor, nil
	})
	return all, err
}

func (c *Client) CallTool(name string, input json.RawMessage) (string, bool, error) {
	return c.CallToolWithCallback(context.Background(), name, input, nil)
}

// CallToolWithCallback 只回文本，给 `mcp call` 这类没有模型在场的调用方用。
func (c *Client) CallToolWithCallback(ctx context.Context, name string, input json.RawMessage, handler serverCallbackHandler) (string, bool, error) {
	content, isError, err := c.callToolContent(ctx, name, input, handler)
	return content.Text, isError, err
}

// callToolContent 同时回文本和可投递的图片块（TODO-061）。
func (c *Client) callToolContent(ctx context.Context, name string, input json.RawMessage, handler serverCallbackHandler) (toolCallContent, bool, error) {
	var args any = map[string]any{}
	if len(input) > 0 {
		if err := json.Unmarshal(input, &args); err != nil {
			return toolCallContent{}, true, err
		}
	}
	var result callToolResult
	if err := c.rpc.callWithCallback(ctx, methodToolsCall, map[string]any{
		"name":      name,
		"arguments": args,
	}, &result, handler); err != nil {
		return toolCallContent{}, true, err
	}
	return renderToolCallContent(result.Content), result.IsError, nil
}

func (c *Client) ListResources() ([]ResourceInfo, error) {
	return c.ListResourcesContext(context.Background())
}

func (c *Client) ListResourcesContext(ctx context.Context) ([]ResourceInfo, error) {
	if err := c.requireCapability(c.negotiated.Capabilities.Resources, "resources"); err != nil {
		return nil, err
	}
	var all []ResourceInfo
	err := paginate(func(cursor string) (string, error) {
		var result listResourcesResult
		if err := c.rpc.call(ctx, "resources/list", pageParams(cursor), &result); err != nil {
			return "", err
		}
		all = append(all, result.Resources...)
		return result.NextCursor, nil
	})
	return all, err
}

func (c *Client) ReadResource(uri string) ([]ReadResourceContent, error) {
	return c.ReadResourceContext(context.Background(), uri)
}

func (c *Client) ReadResourceContext(ctx context.Context, uri string) ([]ReadResourceContent, error) {
	var result readResourceResult
	if err := c.rpc.call(ctx, "resources/read", map[string]any{"uri": uri}, &result); err != nil {
		return nil, err
	}
	return result.Contents, nil
}

func (c *Client) ListPrompts() ([]PromptInfo, error) {
	if err := c.requireCapability(c.negotiated.Capabilities.Prompts, "prompts"); err != nil {
		return nil, err
	}
	var all []PromptInfo
	err := paginate(func(cursor string) (string, error) {
		var result listPromptsResult
		if err := c.rpc.call(context.Background(), "prompts/list", pageParams(cursor), &result); err != nil {
			return "", err
		}
		all = append(all, result.Prompts...)
		return result.NextCursor, nil
	})
	return all, err
}

func (c *Client) GetPrompt(name string, arguments map[string]string) (GetPromptResult, error) {
	if arguments == nil {
		arguments = map[string]string{}
	}
	var result GetPromptResult
	if err := c.rpc.call(context.Background(), "prompts/get", map[string]any{"name": name, "arguments": arguments}, &result); err != nil {
		return GetPromptResult{}, err
	}
	return result, nil
}

// maxListPages 给分页游标兜底，防止一个坏 server 用永不结束的游标把我们转死。
const maxListPages = 100

func paginate(fetch func(cursor string) (string, error)) error {
	cursor := ""
	for page := 0; page < maxListPages; page++ {
		next, err := fetch(cursor)
		if err != nil {
			return err
		}
		if next == "" || next == cursor {
			return nil
		}
		cursor = next
	}
	return nil
}

func pageParams(cursor string) map[string]any {
	if cursor == "" {
		return map[string]any{}
	}
	return map[string]any{"cursor": cursor}
}

// Close 关掉 stdin 让 server 自己收摊，只有它赖着不走才升级到 SIGKILL，然后等
// supervise 那边把它收掉。旧实现直接 Kill 并自己 Wait，既不给优雅退出的机会，
// 又会和监管 goroutine 抢 Wait。
func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	c.closing.Store(true)
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cmd == nil || c.cmd.Process == nil {
		return nil
	}
	select {
	case <-c.exited:
		waitAll(c.timeouts.shutdown, c.conn.readDone)
		return nil
	case <-time.After(c.timeouts.shutdown):
	}
	_ = c.cmd.Process.Kill()
	// Kill 只杀直接子进程。孙进程若继承了管道，排空和 Wait 都可能收不了尾，
	// 所以这一等也必须有界 —— 否则一个这样的 server 就能把整个进程的退出挂住
	// （LoadTools 的 cleanup 是逐个串行关的）。
	select {
	case <-c.exited:
	case <-time.After(c.timeouts.shutdown):
		observability.Warn(context.Background(), nil, "mcp.server.reap_timeout", "mcp.Client.Close",
			"MCP server did not reap within the shutdown grace; a grandchild is probably holding its pipes",
			"server", c.ServerName)
	}
	waitAll(c.timeouts.shutdown, c.conn.readDone)
	return nil
}

// waitAll 等所有 channel 关闭，最多等 limit。
func waitAll(limit time.Duration, channels ...<-chan struct{}) {
	deadline := time.NewTimer(limit)
	defer deadline.Stop()
	for _, ch := range channels {
		select {
		case <-ch:
		case <-deadline.C:
			return
		}
	}
}

// writeDeadlineSetter 在 stdin 支持 deadline 时把它暴露给 rpcConn。exec 的
// StdinPipe 底层是 os.Pipe，可轮询，所以正常情况下都拿得到。
func writeDeadlineSetter(stdin io.WriteCloser) func(time.Time) error {
	file, ok := stdin.(*os.File)
	if !ok {
		return nil
	}
	return file.SetWriteDeadline
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// renderToolContent 把工具结果的各类 content 块摊平成文本。
//
// 旧实现只保留 type=="text"，其余（图片、音频、内嵌资源）静默丢弃 —— 一个只返回
// 图片的工具在模型看来就是"什么都没返回"。现在非文本块至少留一行占位说明，
// 让模型知道有东西但拿不到，而不是以为工具是空的。可投递的图片见 media.go。
func renderToolContent(blocks []toolContentBlock) string {
	return renderToolCallContent(blocks).Text
}

func orUnknown(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unknown type"
	}
	return value
}

func envPairs(env map[string]string) []string {
	pairs := make([]string, 0, len(env))
	for k, v := range env {
		pairs = append(pairs, k+"="+v)
	}
	return pairs
}
