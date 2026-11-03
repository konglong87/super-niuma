package server

import (
	_ "embed"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const webUIAuthCookie = "go_claude_webui_token"

// webUIDirPrimaryEnv is the documented override for the frontend build
// directory. It is referenced by the setup page and its test so the two cannot
// drift apart.
const webUIDirPrimaryEnv = "NIUMA_WEBUI_DIR"

// webUIUnavailableHTML explains how to build the frontend. The Vite bundle
// itself is deliberately not embedded (see resolveWebUIDir), but this page is,
// so /webui always has something honest to say.
//
//go:embed webui_unavailable.html
var webUIUnavailableHTML string

func registerWebUIRoutes(router *gin.Engine, opts Options) {
	// Registered unconditionally. Skipping registration when no build is
	// available produced a bare 404 that no user could tell apart from "this
	// binary has no web UI", so the unbuilt case gets an explicit page instead.
	dir := resolveWebUIDir(opts.WebUIDir)
	router.GET("/webui", gin.WrapF(webUIRedirectHandler(opts.AuthToken)))
	router.GET("/webui/*path", gin.WrapF(webUIStaticHandler(opts.AuthToken, dir)))
}

// resolveWebUIDir picks the frontend build directory: the explicit option wins,
// otherwise a build sitting next to the working directory or the executable is
// adopted so that `npm --prefix web run build` alone is enough. Returns "" when
// no usable build exists, which makes the handlers serve the setup page.
//
// Discovery deliberately does not walk up the tree: it keeps the lookup
// predictable, and it keeps `go test ./internal/server` from finding a real
// web/dist in the repository root and going non-deterministic.
func resolveWebUIDir(configured string) string {
	if dir := strings.TrimSpace(configured); dir != "" {
		// A configured-but-unusable directory still falls through to discovery
		// and then to the setup page rather than serving 404s for every asset.
		if webUIDirUsable(dir) {
			return dir
		}
	}
	var candidates []string
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(cwd, "web", "dist"), filepath.Join(cwd, "dist"))
	}
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		candidates = append(candidates, filepath.Join(exeDir, "web", "dist"), filepath.Join(exeDir, "dist"))
	}
	for _, candidate := range candidates {
		if webUIDirUsable(candidate) {
			return candidate
		}
	}
	return ""
}

// webUIDirUsable reports whether dir is a directory holding an index.html. A
// directory without one cannot boot the single-page app, so it is treated as
// absent.
func webUIDirUsable(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, "index.html"))
	return err == nil && info.Mode().IsRegular()
}

// webUIUnavailableHandler reports that the frontend needs building. It answers
// 503 rather than 200 so probes and scripts see an honest "not serving yet",
// while browsers still render the instructions.
func webUIUnavailableHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusServiceUnavailable)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write([]byte(webUIUnavailableHTML))
}

func webUIRedirectHandler(authToken string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authorizeWebUI(w, r, authToken) {
			return
		}
		http.Redirect(w, r, "/webui/", http.StatusFound)
	}
}

func webUIStaticHandler(authToken, dir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authorizeWebUI(w, r, authToken) {
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if dir == "" {
			webUIUnavailableHandler(w, r)
			return
		}
		requestPath := strings.TrimPrefix(r.URL.Path, "/webui/")
		if requestPath == "" {
			requestPath = "index.html"
		}
		requestPath = strings.TrimPrefix(filepath.Clean("/"+requestPath), "/")
		fullPath := filepath.Join(dir, requestPath)
		if info, err := os.Stat(fullPath); err != nil || info.IsDir() {
			http.ServeFile(w, r, filepath.Join(dir, "index.html"))
			return
		}
		http.ServeFile(w, r, fullPath)
	}
}

// authorizeWebUI 守的是 /webui 静态资源。这里保留 `?token=` → Cookie 的流程：
// 浏览器加载 SPA bundle 时没有地方能塞 header，而这条路径换到的只有前端构建产物。
// Cookie 的 Path 锁在 /webui/，浏览器不会把它带到 /api/*，且 authorize 根本不读
// Cookie —— 所以 WebUI 的 token 交换拿不到任何会话内容（AUDIT-P1-21）。
func authorizeWebUI(w http.ResponseWriter, r *http.Request, token string) bool {
	if strings.TrimSpace(token) == "" {
		return true
	}
	if authorizeHeaderToken(r, token) || webUICookieToken(r, token) {
		return true
	}
	if secureTokenEqual(strings.TrimSpace(r.URL.Query().Get("token")), token) {
		http.SetCookie(w, &http.Cookie{
			Name:     webUIAuthCookie,
			Value:    token,
			Path:     "/webui/",
			Expires:  time.Now().Add(12 * time.Hour),
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
		return true
	}
	http.Error(w, "unauthorized", http.StatusUnauthorized)
	return false
}

func webUICookieToken(r *http.Request, token string) bool {
	cookie, err := r.Cookie(webUIAuthCookie)
	return err == nil && secureTokenEqual(strings.TrimSpace(cookie.Value), token)
}

func webUIAPIPrefixHandler(next http.Handler, webUIDir string) http.Handler {
	if strings.TrimSpace(webUIDir) == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			r = r.Clone(r.Context())
			r.URL.Path = strings.TrimPrefix(r.URL.Path, "/api")
			if r.URL.RawPath != "" {
				r.URL.RawPath = strings.TrimPrefix(r.URL.RawPath, "/api")
			}
		}
		next.ServeHTTP(w, r)
	})
}
