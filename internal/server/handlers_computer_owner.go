package server

import (
	"net/http"

	cu "github.com/konglong87/super-niuma/internal/computeruse"
	"github.com/konglong87/super-niuma/internal/sessioncontrol"
)

// This endpoint resolves identity only. It cannot create/approve a computer
// session; that authority stays with the native host's user approval surface.
func tenantComputerOwnerHandler(opts Options) http.HandlerFunc {
	authorized := func(w http.ResponseWriter, r *http.Request) {
		if !authorizeHeaderToken(r, opts.AuthToken) {
			writeSessionControlError(w, http.StatusUnauthorized, sessionControlCodeUnauthorized, "unauthorized")
			return
		}
		// Actor is resolved from the desktop's configured identity during server
		// startup, never from browser-supplied headers, JSON, or numeric IDs.
		if opts.SessionControl == nil || opts.DesktopComputerOwnerResolver == nil {
			writeSessionControlError(w, http.StatusServiceUnavailable, sessionControlCodeServiceUnavailable, "desktop identity is unavailable")
			return
		}
		bound, actor, err := opts.DesktopComputerOwnerResolver(r.Context())
		if err != nil || bound == nil || actor.TenantID == 0 || actor.UserID == 0 {
			writeSessionControlError(w, http.StatusServiceUnavailable, sessionControlCodeServiceUnavailable, "desktop identity is unavailable")
			return
		}
		ref, ok := sessionControlRefFromRequest(w, r)
		if !ok {
			return
		}
		if ref.Source != sessioncontrol.SourceTenant {
			writeSessionControlError(w, http.StatusForbidden, string(sessioncontrol.CodeForbidden), "computer use requires a managed conversation")
			return
		}
		snapshot, err := opts.SessionControl.Get(bound, sessioncontrol.GetRequest{Context: actor, Ref: ref})
		if err != nil {
			writeSessionControlServiceError(w, err)
			return
		}
		if actor.TenantID == 0 || actor.UserID == 0 || snapshot.ID == 0 || snapshot.Ref != ref || snapshot.ReadOnly || snapshot.Status == sessioncontrol.StatusArchived {
			writeSessionControlError(w, http.StatusForbidden, string(sessioncontrol.CodeForbidden), "conversation cannot authorize computer use")
			return
		}
		writeJSON(w, sessionControlDataEnvelope[cu.SessionOwner]{Data: cu.SessionOwner{TenantID: actor.TenantID, UserID: actor.UserID, SessionID: snapshot.ID}})
	}
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !opts.DesktopComputerOwnerLookup || opts.AuthToken == "" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet || r.URL.RawQuery != "" || r.Header.Get("Origin") != "" {
			writeSessionControlError(w, http.StatusForbidden, string(sessioncontrol.CodeForbidden), "native desktop request required")
			return
		}
		authorized(w, r)
	}
}
