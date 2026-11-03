package httpapi

import (
	"net/http"
	"strings"

	"github.com/konglong87/super-niuma/internal/super/workorder"
)

type routeKind uint8

const (
	routeUnknown routeKind = iota
	routeStatus
	routeCollection
	routeOrder
	routeCancel
)

type route struct {
	kind  routeKind
	id    string
	allow string
}

func matchRoute(r *http.Request) route {
	path := r.URL.Path
	// No redirects, cleaning, encoded aliases, extra segments, or trailing slashes.
	if r.URL.RawPath != "" || r.URL.EscapedPath() != path {
		return route{}
	}
	switch path {
	case statusPath:
		return route{kind: routeStatus, allow: http.MethodGet}
	case collectionPath:
		return route{kind: routeCollection, allow: "GET, POST"}
	}
	rest, ok := strings.CutPrefix(path, collectionPath+"/")
	if !ok {
		return route{}
	}
	parts := strings.Split(rest, "/")
	if parts[0] == "" || parts[0] == "." || parts[0] == ".." {
		return route{}
	}
	if len(parts) == 1 {
		return route{kind: routeOrder, id: parts[0], allow: http.MethodGet}
	}
	if len(parts) == 2 && parts[1] == "cancel" {
		return route{kind: routeCancel, id: parts[0], allow: http.MethodPost}
	}
	return route{}
}

func (h *handler) dispatch(w http.ResponseWriter, r *http.Request) response {
	route := matchRoute(r)
	if route.kind == routeUnknown {
		return failure(codeNotFound)
	}
	if !methodAllowed(route, r.Method) {
		result := failure(codeMethod)
		result.allow = route.allow
		return result
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		return failure(codeInvalidRequest)
	}
	if r.Method == http.MethodPost {
		return h.mutate(w, r, route)
	}
	body, code := readBody(w, r)
	if code != "" {
		return failure(code)
	}
	if len(body) != 0 {
		return failure(codeInvalidRequest)
	}
	return h.read(r, route)
}

func methodAllowed(route route, method string) bool {
	if route.kind == routeCollection {
		return method == http.MethodGet || method == http.MethodPost
	}
	return method == route.allow
}

func (h *handler) read(r *http.Request, route route) response {
	switch route.kind {
	case routeStatus:
		return response{status: http.StatusOK, body: struct {
			Name              string `json:"name"`
			Mode              string `json:"mode"`
			ExecutionEnabled  bool   `json:"execution_enabled"`
			LLMRoutingEnabled bool   `json:"llm_routing_enabled"`
		}{Name: applicationName, Mode: intakeMode}}
	case routeCollection:
		orders, err := h.service.List(r.Context())
		if err != nil {
			return serviceFailure(err)
		}
		return response{status: http.StatusOK, body: struct {
			Items []workorder.WorkOrder `json:"items"`
		}{Items: orders}}
	default:
		order, err := h.service.Get(r.Context(), route.id)
		if err != nil {
			return serviceFailure(err)
		}
		return response{status: http.StatusOK, body: order}
	}
}

func (h *handler) mutate(w http.ResponseWriter, r *http.Request, route route) response {
	if !jsonContentType(r) {
		return failure(codeMedia)
	}
	body, code := readBody(w, r)
	if code != "" {
		return failure(code)
	}
	fields, ok := decodeObject(body)
	if !ok {
		return failure(codeInvalidRequest)
	}
	if route.kind == routeCancel {
		if len(fields) != 0 {
			return failure(codeInvalidRequest)
		}
		order, err := h.service.Cancel(r.Context(), route.id)
		if err != nil {
			return serviceFailure(err)
		}
		return response{status: http.StatusOK, body: order}
	}
	input, ok := decodeInput(fields)
	if !ok {
		return failure(codeInvalidRequest)
	}
	key, ok := singleHeader(r, IdempotencyHeader)
	if !ok {
		return failure(codeInvalidInput)
	}
	order, created, err := h.service.Create(r.Context(), key, input)
	if err != nil {
		return serviceFailure(err)
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	return response{status: status, body: order}
}
