package computeracceptance

import (
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
)

// TokenHeader authenticates reporting and HTTP snapshot reads. No CORS access
// or cookie authentication is provided; Go harnesses can call Snapshot directly.
const TokenHeader = "X-Fixture-Token"

//go:embed assets/index.html assets/fixture.css assets/fixture.js
var assets embed.FS

func (f *Fixture) serveHTTP(w http.ResponseWriter, r *http.Request) {
	setHeaders(w)
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || !net.ParseIP(host).IsLoopback() || r.Host != f.host {
		http.Error(w, "loopback host required", http.StatusForbidden)
		return
	}
	if r.URL.Path == "/events" || r.URL.Path == "/snapshot" || r.URL.Path == "/config" {
		if !f.authorized(w, r) {
			return
		}
		f.serveAPI(w, r)
		return
	}
	serveAsset(w, r)
}

func setHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'")
}

func (f *Fixture) authorized(w http.ResponseWriter, r *http.Request) bool {
	if subtle.ConstantTimeCompare([]byte(r.Header.Get(TokenHeader)), []byte(f.token)) != 1 {
		http.Error(w, "invalid fixture token", http.StatusForbidden)
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+f.host {
		http.Error(w, "cross-origin request denied", http.StatusForbidden)
		return false
	}
	return true
}

func (f *Fixture) serveAPI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/events" {
		if allowMethod(w, r, http.MethodPost) {
			f.receive(w, r)
		}
		return
	}
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/snapshot" {
		_ = json.NewEncoder(w).Encode(f.Snapshot())
		return
	}
	_ = json.NewEncoder(w).Encode(struct {
		MaxBatchEvents  int `json:"maxBatchEvents"`
		MaxTextBytes    int `json:"maxTextBytes"`
		MaxQueueEvents  int `json:"maxQueueEvents"`
		MaxRequestBytes int `json:"maxRequestBytes"`
		MaxLabelBytes   int `json:"maxLabelBytes"`
	}{MaxBatchEvents, MaxTextBytes, MaxEvents, MaxRequestBytes, maxLabelBytes})
}

func serveAsset(w http.ResponseWriter, r *http.Request) {
	name, contentType := "", ""
	switch r.URL.Path {
	case "/":
		name, contentType = "index.html", "text/html; charset=utf-8"
	case "/fixture.css":
		name, contentType = "fixture.css", "text/css; charset=utf-8"
	case "/fixture.js":
		name, contentType = "fixture.js", "text/javascript; charset=utf-8"
	default:
		http.NotFound(w, r)
		return
	}
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	data, err := assets.ReadFile("assets/" + name)
	if err != nil {
		http.Error(w, "missing embedded asset", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", contentType)
	_, _ = w.Write(data)
}

func allowMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}
	w.Header().Set("Allow", method)
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	return false
}

func (f *Fixture) receive(w http.ResponseWriter, r *http.Request) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" || r.Header.Get("Content-Encoding") != "" {
		http.Error(w, "uncompressed application/json required", http.StatusUnsupportedMediaType)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxRequestBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "report too large", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "unreadable report", http.StatusBadRequest)
		}
		return
	}
	report, err := decodeReport(body)
	if err != nil {
		http.Error(w, "invalid report: "+err.Error(), http.StatusBadRequest)
		return
	}
	f.record(report)
	w.WriteHeader(http.StatusNoContent)
}
