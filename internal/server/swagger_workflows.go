package server

import (
	"github.com/konglong87/super-niuma/internal/agentprofile"
	"github.com/konglong87/super-niuma/internal/agentteam/workflow"
	"github.com/konglong87/super-niuma/internal/agentteam/workflowapp"
)

// These response contracts document the existing authenticated Workflow facade.
// They intentionally contain no provider credentials or hidden reasoning.
type SwaggerWorkflowTypes struct {
	Roles          []agentprofile.RoleTypeDescriptor      `json:"roles"`
	ExecutionKinds []agentprofile.ExecutionKindDescriptor `json:"execution_kinds"`
}
type SwaggerWorkflowTemplates struct {
	Items []workflowapp.Template `json:"items"`
}
type SwaggerWorkflowBuiltins struct {
	Items []workflowapp.Builtin `json:"items"`
}
type SwaggerWorkflowTemplate struct {
	Template workflowapp.Template `json:"template"`
}
type SwaggerWorkflowRuns struct {
	Items []workflow.Run `json:"items"`
}
type SwaggerWorkflowRun struct {
	Run      workflow.Run `json:"run"`
	Replayed bool         `json:"replayed,omitempty"`
}
type SwaggerWorkflowEvents struct {
	Events       []workflow.Event `json:"events"`
	HasMore      bool             `json:"has_more"`
	LastSequence uint64           `json:"last_sequence"`
}
type SwaggerWorkflowRecovery struct {
	Revision uint64 `json:"revision"`
}

// @Summary List extensible Agent role and execution kind metadata
// @Description Workflow V2 uses private Profile/Team resources. Read-only deployment disables mutations; text Agents have no tools.
// @Tags Team Workflows
// @Security ApiKeyAuth
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User identity"
// @Produce json
// @Success 200 {object} SwaggerWorkflowTypes
// @Failure 400,401,403,404,409,503 {object} SwaggerError
// @Router /tenant/agent-types [get]
func swaggerWorkflowTypes() {}

// @Summary List private Team workflow versions
// @Description Workflow V2 uses private Profile/Team resources. Read-only deployment disables mutations; text Agents have no tools.
// @Tags Team Workflows
// @Security ApiKeyAuth
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User identity"
// @Produce json
// @Success 200 {object} SwaggerWorkflowTemplates
// @Failure 400,401,403,404,409,503 {object} SwaggerError
// @Router /tenant/workflows [get]
func swaggerWorkflowList() {}

// @Summary Save a new immutable Team workflow version
// @Description Workflow V2 uses private Profile/Team resources. Read-only deployment disables mutations; text Agents have no tools.
// @Tags Team Workflows
// @Security ApiKeyAuth
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User identity"
// @Produce json
// @Accept json
// @Param request body workflowapp.SaveRequest true "Workflow with published Profile bindings"
// @Success 200 {object} SwaggerWorkflowTemplate
// @Failure 400,401,403,404,409,503 {object} SwaggerError
// @Router /tenant/workflows [post]
func swaggerWorkflowSave() {}

// @Summary List beginner Team workflow starting points
// @Description Workflow V2 uses private Profile/Team resources. Read-only deployment disables mutations; text Agents have no tools.
// @Tags Team Workflows
// @Security ApiKeyAuth
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User identity"
// @Produce json
// @Success 200 {object} SwaggerWorkflowBuiltins
// @Failure 400,401,403,404,409,503 {object} SwaggerError
// @Router /tenant/workflows/templates [get]
func swaggerWorkflowBuiltins() {}

// @Summary Create a personal Team and Agents from a starting point
// @Description Workflow V2 uses private Profile/Team resources. Read-only deployment disables mutations; text Agents have no tools.
// @Tags Team Workflows
// @Security ApiKeyAuth
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User identity"
// @Produce json
// @Param key path string true "Built-in template key"
// @Success 200 {object} SwaggerWorkflowTemplate
// @Failure 400,401,403,404,409,503 {object} SwaggerError
// @Router /tenant/workflows/templates/{key}/create [post]
func swaggerWorkflowCreateBuiltin() {}

// @Summary Get a Team workflow version
// @Description Workflow V2 uses private Profile/Team resources. Read-only deployment disables mutations; text Agents have no tools.
// @Tags Team Workflows
// @Security ApiKeyAuth
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User identity"
// @Produce json
// @Param key path string true "Team key"
// @Param version query int false "Fixed version; omit for latest"
// @Success 200 {object} SwaggerWorkflowTemplate
// @Failure 400,401,403,404,409,503 {object} SwaggerError
// @Router /tenant/workflows/{key} [get]
func swaggerWorkflowGet() {}

