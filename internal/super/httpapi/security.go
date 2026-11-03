package httpapi

import (
	"net"
	"net/http"
	"strconv"
)

func validAuthority(authority string) bool {
	host, port, err := net.SplitHostPort(authority)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	number, err := strconv.Atoi(port)
	return ip != nil && ip.IsLoopback() && err == nil && number > 0 && number <= 65535 && strconv.Itoa(number) == port && net.JoinHostPort(host, port) == authority
}

func (h *handler) authorized(r *http.Request) bool {
	if r.Host != h.host {
		return false
	}
	if r.URL.Host != "" && r.URL.Host != h.host {
		return false
	}
	if r.URL.Scheme != "" && r.URL.Scheme != "http" {
		return false
	}
	if value, ok := singleHeader(r, RequestHeader); !ok || value != RequestHeaderValue {
		return false
	}
	if values, present := r.Header["Origin"]; present {
		if len(values) != 1 || values[0] != "http://"+h.host {
			return false
		}
	}
	if values, present := r.Header["Sec-Fetch-Site"]; present {
		if len(values) != 1 || (values[0] != "same-origin" && values[0] != "none") {
			return false
		}
	}
	// Forwarded and X-Forwarded-* are deliberately not trust inputs.
	return true
}

func singleHeader(r *http.Request, key string) (string, bool) {
	values := r.Header.Values(key)
	if len(values) != 1 {
		return "", false
	}
	return values[0], true
}
