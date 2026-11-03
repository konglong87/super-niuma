package tools

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
)

func CheckNetworkURL(rawURL string, sandbox SandboxConfig) error {
	if sandbox.NetworkDisabled {
		return fmt.Errorf("network access is disabled by sandbox settings")
	}
	if len(sandbox.NetworkAllowDomains) == 0 && len(sandbox.NetworkDenyDomains) == 0 {
		return nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return fmt.Errorf("network URL host is required")
	}
	if DomainMatches(host, sandbox.NetworkDenyDomains) {
		return fmt.Errorf("network domain %s is denied by sandbox settings", host)
	}
	if len(sandbox.NetworkAllowDomains) > 0 && !DomainMatches(host, sandbox.NetworkAllowDomains) {
		return fmt.Errorf("network domain %s is not in sandbox allowDomains", host)
	}
	return nil
}

func NetworkHTTPClient(base *http.Client, sandbox SandboxConfig) (*http.Client, error) {
	transport, err := NetworkHTTPTransport(baseTransport(base), sandbox)
	if err != nil {
		return nil, err
	}
	client := &http.Client{}
	if base != nil {
		*client = *base
	}
	client.Transport = transport
	return client, nil
}

func NetworkHTTPTransport(base http.RoundTripper, sandbox SandboxConfig) (http.RoundTripper, error) {
	mode := strings.ToLower(strings.TrimSpace(sandbox.NetworkProxyMode))
	proxyRequired := sandbox.NetworkProxyRequired || mode == "required"
	switch mode {
	case "", "optional", "required":
	case "off", "direct":
		proxyRequired = false
	default:
		return nil, fmt.Errorf("unsupported sandbox.network.proxy.mode: %s", sandbox.NetworkProxyMode)
	}
	if proxyRequired && strings.TrimSpace(sandbox.NetworkProxyURL) == "" {
		return nil, fmt.Errorf("network proxy is required by sandbox settings but proxy.url is empty")
	}
	if sandbox.NetworkMITMRequired && strings.TrimSpace(sandbox.NetworkMITMCAFile) == "" {
		return nil, fmt.Errorf("network MITM CA is required by sandbox settings but mitm.caFile is empty")
	}

	transport := cloneHTTPTransport(base)
	if mode == "off" || mode == "direct" {
		transport.Proxy = nil
	} else if strings.TrimSpace(sandbox.NetworkProxyURL) != "" {
		proxyURL, err := url.Parse(strings.TrimSpace(sandbox.NetworkProxyURL))
		if err != nil {
			return nil, fmt.Errorf("invalid sandbox network proxy URL: %w", err)
		}
		if proxyURL.Scheme == "" || proxyURL.Host == "" {
			return nil, fmt.Errorf("invalid sandbox network proxy URL: scheme and host are required")
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	}
	if strings.TrimSpace(sandbox.NetworkMITMCAFile) != "" {
		pool, err := loadCertPool(sandbox.NetworkMITMCAFile)
		if err != nil {
			return nil, err
		}
		tlsConfig := &tls.Config{RootCAs: pool}
		if transport.TLSClientConfig != nil {
			tlsConfig = transport.TLSClientConfig.Clone()
			tlsConfig.RootCAs = pool
		}
		transport.TLSClientConfig = tlsConfig
	}
	return transport, nil
}

func NetworkProxyEnv(sandbox SandboxConfig) []string {
	var env []string
	proxyURL := strings.TrimSpace(sandbox.NetworkProxyURL)
	mode := strings.ToLower(strings.TrimSpace(sandbox.NetworkProxyMode))
	if proxyURL != "" && mode != "off" && mode != "direct" {
		env = append(env,
			"SANDBOX_NETWORK_PROXY="+proxyURL,
			"HTTP_PROXY="+proxyURL,
			"HTTPS_PROXY="+proxyURL,
			"ALL_PROXY="+proxyURL,
		)
	}
	if caFile := strings.TrimSpace(sandbox.NetworkMITMCAFile); caFile != "" {
		env = append(env, "SSL_CERT_FILE="+caFile, "SANDBOX_NETWORK_MITM_CA="+caFile)
	}
	if sandbox.NetworkMITMRequired {
		env = append(env, "SANDBOX_NETWORK_MITM=required")
	}
	return env
}

func DomainMatches(host string, domains []string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	for _, domain := range domains {
		domain = strings.ToLower(strings.TrimSpace(domain))
		domain = strings.TrimPrefix(domain, ".")
		if domain == "" {
			continue
		}
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

func baseTransport(base *http.Client) http.RoundTripper {
	if base != nil && base.Transport != nil {
		return base.Transport
	}
	return http.DefaultTransport
}

func cloneHTTPTransport(base http.RoundTripper) *http.Transport {
	if transport, ok := base.(*http.Transport); ok && transport != nil {
		return transport.Clone()
	}
	return http.DefaultTransport.(*http.Transport).Clone()
}

func loadCertPool(path string) (*x509.CertPool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read sandbox MITM CA file: %w", err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(data) {
		return nil, fmt.Errorf("sandbox MITM CA file does not contain a valid PEM certificate")
	}
	return pool, nil
}