// @Summary List persisted runs for a Team
// @Description Workflow V2 uses private Profile/Team resources. Read-only deployment disables mutations; text Agents have no tools.
// @Tags Team Workflows
// @Security ApiKeyAuth
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User identity"
// @Produce json
// @Param key path string true "Team key"
// @Param version query int false "Fixed Team version"
// @Success 200 {object} SwaggerWorkflowRuns
// @Failure 400,401,403,404,409,503 {object} SwaggerError
// @Router /tenant/workflows/{key}/runs [get]
func swaggerWorkflowRuns() {}

// @Summary Queue an idempotent bounded Team run
// @Description Workflow V2 uses private Profile/Team resources. Read-only deployment disables mutations; text Agents have no tools.
// @Tags Team Workflows
// @Security ApiKeyAuth
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User identity"
// @Produce json
// @Accept json
// @Param key path string true "Team key"
// @Param Idempotency-Key header string true "Unique submission key; reuse with identical body to replay"
// @Param request body workflowapp.StartRequest true "Fixed Team version and task objective"
// @Success 200 {object} SwaggerWorkflowRun
// @Success 202 {object} SwaggerWorkflowRun
// @Failure 400,401,403,404,409,503 {object} SwaggerError
// @Router /tenant/workflows/{key}/runs [post]
func swaggerWorkflowStart() {}

// @Summary Read an authoritative persisted run snapshot
// @Description Workflow V2 uses private Profile/Team resources. Read-only deployment disables mutations; text Agents have no tools.
// @Tags Team Workflows
// @Security ApiKeyAuth
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User identity"
// @Produce json
// @Param id path string true "Run ID"
// @Success 200 {object} SwaggerWorkflowRun
// @Failure 400,401,403,404,409,503 {object} SwaggerError
// @Router /tenant/workflow-runs/{id} [get]
func swaggerWorkflowRun() {}

// @Summary Read ordered durable execution events
// @Description Workflow V2 uses private Profile/Team resources. Read-only deployment disables mutations; text Agents have no tools.
// @Tags Team Workflows
// @Security ApiKeyAuth
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User identity"
// @Produce json
// @Param id path string true "Run ID"
// @Param after_sequence query int false "Last applied event sequence"
// @Success 200 {object} SwaggerWorkflowEvents
// @Failure 400,401,403,404,409,503 {object} SwaggerError
// @Router /tenant/workflow-runs/{id}/events [get]
func swaggerWorkflowEvents() {}

// @Summary Stream ordered execution events with persisted replay
// @Description Workflow V2 uses private Profile/Team resources. Read-only deployment disables mutations; text Agents have no tools.
// @Tags Team Workflows
// @Security ApiKeyAuth
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User identity"
// @Produce text/event-stream
// @Param id path string true "Run ID"
// @Param after_sequence query int false "Last applied event sequence"
// @Success 200 {object} workflow.Event
// @Failure 400,401,403,404,409,503 {object} SwaggerError
// @Router /tenant/workflow-runs/{id}/events/stream [get]
func swaggerWorkflowStream() {}

// @Summary Request run cancellation and bounded executor cleanup
// @Description Workflow V2 uses private Profile/Team resources. Read-only deployment disables mutations; text Agents have no tools.
// @Tags Team Workflows
// @Security ApiKeyAuth
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User identity"
// @Produce json
// @Param id path string true "Run ID"
// @Success 200 {object} SwaggerWorkflowRun
// @Failure 400,401,403,404,409,503 {object} SwaggerError
// @Router /tenant/workflow-runs/{id}/cancel [post]
func swaggerWorkflowCancel() {}

// @Summary Explicitly recover an interrupted run using its checkpoint revision
// @Description Workflow V2 uses private Profile/Team resources. Read-only deployment disables mutations; text Agents have no tools.
// @Tags Team Workflows
// @Security ApiKeyAuth
// @Param X-Tenant-Key header string true "Tenant key"
// @Param X-User-Id header string true "User identity"
// @Produce json
// @Accept json
// @Param id path string true "Run ID"
// @Param request body SwaggerWorkflowRecovery true "Expected interrupted snapshot revision"
// @Success 200 {object} SwaggerWorkflowRun
// @Failure 400,401,403,404,409,503 {object} SwaggerError
// @Router /tenant/workflow-runs/{id}/recover [post]
func swaggerWorkflowRecover() {}
