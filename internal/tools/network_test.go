package tools

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckNetworkURLPolicy(t *testing.T) {
	if err := CheckNetworkURL("https://docs.example.com/page", SandboxConfig{NetworkAllowDomains: []string{"example.com"}}); err != nil {
		t.Fatalf("allow err = %v", err)
	}
	if err := CheckNetworkURL("https://blocked.example.com/page", SandboxConfig{NetworkDenyDomains: []string{"blocked.example.com"}}); err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("deny err = %v", err)
	}
	if err := CheckNetworkURL("https://other.test/page", SandboxConfig{NetworkAllowDomains: []string{"example.com"}}); err == nil || !strings.Contains(err.Error(), "allowDomains") {
		t.Fatalf("allow-list err = %v", err)
	}
	if err := CheckNetworkURL("https://example.com", SandboxConfig{NetworkDisabled: true}); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("disabled err = %v", err)
	}
}

func TestNetworkHTTPTransportProxyPolicy(t *testing.T) {
	rt, err := NetworkHTTPTransport(nil, SandboxConfig{
		NetworkProxyURL:      "http://127.0.0.1:8080",
		NetworkProxyMode:     "required",
		NetworkProxyRequired: true,
	})
	if err != nil {
		t.Fatalf("NetworkHTTPTransport() err = %v", err)
	}
	transport := rt.(*http.Transport)
	req := &http.Request{URL: mustURL(t, "https://example.com")}
	proxyURL, err := transport.Proxy(req)
	if err != nil {
		t.Fatalf("proxy err = %v", err)
	}
	if proxyURL == nil || proxyURL.String() != "http://127.0.0.1:8080" {
		t.Fatalf("proxyURL = %v", proxyURL)
	}
	if _, err := NetworkHTTPTransport(nil, SandboxConfig{NetworkProxyRequired: true}); err == nil || !strings.Contains(err.Error(), "proxy.url is empty") {
		t.Fatalf("required proxy err = %v", err)
	}
	if _, err := NetworkHTTPTransport(nil, SandboxConfig{NetworkProxyURL: "://bad"}); err == nil {
		t.Fatalf("expected invalid proxy URL error")
	}
	rt, err = NetworkHTTPTransport(nil, SandboxConfig{NetworkProxyMode: "direct", NetworkProxyURL: "http://127.0.0.1:8080"})
	if err != nil {
		t.Fatalf("direct mode err = %v", err)
	}
	if rt.(*http.Transport).Proxy != nil {
		t.Fatalf("direct mode proxy function should be nil")
	}
}

func TestNetworkHTTPTransportMITMPolicy(t *testing.T) {
	if _, err := NetworkHTTPTransport(nil, SandboxConfig{NetworkMITMRequired: true}); err == nil || !strings.Contains(err.Error(), "mitm.caFile is empty") {
		t.Fatalf("required MITM err = %v", err)
	}
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, []byte("not a cert"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := NetworkHTTPTransport(nil, SandboxConfig{NetworkMITMCAFile: path}); err == nil || !strings.Contains(err.Error(), "valid PEM certificate") {
		t.Fatalf("invalid CA err = %v", err)
	}
}

func TestNetworkProxyEnv(t *testing.T) {
	env := NetworkProxyEnv(SandboxConfig{
		NetworkProxyURL:     "http://proxy.local:8080",
		NetworkMITMCAFile:   "/tmp/dev-ca.pem",
		NetworkMITMRequired: true,
	})
	joined := strings.Join(env, "\x00")
	for _, want := range []string{"HTTP_PROXY=http://proxy.local:8080", "HTTPS_PROXY=http://proxy.local:8080", "SSL_CERT_FILE=/tmp/dev-ca.pem", "SANDBOX_NETWORK_MITM=required"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("env missing %q in %+v", want, env)
		}
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
