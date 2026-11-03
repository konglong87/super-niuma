package channelbridge

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

func (b *Bridge) authorizedURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Fragment != "" || u.RawQuery != "" || u.Hostname() == "" || u.Opaque != "" || u.RawPath != "" {
		return nil, ErrConfiguration
	}
	host := strings.ToLower(u.Hostname())
	ip, ipErr := netip.ParseAddr(host)
	loopback := host == "localhost" || (ipErr == nil && ip.Unmap().IsLoopback())
	if u.Scheme != "https" && !(b.options.AllowLoopback && loopback && u.Scheme == "http") {
		return nil, ErrConfiguration
	}
	if loopback && !b.options.AllowLoopback {
		return nil, ErrConfiguration
	}
	if ipErr == nil && !allowedIP(ip, b.options.AllowLoopback) {
		return nil, ErrConfiguration
	}
	if u.Path == "" {
		u.Path = "/"
	}
	return u, nil
}
func allowedIP(ip netip.Addr, loopback bool) bool {
	ip = ip.Unmap()
	if ip.IsLoopback() {
		return loopback
	}
	return ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() && !ip.IsUnspecified()
}
func (b *Bridge) httpClient(injected *http.Client) *http.Client {
	client := http.Client{Timeout: DefaultTimeout}
	if injected != nil {
		client = *injected
	}
	if client.Timeout <= 0 || client.Timeout > DefaultTimeout {
		client.Timeout = DefaultTimeout
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if injected == nil {
		dialer := &net.Dialer{Timeout: DefaultTimeout, KeepAlive: 30 * time.Second}
		client.Transport = &http.Transport{Proxy: nil, ForceAttemptHTTP2: true, TLSHandshakeTimeout: DefaultTimeout, ResponseHeaderTimeout: DefaultTimeout, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, ErrConfiguration
			}
			ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil || len(ips) == 0 {
				return nil, ErrConfiguration
			}
			for _, ip := range ips {
				if !allowedIP(ip, b.options.AllowLoopback) {
					return nil, ErrConfiguration
				}
			}
			// Dial the validated numeric address rather than resolving the host again.
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
		}}
	}
	return &client
}
