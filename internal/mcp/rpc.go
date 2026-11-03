package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/konglong87/super-niuma/internal/observability"
)

// MCP 调用的分段超时预算。
//
// 设计参考 internal/anthropic/httpclient.go：刻意**不**给整次调用挂一个覆盖全程的
// 硬 deadline。MCP 的 tools/call 可以合法地跑上几分钟（编译、跑测试、抓网页），
// 一刀切的总超时会把正常的长调用砍断，和 AUDIT-P0-07 里流式响应被砍是同一个坑。
// 因此按阶段拆开：
//
//   - request —— initialize 和各类 list/read/get 元数据往返。这些答不上来就是
//     server 坏了，用总时长卡。
//   - toolCallIdle —— tools/call 的**服务端静默**上限，不是总时长。收到任何一帧
//     （包括 notifications/progress）都续期，所以 server 只要还在说话就一直等，
//     彻底哑掉才判死。
//   - shutdown —— Close 时等子进程自己退出的宽限期，超时才 SIGKILL。
const (
	mcpRequestTimeout      = 60 * time.Second
	mcpToolCallIdleTimeout = 10 * time.Minute
	mcpShutdownGrace       = 5 * time.Second
)

type mcpTimeoutBudget struct {
	request      time.Duration
	toolCallIdle time.Duration
	shutdown     time.Duration
}

// mcpTimeouts 做成变量只是为了让测试把分钟级压到毫秒级，否则超时路径根本跑不起来。
// client/connection 构造时必须通过 snapshotMCPTimeouts 复制，后台 goroutine 不得
// 直接读取这个测试可变的包级值。
var (
	mcpTimeoutsMu sync.RWMutex
	mcpTimeouts   = defaultMCPTimeouts()
)

func defaultMCPTimeouts() mcpTimeoutBudget {
	return mcpTimeoutBudget{
		request:      mcpRequestTimeout,
		toolCallIdle: mcpToolCallIdleTimeout,
		shutdown:     mcpShutdownGrace,
	}
}

func snapshotMCPTimeouts() mcpTimeoutBudget {
	mcpTimeoutsMu.RLock()
	defer mcpTimeoutsMu.RUnlock()
	return mcpTimeouts
}

// errMCPTimeout 标记「是超时守卫结束了这次调用」，让上层能把它和用户主动取消
// （context.Canceled）区分开。
var errMCPTimeout = errors.New("mcp timeout")

// callBudget 决定一次调用怎么计时；idle 为 true 时每收到一帧都续期。
type callBudget struct {
	limit time.Duration
	idle  bool
}

func budgetFor(timeouts mcpTimeoutBudget, method string) callBudget {
	if method == methodToolsCall {
		return callBudget{limit: timeouts.toolCallIdle, idle: true}
	}
	return callBudget{limit: timeouts.request}
}

const methodToolsCall = "tools/call"

type rpcCaller interface {
	call(ctx context.Context, method string, params any, out any) error
	callWithCallback(ctx context.Context, method string, params any, out any, handler serverCallbackHandler) error
	notify(ctx context.Context, method string, params any) error
}

type serverCallbackHandler func(method string, params json.RawMessage) (json.RawMessage, *rpcError)

// pendingCall 是一次在途调用在读循环侧的落点。response 有缓冲，所以派发响应的
// 读循环永远不会被一个已经放弃等待的调用方卡住。
type pendingCall struct {
	response chan rpcResponse
	activity chan struct{}
	handler  serverCallbackHandler
}

