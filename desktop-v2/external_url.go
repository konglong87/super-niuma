package main

import (
	"errors"
	"net/url"
	"strings"
	"sync"

	"github.com/konglong87/super-niuma/internal/super/freemodels"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

var (
	errExternalURLInvalid        = errors.New("external url is invalid")
	errExternalURLNotHTTPS       = errors.New("external url must use https")
	errExternalURLHostNotAllowed = errors.New("external url host is not allowed")
	errExternalURLUnavailable    = errors.New("external url opener is unavailable")
)

var (
	externalURLHostsMu sync.RWMutex
	externalURLHosts   = map[string]struct{}{}

	// openExternalBrowser is replaced in tests.
	openExternalBrowser = wailsruntime.BrowserOpenURL
)

// RegisterExternalURLHosts adds hosts (lower-cased, no port) that
// OpenExternalURL may open. Embedded provider catalogs call this to register
// the official sign-up hosts.
func RegisterExternalURLHosts(hosts ...string) {
	externalURLHostsMu.Lock()
	defer externalURLHostsMu.Unlock()
	for _, host := range hosts {
		if host = strings.ToLower(strings.TrimSpace(host)); host != "" {
			externalURLHosts[host] = struct{}{}
		}
	}
}

// The embedded free model catalog is validated by its own tests, so a load
// error here only leaves the allowlist empty: nothing opens rather than
// anything opening.
func init() {
	hosts, err := freemodels.SignupHosts()
	if err != nil {
		return
	}
	for host := range hosts {
		RegisterExternalURLHosts(host)
	}
}

func externalURLHostAllowed(host string) bool {
	externalURLHostsMu.RLock()
	defer externalURLHostsMu.RUnlock()
	_, ok := externalURLHosts[strings.ToLower(host)]
	return ok
}

func validateExternalURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.Hostname() == "" {
		return "", errExternalURLInvalid
	}
	if !strings.EqualFold(parsed.Scheme, "https") {
		return "", errExternalURLNotHTTPS
	}
	if parsed.User != nil {
		return "", errExternalURLInvalid
	}
	if !externalURLHostAllowed(parsed.Hostname()) {
		return "", errExternalURLHostNotAllowed
	}
	return parsed.String(), nil
}

// OpenExternalURL opens an allowlisted https URL in the system browser.
func (a *app) OpenExternalURL(rawURL string) error {
	target, err := validateExternalURL(rawURL)
	if err != nil {
		return err
	}
	ctx := a.windowContext()
	if ctx == nil {
		return errExternalURLUnavailable
	}
	openExternalBrowser(ctx, target)
	return nil
}
