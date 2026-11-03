package server

import (
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"
	"strings"
)

// AUDIT-P1-21: server 对外暴露前的三道闸门集中在这里。
//
// 修复前的组合拳是：`authorize` 在 token 为空时直接放行（fail-open），而部署文档
// 明确教用户 `--host 0.0.0.0`。两者叠加，一个忘了 `--auth-token` 的远程部署会把
// /trace（完整会话）和 /prompt-dump（完整 prompt 正文）匿名暴露给整个网络。
//
// 这里不再依赖「用户记得配 token」，而是让那个组合根本无法启动。

// ValidateBind 在监听之前拒绝「绑到本机之外 + 没有 token」的组合。
//
// Run 自己会调它，所以嵌入方不做任何事也受保护；导出是为了让 CLI 能在打印
// "Starting server on ..." 之前先问一句 —— 明明不会启动却先说自己在启动，
// 是上一版错误信息返工时刚清掉的那类噪音。
//
// 刻意没有环境变量逃生口：逃生口存在，这个组合就仍然可能出现，而它正是本条
// 审计项要消灭的东西。本机自用（127.0.0.1）不受影响，仍然可以不配 token。
func ValidateBind(opts Options) error {
	if strings.TrimSpace(opts.Host) == "" {
		// 与 Run 的默认值保持一致，否则 CLI 侧的预检会比 Run 更严格。
		opts.Host = "127.0.0.1"
	}
	return validateServerBind(opts)
}

func validateServerBind(opts Options) error {
	if strings.TrimSpace(opts.AuthToken) != "" {
		return nil
	}
	if hostIsLoopbackOnly(opts.Host) {
		return nil
	}
	return fmt.Errorf(
		"refusing to start: --host %q accepts connections from other machines, but no auth token is set\n"+
			"  every endpoint would be anonymous, including /trace (full sessions) and /prompt-dump (full prompt text)\n"+
			"  pick one:\n"+
			"    --auth-token <long random string>   keep the remote bind and require a token\n"+
			"    --host 127.0.0.1                    local-only, no token needed",
		opts.Host)
}

// hostIsLoopbackOnly 判断绑定地址是否只能被本机访问。
//
// 非 IP 的主机名一律按「远端可达」处理：这里刻意不做 DNS 解析，宁可多要一个
// token，也不让启动路径依赖网络，更不让一次解析失败把判断翻成「安全」。
func hostIsLoopbackOnly(host string) bool {
	host = strings.TrimSpace(host)
	switch host {
	case "", "localhost":
		// 空值由 Run 兜成 127.0.0.1；localhost 在所有平台都解析到回环。
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// secureTokenEqual 是全部 token 比较的唯一入口，用常量时间比较避免逐字节
// 短路带来的时序信息泄漏。长度不同直接返回 false（ConstantTimeCompare 的既有
// 语义），长度本身不是秘密。
func secureTokenEqual(got, want string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// requestIsProxied 判断请求是否经过了反向代理。
//
// requireLocalClient 只能看到 r.RemoteAddr，而 Nginx 与 server 同机部署时
// RemoteAddr 恰恰是回环地址 —— 那样一来「只服务本机」会被整条代理链绕过。
// 带转发头的请求一律判定为非本机。
func requestIsProxied(r *http.Request) bool {
	for _, header := range []string{"X-Forwarded-For", "X-Real-Ip", "Forwarded", "X-Forwarded-Host"} {
		if strings.TrimSpace(r.Header.Get(header)) != "" {
			return true
		}
	}
	return false
}

// requireLocalClient 是 /prompt-dump 在 token 之上的额外门槛。
//
// prompt dump 落的是完整 prompt 正文（system prompt、全部历史消息、工具结果），
// 泄漏面比 /trace 还大；而它本身就是本机调试视图：数据来自本机文件，只有本机
// 开着 dump 环境变量时才存在。所以只服务本机直连的请求，需要远程看就开 SSH 隧道。
func requireLocalClient(w http.ResponseWriter, r *http.Request) bool {
	if !requestIsProxied(r) && remoteAddrIsLoopback(r.RemoteAddr) {
		return true
	}
	http.Error(w, "prompt dump is only served to local clients; use an SSH tunnel to reach it remotely", http.StatusForbidden)
	return false
}

func remoteAddrIsLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(remoteAddr))
	if err != nil {
		host = strings.TrimSpace(remoteAddr)
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}
