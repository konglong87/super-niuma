package server

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/observability"
)

const SettingsCurrentEnvironment = "current"
const settingsEnvironmentPrefix = "/runtime/settings/environments"

// SettingsEnvironment binds a preconfigured service to an explicit operator.
// Neither the connection nor the target identity can be supplied by an HTTP caller.
type SettingsEnvironment struct {
	ID               string
	Label            string
	Database         string
	TenantKey        string
	UserID           string
	AllowedTenantKey string
	AllowedUserID    string
	Service          TenantService
}

type SettingsEnvironmentInfo struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Database  string `json:"database"`
	TenantKey string `json:"tenant_key"`
	UserID    string `json:"user_id"`
	APIPath   string `json:"api_path"`
	Available bool   `json:"available"`
}

type SettingsEnvironmentsResponse struct {
	Environments         []SettingsEnvironmentInfo `json:"environments"`
	GlobalSettingsPath   string                    `json:"global_settings_path"`
	GlobalSettingsShared bool                      `json:"global_settings_shared"`
}

func registerAgentProfileRoutes(router *gin.Engine, opts Options) {
	router.Any("/tenant/agent-profiles", gin.WrapF(tenantAgentProfilesHandler(opts)))
	router.Any("/tenant/agent-profiles/:key", gin.WrapF(tenantAgentProfilesHandler(opts)))
	router.Any("/tenant/agent-profiles/:key/validate", gin.WrapF(tenantAgentProfileValidateHandler(opts)))
	router.Any("/tenant/agent-profiles/:key/effective", gin.WrapF(tenantAgentProfileEffectiveHandler(opts)))
	router.Any("/tenant/agent-profiles/:key/publish", gin.WrapF(tenantAgentProfilePublishHandler(opts)))
	router.Any("/tenant/agent-profiles/:key/archive", gin.WrapF(tenantAgentProfileArchiveHandler(opts)))
	router.Any("/tenant/agent-profiles/:key/rollback", gin.WrapF(tenantAgentProfileRollbackHandler(opts)))
	router.Any("/tenant/agent-profiles/:key/bot-binding", gin.WrapF(tenantAgentProfileBindingHandler(opts)))
	router.Any("/tenant/agent-profiles/:key/conversations", gin.WrapF(tenantAgentProfileConversationsHandler(opts)))
	router.Any("/tenant/agent-profile-assignment", gin.WrapF(tenantAgentProfileAssignmentHandler(opts)))
}

func (environment SettingsEnvironment) permits(r *http.Request) bool {
	return environment.AllowedTenantKey != "" && environment.AllowedUserID != "" &&
		requestTenantKey(r) == environment.AllowedTenantKey && requestUserID(r) == environment.AllowedUserID
}

func registerSettingsEnvironmentRoutes(router *gin.Engine, opts Options) {
	router.GET(settingsEnvironmentPrefix, func(c *gin.Context) {
		if !authorize(c.Writer, c.Request, opts.AuthToken) {
			return
		}
		path, err := config.GlobalSettingsPath()
		if err != nil {
			writeTenantError(c.Writer, http.StatusInternalServerError, "cannot locate global settings")
			return
		}
		items := []SettingsEnvironmentInfo{{ID: SettingsCurrentEnvironment, Label: "Web 对话", Database: opts.SettingsDatabase, TenantKey: requestTenantKey(c.Request), UserID: requestUserID(c.Request), Available: true}}
		for _, environment := range opts.SettingsEnvironments {
			if !environment.permits(c.Request) {
				continue
			}
			if !requireTenantRole(c.Writer, c.Request, opts, "owner", "admin") {
				return
			}
			items = append(items, SettingsEnvironmentInfo{ID: environment.ID, Label: environment.Label, Database: environment.Database, TenantKey: environment.TenantKey, UserID: environment.UserID, APIPath: settingsEnvironmentPrefix + "/" + environment.ID, Available: environment.Service != nil})
		}
		c.Header("Cache-Control", "no-store")
		writeJSON(c.Writer, SettingsEnvironmentsResponse{Environments: items, GlobalSettingsPath: path, GlobalSettingsShared: len(items) > 1})
	})

	// Only the settings control plane is mounted. Chat execution, channel worker
	// lifecycle, arbitrary SQL and global identity mutation have no route here.
	targets := make(map[string]http.Handler)
	for _, environment := range opts.SettingsEnvironments {
		target := gin.New()
		target.RedirectTrailingSlash, target.RedirectFixedPath = false, false
		targetOpts := Options{AuthToken: opts.AuthToken, TenantService: environment.Service}
		registerAgentProfileRoutes(target, targetOpts)
		target.GET("/tenant/channel-accounts", gin.WrapF(tenantChannelAccountsHandler(targetOpts)))
		target.GET("/tenant/messages", gin.WrapF(tenantMessagesHandler(targetOpts)))
		targets[environment.ID] = target
	}
	router.Any(settingsEnvironmentPrefix+"/:environment/*resource", func(c *gin.Context) {
		if !authorize(c.Writer, c.Request, opts.AuthToken) {
			return
		}
		var selected *SettingsEnvironment
		for i := range opts.SettingsEnvironments {
			candidate := &opts.SettingsEnvironments[i]
			if candidate.ID == c.Param("environment") && candidate.permits(c.Request) {
				selected = candidate
				break
			}
		}
		if selected == nil {
			writeTenantError(c.Writer, http.StatusNotFound, "settings environment not found")
			return
		}
		if !requireTenantRole(c.Writer, c.Request, opts, "owner", "admin") {
			return
		}
		if selected.Service == nil {
			writeTenantError(c.Writer, http.StatusServiceUnavailable, "settings environment is unavailable")
			return
		}
		resource := c.Param("resource")
		for _, segment := range strings.Split(resource, "/") {
			if segment == "." || segment == ".." || strings.ContainsAny(segment, "\\%") {
				writeTenantError(c.Writer, http.StatusBadRequest, "invalid settings resource")
				return
			}
		}
		request := c.Request.Clone(observability.WithRequestValues(c.Request.Context(), observability.TraceID(c.Request.Context()), selected.UserID, selected.TenantKey))
		request.URL.Path, request.URL.RawPath = resource, ""
		request.Header.Set("X-Tenant-Key", selected.TenantKey)
		request.Header.Set("X-User-Id", selected.UserID)
		targets[selected.ID].ServeHTTP(c.Writer, request)
		if c.Request.Method != http.MethodGet && c.Writer.Status() < http.StatusBadRequest {
			recordTenantAudit(c.Request.Context(), opts.TenantService, "settings.environment.write", "settings_environment", selected.ID+resource)
		}
	})
}