// rpcConn 用一个独立的读循环把 server 的帧按 id 多路分解到各个在途调用。
//
// 旧实现把「写请求 + 阻塞读到自己的响应」整个放在一把锁里，于是同一 server 的
// 并发工具调用互相排队，而且没有任何办法在读上加超时或响应 context 取消。
type rpcConn struct {
	serverName string
	timeouts   mcpTimeoutBudget

	writer  io.Writer
	writeMu sync.Mutex

	mu       sync.Mutex
	nextID   int64
	pending  map[int64]*pendingCall
	closeErr error

	done chan struct{}

	// readStopped 在读循环不再从 stdout 读时关闭。stdio 的监管方必须等到这一刻
	// 才能调 cmd.Wait —— exec 的 Wait 结束时会关掉 StdoutPipe 的读端，抢在读循环
	// 前面调用就会把 server 的最后一帧连同它的错误说明一起撕掉。
	readStopped chan struct{}
	// readDone 在 closeReason 和连接关闭都处理完成后关闭。readStopped 解决
	// supervise 与 explainStreamEnd 的互等；readDone 则让 Client.Close 可以真正
	// join 读循环，不把后台工作泄漏到调用方 cleanup 之后。
	readDone chan struct{}

	// closeReason 让连接的拥有者把「流断了」翻译成更有用的话。stdio 客户端用它
	// 等一小会子进程的退出码，好把裸 EOF 换成「server 崩了，退出码 1，stderr 是…」。
	closeReason func(error) error

	// setWriteDeadline 给写请求加时限（stdio 的 stdin 是可轮询的管道）。没有它，
	// 一个不再读 stdin 的 server 会让 writeFrame 永久阻塞在锁里，超时和取消都够不着。
	setWriteDeadline func(time.Time) error
}

type rpcConnOptions struct {
	closeReason      func(error) error
	setWriteDeadline func(time.Time) error
	timeouts         mcpTimeoutBudget
}

func newRPCConn(serverName string, reader io.Reader, writer io.Writer) *rpcConn {
	return newRPCConnWithOptions(serverName, reader, writer, rpcConnOptions{timeouts: snapshotMCPTimeouts()})
}

func newRPCConnWithOptions(serverName string, reader io.Reader, writer io.Writer, opts rpcConnOptions) *rpcConn {
	c := &rpcConn{
		serverName:       serverName,
		timeouts:         opts.timeouts,
		writer:           writer,
		nextID:           1,
		pending:          map[int64]*pendingCall{},
		done:             make(chan struct{}),
		readStopped:      make(chan struct{}),
		readDone:         make(chan struct{}),
		closeReason:      opts.closeReason,
		setWriteDeadline: opts.setWriteDeadline,
	}
	go c.readLoop(bufio.NewReader(reader))
	return c
}

func (c *rpcConn) call(ctx context.Context, method string, params any, out any) error {
	return c.callWithCallback(ctx, method, params, out, nil)
}

func (c *rpcConn) callWithCallback(ctx context.Context, method string, params any, out any, handler serverCallbackHandler) error {
	pending := &pendingCall{
		response: make(chan rpcResponse, 1),
		activity: make(chan struct{}, 1),
		handler:  handler,
	}

	c.mu.Lock()
	if c.closeErr != nil {
		err := c.closeErr
		c.mu.Unlock()
		return fmt.Errorf("mcp %s: %w", method, err)
	}
	id := c.nextID
	c.nextID++
	c.pending[id] = pending
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	if err := c.writeFrame(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}); err != nil {
		return err
	}
	return c.await(ctx, method, budgetFor(c.timeouts, method), pending, out)
}

func (c *rpcConn) notify(_ context.Context, method string, params any) error {
	c.mu.Lock()
	closeErr := c.closeErr
	c.mu.Unlock()
	if closeErr != nil {
		return fmt.Errorf("mcp %s: %w", method, closeErr)
	}
	return c.writeFrame(rpcRequest{JSONRPC: "2.0", Method: method, Params: params})
}

// await 是超时、取消和连接中断三者的汇合点。
func (c *rpcConn) await(ctx context.Context, method string, budget callBudget, pending *pendingCall, out any) error {
	var expired <-chan time.Time
	var timer *time.Timer
	if budget.limit > 0 {
		timer = time.NewTimer(budget.limit)
		defer timer.Stop()
		expired = timer.C
	}
	for {
		select {
		case res := <-pending.response:
			return decodeResponse(method, res, out)
		case <-pending.activity:
			if budget.idle && timer != nil {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(budget.limit)
			}
		case <-ctx.Done():
			return ctx.Err()
		case <-expired:
			return fmt.Errorf("mcp %s: server %s produced no output for %s: %w",
				method, c.serverName, budget.limit, errMCPTimeout)
		case <-c.done:
			// 读循环先派发响应再关连接，所以已经送达的响应优先于中断错误。
			select {
			case res := <-pending.response:
				return decodeResponse(method, res, out)
			default:
			}
			c.mu.Lock()
			err := c.closeErr
			c.mu.Unlock()
			return fmt.Errorf("mcp %s: %w", method, err)
		}
	}
}

