package server

import (
	"context"

	"github.com/konglong87/super-niuma/internal/agentprofile"
	"github.com/konglong87/super-niuma/internal/agentteam"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	tenantservice "github.com/konglong87/super-niuma/internal/tenant"
)

type AgentProfileService interface {
	ListAgentProfiles(context.Context, string, int) ([]mysqlstore.AgentProfile, error)
	GetAgentProfile(context.Context, string, uint) (mysqlstore.AgentProfile, error)
	SaveAgentProfile(context.Context, mysqlstore.AgentProfileInput) (mysqlstore.AgentProfile, error)
	ValidateAgentProfile(context.Context, mysqlstore.AgentProfileInput) (agentprofile.ValidationReport, error)
	ResolveAgentProfile(context.Context, agentprofile.ResolveRequest) (agentprofile.EffectiveProfile, error)
	PublishAgentProfile(context.Context, string, uint) error
	ArchiveAgentProfile(context.Context, string, uint) error
	RollbackAgentProfile(context.Context, string, uint) (mysqlstore.AgentProfile, error)
	GetAgentProfileChannelBinding(context.Context, string, uint) (mysqlstore.AgentProfileChannelBinding, error)
	UpsertAgentProfileChannelBinding(context.Context, string, uint, mysqlstore.AgentProfileChannelBindingInput) (mysqlstore.AgentProfileChannelBinding, error)
	ArchiveAgentProfileChannelBinding(context.Context, string, uint) error
	GetAgentProfileAssignment(context.Context, string) (mysqlstore.AgentProfileAssignment, error)
	UpsertAgentProfileAssignment(context.Context, mysqlstore.AgentProfileAssignmentInput) (mysqlstore.AgentProfileAssignment, error)
	ListAgentProfileConversations(context.Context, string, uint, int) (mysqlstore.AgentProfileConversationCatalog, error)
}

type AgentTeamService interface {
	ListAgentTeams(context.Context, string, int) ([]mysqlstore.AgentTeam, error)
	GetAgentTeam(context.Context, string, uint) (mysqlstore.AgentTeam, error)
	SaveAgentTeam(context.Context, mysqlstore.AgentTeamInput) (mysqlstore.AgentTeam, error)
	ValidateAgentTeam(context.Context, mysqlstore.AgentTeamInput, []mysqlstore.AgentTeamMemberInput, []mysqlstore.AgentTeamBindingInput) (agentteam.ValidationReport, error)
	PublishAgentTeam(context.Context, string, uint) error
	ArchiveAgentTeam(context.Context, string, uint) error
	RollbackAgentTeam(context.Context, string, uint) (mysqlstore.AgentTeam, error)
	ReplaceAgentTeamMembers(context.Context, string, uint, []mysqlstore.AgentTeamMemberInput) ([]mysqlstore.AgentTeamMember, error)
	ListAgentTeamMembers(context.Context, string, uint, int) ([]mysqlstore.AgentTeamMember, error)
	ReplaceAgentTeamBindings(context.Context, string, uint, []mysqlstore.AgentTeamBindingInput) ([]mysqlstore.AgentTeamBinding, error)
	ListAgentTeamBindings(context.Context, string, uint, int) ([]mysqlstore.AgentTeamBinding, error)
	ListAgentTeamRuns(context.Context, string, uint, int) ([]mysqlstore.AgentTeamRun, error)
	GetAgentTeamRun(context.Context, string) (mysqlstore.AgentTeamRun, error)
	ListAgentTeamRunEvents(context.Context, string, uint, string, int) ([]mysqlstore.AgentTeamRunEvent, error)
	ListAgentTeamMailbox(context.Context, string, uint, string, int) ([]mysqlstore.AgentTeamMailbox, error)
	CancelAgentTeamRun(context.Context, string) error
	ListChannelAccounts(context.Context, int) ([]mysqlstore.ChannelAccount, error)
}

func optionalAgentProfileService(opts Options) (AgentProfileService, bool) {
	service, ok := opts.TenantService.(AgentProfileService)
	return service, ok && service != nil
}

func optionalAgentTeamService(opts Options) (AgentTeamService, bool) {
	service, ok := opts.TenantService.(AgentTeamService)
	return service, ok && service != nil
}

var _ AgentProfileService = (*tenantservice.Service)(nil)
var _ AgentTeamService = (*tenantservice.Service)(nil)
