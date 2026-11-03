package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

func TestWebUIStaticHandlerServesIndexAndRequiresAuth(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>webui</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "app.js"), []byte("console.log('webui')"), 0o644); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(Options{AuthToken: "token", WebUIDir: dir}, nil)

	req := httptest.NewRequest(http.MethodGet, "/webui/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/webui/?token=token", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "webui") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 || cookies[0].Name != webUIAuthCookie || cookies[0].Path != "/webui/" {
		t.Fatalf("missing scoped auth cookie: %+v", cookies)
	}

	req = httptest.NewRequest(http.MethodGet, "/webui/assets/app.js", nil)
	req.AddCookie(cookies[0])
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "webui") {
		t.Fatalf("asset status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// The /webui route must never answer a bare 404: a user who has not built the
// frontend has no way to tell "you forgot to run npm run build" apart from
// "this build has no web UI". Both here and in the stale-directory case below
// the response has to name the fix.
func TestWebUIWithoutDirServesActionableSetupPage(t *testing.T) {
	handler := NewHandler(Options{}, nil)

	req := httptest.NewRequest(http.MethodGet, "/webui/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code == http.StatusNotFound {
		t.Fatalf("/webui/ answered a silent 404 with no WebUIDir configured")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"npm", "run build", webUIDirPrimaryEnv} {
		if !strings.Contains(body, want) {
			t.Fatalf("setup page missing actionable hint %q: %s", want, body)
		}
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "text/html") {
		t.Fatalf("content-type=%q", got)
	}
}

// A configured-but-empty WebUIDir used to fall through to http.ServeFile on a
// missing index.html, which is the same silent 404 wearing a different hat.
func TestWebUIStaleDirServesSetupPage(t *testing.T) {
	handler := NewHandler(Options{WebUIDir: t.TempDir()}, nil)

	req := httptest.NewRequest(http.MethodGet, "/webui/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "run build") {
		t.Fatalf("stale dir did not explain the fix: %s", rec.Body.String())
	}
}

// The setup page is a diagnostic, so it stays behind the same auth gate as the
// real UI rather than becoming an unauthenticated information leak.
func TestWebUISetupPageRequiresAuth(t *testing.T) {
	handler := NewHandler(Options{AuthToken: "token"}, nil)

	req := httptest.NewRequest(http.MethodGet, "/webui/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// Building the frontend is enough on its own: a plain `npm run build` leaves
// web/dist next to the working directory, so the env var is an override rather
// than a requirement.
func TestWebUIAutoDiscoversBuiltDist(t *testing.T) {
	root := t.TempDir()
	dist := filepath.Join(root, "web", "dist")
	if err := os.MkdirAll(dist, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "index.html"), []byte("<html>discovered webui</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)

	handler := NewHandler(Options{}, nil)
	req := httptest.NewRequest(http.MethodGet, "/webui/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "discovered webui") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// An explicit, usable WebUIDir always wins over whatever happens to sit in the
// working directory.
func TestWebUIExplicitDirWinsOverDiscovery(t *testing.T) {
	root := t.TempDir()
	dist := filepath.Join(root, "web", "dist")
	if err := os.MkdirAll(dist, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "index.html"), []byte("<html>discovered webui</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)

	explicit := t.TempDir()
	if err := os.WriteFile(filepath.Join(explicit, "index.html"), []byte("<html>explicit webui</html>"), 0o644); err != nil {
		t.Fatal(err)
	}

	handler := NewHandler(Options{WebUIDir: explicit}, nil)
	req := httptest.NewRequest(http.MethodGet, "/webui/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !strings.Contains(rec.Body.String(), "explicit webui") {
		t.Fatalf("explicit dir lost to discovery: %s", rec.Body.String())
	}
}

func TestWebUIAPIPrefixCompatRequiresStaticWebUI(t *testing.T) {
	service := &fakeTenantService{
		tenants: []mysqlstore.Tenant{{TenantKey: "webui-local", Name: "WebUI Local", Status: "active"}},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/tenant/tenants?limit=1", nil)
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("X-Tenant-Key", "webui-local")
	req.Header.Set("X-User-Id", "webui-local-user")

	rec := httptest.NewRecorder()
	NewHandler(Options{AuthToken: "token", TenantService: service}, nil).ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected /api prefix to stay unavailable without static webui, status=%d body=%s", rec.Code, rec.Body.String())
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>webui</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/tenant/tenants?limit=1", nil)
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("X-Tenant-Key", "webui-local")
	req.Header.Set("X-User-Id", "webui-local-user")
	rec = httptest.NewRecorder()
	NewHandler(Options{AuthToken: "token", WebUIDir: dir, TenantService: service}, nil).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "webui-local") {
		t.Fatalf("expected /api prefix compat to reach tenant handler, status=%d body=%s", rec.Code, rec.Body.String())
	}
}
