package server

import (
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// AUDIT-P0-18: docs/swagger.json drifted 19 commits behind because four handlers
// were registered without an @Router annotation. Regenerating never surfaced it —
// swag cannot emit what nobody annotated, so the generated file looked healthy
// while silently missing endpoints, and the frontend covered for it with inline
// types.
//
// This asserts the inverse: every route the server actually registers is present
// in docs/swagger.json. A new endpoint without an annotation fails here, at the
// point the endpoint is added, rather than being noticed in an audit months on.
//
// Adding a path to swaggerUndocumentedRoutes is a deliberate act: it means "this
// is not part of the HTTP API", not "I could not be bothered".
var swaggerUndocumentedRoutes = map[string]string{
	"/swagger/*any": "the Swagger UI itself",
	"/docs":         "redirect to the Swagger UI",
}

func TestEveryRegisteredRouteIsInSwagger(t *testing.T) {
	documented := swaggerPaths(t)

	var missing []string
	seen := map[string]bool{}
	for _, route := range newRouter(Options{}, nil).Routes() {
		if _, skip := swaggerUndocumentedRoutes[route.Path]; skip {
			continue
		}
		path := swaggerPathForRoute(route.Path)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		if !documented[path] {
			missing = append(missing, path)
		}
	}
	sort.Strings(missing)

	if len(missing) > 0 {
		t.Fatalf("%d registered route(s) missing from docs/swagger.json:\n  %s\n\n"+
			"Add an @Router annotation in internal/server/swagger_annotations.go, then run:\n"+
			"  swag init -g cmd/niuma/main.go --parseInternal --parseDependency\n"+
			"  npm --prefix web run generate:api-types",
			len(missing), strings.Join(missing, "\n  "))
	}
	if len(seen) < 50 {
		t.Fatalf("only enumerated %d routes; the route table is not being read properly", len(seen))
	}
}

// The four endpoints AUDIT-P0-18 found undocumented, pinned by name so a
// regression is legible rather than just a count.
func TestPreviouslyUndocumentedEndpointsAreInSwagger(t *testing.T) {
	documented := swaggerPaths(t)
	for _, path := range []string{
		"/v1/providers",
		"/runtime/settings",
		"/tenant/agent-tasks/{id}/events/stream",
		"/mobile/chat/ws",
	} {
		if !documented[path] {
			t.Errorf("%s is missing from docs/swagger.json", path)
		}
	}
}

func TestSwaggerSessionControlEndpoints(t *testing.T) {
	documented := swaggerPathMethods(t)
	want := map[string][]string{
		"/tenant/session-control/sessions":                             {http.MethodGet, http.MethodPost},
		"/tenant/session-control/sessions/{source}/{id}":               {http.MethodGet},
		"/tenant/session-control/sessions/{source}/{id}/messages":      {http.MethodPost},
		"/tenant/session-control/sessions/{source}/{id}/compact":       {http.MethodPost},
		"/tenant/session-control/sessions/{source}/{id}/stop":          {http.MethodPost},
		"/tenant/session-control/sessions/{source}/{id}/attachments":   {http.MethodPost},
		"/tenant/session-control/sessions/{source}/{id}/monitors":      {http.MethodPost},
		"/tenant/session-control/sessions/{source}/{id}/events/stream": {http.MethodGet},
	}
	for path, methods := range want {
		for _, method := range methods {
			if !documented[path][strings.ToLower(method)] {
				t.Errorf("%s %s is missing from docs/swagger.json", method, path)
			}
		}
	}
}

func swaggerPathMethods(t *testing.T) map[string]map[string]bool {
	t.Helper()
	raw, err := os.ReadFile("../../docs/swagger.json")
	if err != nil {
		t.Fatalf("read docs/swagger.json: %v", err)
	}
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse docs/swagger.json: %v", err)
	}
	out := make(map[string]map[string]bool, len(doc.Paths))
	for path, operations := range doc.Paths {
		out[path] = make(map[string]bool, len(operations))
		for method := range operations {
			out[path][method] = true
		}
	}
	return out
}

func swaggerPaths(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile("../../docs/swagger.json")
	if err != nil {
		t.Fatalf("read docs/swagger.json: %v", err)
	}
	var doc struct {
		Paths map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse docs/swagger.json: %v", err)
	}
	paths := make(map[string]bool, len(doc.Paths))
	for path := range doc.Paths {
		paths[path] = true
	}
	return paths
}

var ginPathParam = regexp.MustCompile(`[:*]([^/]+)`)

// swaggerPathForRoute rewrites gin's ":id" parameters and "*wildcard" catch-alls
// into swagger's "{id}" / "{wildcard}".
//
// The wildcard form matters: until /webui/*path was documented, every wildcard
// route in the table was skip-listed, so a documented catch-all was reported as
// missing no matter how it was annotated.
func swaggerPathForRoute(route string) string {
	return strings.TrimSuffix(ginPathParam.ReplaceAllString(route, "{$1}"), "/")
}
