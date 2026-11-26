package main

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func withExternalURLTestState(t *testing.T, hosts ...string) *[]string {
	t.Helper()
	externalURLHostsMu.Lock()
	saved := externalURLHosts
	externalURLHosts = map[string]struct{}{}
	externalURLHostsMu.Unlock()
	RegisterExternalURLHosts(hosts...)
	var opened []string
	savedOpen := openExternalBrowser
	openExternalBrowser = func(_ context.Context, u string) { opened = append(opened, u) }
	t.Cleanup(func() {
		openExternalBrowser = savedOpen
		externalURLHostsMu.Lock()
		externalURLHosts = saved
		externalURLHostsMu.Unlock()
	})
	return &opened
}

func TestOpenExternalURL(t *testing.T) {
	opened := withExternalURLTestState(t, "Example.com")
	application := &app{windowCtx: context.Background()}

	cases := []struct {
		name string
		url  string
		want error
	}{
		{"http rejected", "http://example.com/signup", errExternalURLNotHTTPS},
		{"other scheme rejected", "javascript://example.com/%0aalert(1)", errExternalURLNotHTTPS},
		{"host not allowed", "https://evil.test/signup", errExternalURLHostNotAllowed},
		{"subdomain not allowed", "https://a.example.com/", errExternalURLHostNotAllowed},
		{"userinfo rejected", "https://user@example.com/", errExternalURLInvalid},
		{"malformed", "https://exa mple.com/%zz", errExternalURLInvalid},
		{"no host", "https:///path", errExternalURLInvalid},
		{"empty", "", errExternalURLInvalid},
	}
	for _, tc := range cases {
		if err := application.OpenExternalURL(tc.url); !errors.Is(err, tc.want) {
			t.Errorf("%s: error = %v, want %v", tc.name, err, tc.want)
		}
	}
	if len(*opened) != 0 {
		t.Fatalf("rejected urls were opened: %v", *opened)
	}

	if err := application.OpenExternalURL(" https://EXAMPLE.com:8443/signup?a=1 "); err != nil {
		t.Fatalf("allowed url: %v", err)
	}
	if want := []string{"https://EXAMPLE.com:8443/signup?a=1"}; !reflect.DeepEqual(*opened, want) {
		t.Fatalf("opened = %v, want %v", *opened, want)
	}
}

func TestOpenExternalURLEmptyAllowlistAndNoWindow(t *testing.T) {
	withExternalURLTestState(t)
	if err := (&app{windowCtx: context.Background()}).OpenExternalURL("https://example.com/"); !errors.Is(err, errExternalURLHostNotAllowed) {
		t.Fatalf("empty allowlist: %v", err)
	}
	RegisterExternalURLHosts("example.com")
	if err := (&app{}).OpenExternalURL("https://example.com/"); !errors.Is(err, errExternalURLUnavailable) {
		t.Fatalf("no window: %v", err)
	}
}
