package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/konglong87/super-niuma/internal/prompttemplate"
)

type promptTemplateRequest struct {
	ID        uint64 `json:"id"`
	Title     string `json:"title"`
	Content   string `json:"content"`
	Category  string `json:"category"`
	Pinned    bool   `json:"pinned"`
	SortOrder int    `json:"sort_order"`
}

func promptTemplatesHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if opts.PromptTemplateService == nil || opts.TenantService == nil {
			http.Error(w, "prompt templates unavailable", http.StatusNotImplemented)
			return
		}
		if !authorize(w, r, opts.AuthToken) {
			return
		}
		ctx, err := opts.TenantService.ResolveContext(r.Context())
		if err != nil {
			http.Error(w, "tenant context unavailable", http.StatusForbidden)
			return
		}
		switch r.Method {
		case http.MethodGet:
			items, err := opts.PromptTemplateService.List(r.Context(), prompttemplate.ListOptions{TenantID: ctx.TenantID, UserID: ctx.UserID, Search: r.URL.Query().Get("search"), Category: r.URL.Query().Get("category"), Limit: promptTemplateLimit(r, 100)})
			if err != nil {
				writePromptTemplateError(w, err)
				return
			}
			writeJSON(w, items)
		case http.MethodPost, http.MethodPatch:
			var req promptTemplateRequest
			if json.NewDecoder(r.Body).Decode(&req) != nil {
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			if r.Method == http.MethodPatch && req.ID == 0 {
				writePromptTemplateError(w, prompttemplate.ErrInvalidInput)
				return
			}
			item, err := opts.PromptTemplateService.Save(r.Context(), prompttemplate.Input{ID: req.ID, TenantID: ctx.TenantID, UserID: ctx.UserID, Title: req.Title, Content: req.Content, Category: req.Category, Pinned: req.Pinned, SortOrder: req.SortOrder})
			if err != nil {
				writePromptTemplateError(w, err)
				return
			}
			writeJSON(w, item)
		case http.MethodDelete:
			id, _ := strconv.ParseUint(strings.TrimPrefix(r.URL.Path, "/tenant/prompt-templates/"), 10, 64)
			if err := opts.PromptTemplateService.Delete(r.Context(), ctx.TenantID, ctx.UserID, id); err != nil {
				writePromptTemplateError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func promptTemplateLimit(r *http.Request, fallback int) int {
	value, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil {
		return fallback
	}
	return value
}

func writePromptTemplateError(w http.ResponseWriter, err error) {
	status, message := http.StatusInternalServerError, "prompt template operation failed"
	switch {
	case errors.Is(err, prompttemplate.ErrInvalidInput):
		status, message = http.StatusBadRequest, prompttemplate.ErrInvalidInput.Error()
	case errors.Is(err, prompttemplate.ErrNotFound):
		status, message = http.StatusNotFound, prompttemplate.ErrNotFound.Error()
	case errors.Is(err, prompttemplate.ErrConflict):
		status, message = http.StatusConflict, prompttemplate.ErrConflict.Error()
	}
	http.Error(w, message, status)
}
