package mcp

import (
	"bufio"
	"context"
	"io"
	"strings"
	"sync"

	"github.com/konglong87/super-niuma/internal/observability"
)

// stderrTailBytes 是每个 server 保留的 stderr 尾巴大小。够放一段 Node/Python 栈回溯，
// 又不至于让一个话痨 server 把内存吃光。
const stderrTailBytes = 8 * 1024

// stderrScannerLineLimit 只约束「逐行记日志」这件事；超过它的行不再被拆成行来
// 记，但仍然会被读走（见 drain 末尾），绝不会把 server 堵死。
const stderrScannerLineLimit = 256 * 1024

// stderrTail 保留子进程 stderr 的最后若干字节。
//
// 旧实现从不给 cmd.Stderr 赋值，于是 server 的诊断信息直接进了 /dev/null：配错的
// server 只会静默失败，可调试性为零。现在既实时记日志，也留一份尾巴挂到错误上。
type stderrTail struct {
	mu   sync.Mutex
	buf  []byte
	seen bool
}

func (s *stderrTail) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = true
	s.buf = append(s.buf, p...)
	if len(s.buf) > stderrTailBytes {
		s.buf = s.buf[len(s.buf)-stderrTailBytes:]
	}
	return len(p), nil
}

func (s *stderrTail) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.TrimSpace(string(s.buf))
}

// suffix 把 stderr 尾巴渲染成可以直接拼到错误消息后面的一段。
func (s *stderrTail) suffix() string {
	tail := s.String()
	if tail == "" {
		return ""
	}
	return "; stderr: " + tail
}

// drainStderr 一边把 server 的 stderr 记进日志，一边留下尾巴。日志走 Debug ——
// 很多 server 把常规启动信息写 stderr，按 Warn 记会变成噪音；真正失败时尾巴会
// 被挂到错误上，那条路径才是用户看得见的。
func (s *stderrTail) drain(serverName string, reader io.Reader) {
	teed := io.TeeReader(reader, s)
	scanner := bufio.NewScanner(teed)
	scanner.Buffer(make([]byte, 0, 8*1024), stderrScannerLineLimit)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		observability.Debug(context.Background(), nil, "mcp.server.stderr", "mcp.stderrTail.drain",
			"MCP server stderr", "server", serverName, "line", truncateForLog(line))
	}
	// Scanner 停下不等于 server 停了：遇到超过 buffer 上限的单行它会永久放弃
	// （bufio.ErrTooLong）。而只要我们不再读，64KB 的 stderr 管道就会填满、
	// 子进程**永久阻塞在 write(2) 上** —— 一条超长的 JSON 错误负载就够了。
	// 所以剩下的字节必须无条件读干净；tail 仍然通过 TeeReader 拿到它们。
	_, _ = io.Copy(io.Discard, teed)
}