func decodeResponse(method string, res rpcResponse, out any) error {
	if res.Error != nil {
		return fmt.Errorf("mcp %s error %d: %s", method, res.Error.Code, res.Error.Message)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(res.Result, out)
}

func (c *rpcConn) readLoop(reader *bufio.Reader) {
	defer close(c.readDone)
	for {
		line, err := reader.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			c.dispatch(line)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = errors.New("server closed its stdout")
			}
			// 先宣告「我不读了」再去问原因：closeReason 会等子进程的退出码，
			// 而收尸方在等这个信号，两边互等就会各自空转到宽限期。
			close(c.readStopped)
			if c.closeReason != nil {
				err = c.closeReason(err)
			}
			c.close(err)
			return
		}
	}
}

func (c *rpcConn) dispatch(line []byte) {
	// 收到任何一帧都说明 server 还活着，据此给在途调用的空闲计时器续期。
	c.touchPending()

	// id 用 RawMessage 收：JSON-RPC 2.0 允许字符串 id，用 int64 直收会让整帧
	// 解析失败，然后帧被无声丢弃、调用方一直等到超时。
	var frame struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(line, &frame); err != nil {
		return
	}
	hasID := len(frame.ID) > 0 && string(frame.ID) != "null"
	switch {
	case frame.Method != "" && hasID:
		// server → client 请求。handler 可能要等用户批准，绝不能卡住读循环，
		// 否则同一 server 上的其它在途调用全被这次审批挂起。id 原样回传，
		// 不做数字化 —— 我们不产生它，也就无权改写它的类型。
		id := append(json.RawMessage(nil), frame.ID...)
		go c.answerServerRequest(id, frame.Method, frame.Params)
	case frame.Method != "":
		c.observeNotification(frame.Method, frame.Params)
	case hasID:
		id, ok := responseID(frame.ID)
		if !ok {
			return
		}
		payload, err := decodeResultFrame(line)
		if err != nil {
			return
		}
		payload.ID = id
		c.deliver(id, payload)
	}
}

// decodeResultFrame 只取 result / error，不碰 id。id 的类型由调用方自己判定 ——
// 直接往 rpcResponse（ID 是 int64）里解会因为一个字符串 id 让整帧解析失败。
func decodeResultFrame(line []byte) (rpcResponse, error) {
	var payload struct {
		JSONRPC string          `json:"jsonrpc"`
		Result  json.RawMessage `json:"result,omitempty"`
		Error   *rpcError       `json:"error,omitempty"`
	}
	if err := json.Unmarshal(line, &payload); err != nil {
		return rpcResponse{}, err
	}
	return rpcResponse{JSONRPC: payload.JSONRPC, Result: payload.Result, Error: payload.Error}, nil
}

// responseID 把应答的 id 还原成我们发出去的那个整数。id 是我们生成的，所以只可能
// 是整数，但有的 server 会把它回成字符串。
func responseID(raw json.RawMessage) (int64, bool) {
	var id int64
	if err := json.Unmarshal(raw, &id); err == nil {
		return id, true
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return 0, false
	}
	id, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	return id, err == nil
}

func (c *rpcConn) touchPending() {
	c.mu.Lock()
	calls := make([]*pendingCall, 0, len(c.pending))
	for _, pending := range c.pending {
		calls = append(calls, pending)
	}
	c.mu.Unlock()
	for _, pending := range calls {
		select {
		case pending.activity <- struct{}{}:
		default:
		}
	}
}

func (c *rpcConn) deliver(id int64, res rpcResponse) {
	c.mu.Lock()
	pending := c.pending[id]
	delete(c.pending, id)
	c.mu.Unlock()
	if pending == nil {
		return
	}
	pending.response <- res
}

// observeNotification 目前只把通知记进日志。旧实现对无 id 的帧一律静默丢弃，连
// server 报的错误日志都看不到；至少让它们可观测，并且（通过 dispatch 里的
// touchPending）给长工具调用续期。
//
// 已知未做：notifications/tools/list_changed 到达后**不会**重新注册工具 ——
// 工具注册表是跨包共享的、会话中途热替换需要更大的改动，见审计文档 AUDIT-P1-18。
func (c *rpcConn) observeNotification(method string, params json.RawMessage) {
	observability.Debug(context.Background(), nil, "mcp.notification", "mcp.rpcConn.observeNotification",
		"MCP server notification",
		"server", c.serverName, "method", method, "params", truncateForLog(string(params)))
}

// activeHandler 找出该由谁回答一个 server→client 请求。
//
// MCP 不给这类请求任何关联信息，所以只有「恰好一个在途调用带 handler」时答案才是
// 确定的。旧的串行实现天然满足这个前提；多路复用之后不再满足，而猜错的代价不是
// 小事 —— 审批弹窗会显示**另一个**工具的名字，写回的权限规则也会记到那个工具头上。
// 所以有歧义时宁可拒绝，也不猜。
func (c *rpcConn) activeHandler() (serverCallbackHandler, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var handler serverCallbackHandler
	count := 0
	for _, pending := range c.pending {
		if pending.handler != nil {
			handler = pending.handler
			count++
		}
	}
	if count != 1 {
		return nil, count > 1
	}
	return handler, false
}

func (c *rpcConn) answerServerRequest(id json.RawMessage, method string, params json.RawMessage) {
	handler, ambiguous := c.activeHandler()
	if handler == nil {
		message := deniedCallbackMessage(method)
		if ambiguous {
			message = "MCP callback cannot be attributed: more than one tool call is in flight on server " +
				c.serverName + " and the protocol does not correlate " + method + " with either"
		}
		_ = c.writeFrame(callbackResponse{
			JSONRPC: "2.0",
			ID:      id,
			Error:   &rpcError{Code: -32001, Message: message},
		})
		return
	}
	result, callbackErr := handler(method, params)
	if callbackErr != nil {
		_ = c.writeFrame(callbackResponse{JSONRPC: "2.0", ID: id, Error: callbackErr})
		return
	}
	if len(result) == 0 {
		result = json.RawMessage(`{}`)
	}
	_ = c.writeFrame(callbackResponse{JSONRPC: "2.0", ID: id, Result: result})
}

func deniedCallbackMessage(method string) string {
	switch method {
	case "sampling/createMessage":
		return "MCP sampling callback is denied by client permissions"
	case "elicitation/create", "elicitation/request", "permissions/request":
		return "MCP permission/elicitation callback is denied by client permissions"
	default:
		return "MCP client callback is not approved in headless mode: " + method
	}
}

// writeFrame 串行化写并给它设时限。
//
// 时限是必须的：一个不再读 stdin 的 server 会让写永久阻塞，而写发生在 await
// 之前、还攥着 writeMu —— 于是既绕过了超时预算和 context，又会把回调应答一起
// 堵死（server 在等回调应答才继续读 stdin，形成死锁）。
//
// 写失败后流里可能只留了半行 JSON，后续帧再也无法解析，所以直接判连接死亡，
// 让所有调用方快速失败而不是对着一条坏掉的流继续等。
func (c *rpcConn) writeFrame(frame any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.setWriteDeadline != nil && c.timeouts.request > 0 {
		if err := c.setWriteDeadline(time.Now().Add(c.timeouts.request)); err == nil {
			defer func() { _ = c.setWriteDeadline(time.Time{}) }()
		}
	}
	if err := json.NewEncoder(c.writer).Encode(frame); err != nil {
		if errors.Is(err, os.ErrDeadlineExceeded) {
			err = fmt.Errorf("server %s stopped reading its stdin after %s: %w",
				c.serverName, c.timeouts.request, errMCPTimeout)
		}
		c.close(err)
		return err
	}
	return nil
}

// close 让所有在途和后续调用带着 err 返回。只有第一次调用生效，因此「进程退出」
// 这种带 stderr 的具体错误不会被随后的 EOF 覆盖成泛泛的一句话。
func (c *rpcConn) close(err error) {
	c.mu.Lock()
	if c.closeErr != nil {
		c.mu.Unlock()
		return
	}
	if err == nil {
		err = errors.New("connection closed")
	}
	c.closeErr = err
	c.mu.Unlock()
	close(c.done)
}

func truncateForLog(value string) string {
	const limit = 512
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}
