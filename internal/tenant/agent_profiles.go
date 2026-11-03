package tenant

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/konglong87/super-niuma/internal/agentprofile"
	"github.com/konglong87/super-niuma/internal/agentruntime"
	"github.com/konglong87/super-niuma/internal/agentteam"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

type agentProfileRepository interface {
	ListAgentProfiles(context.Context, uint64, uint64, string, int) ([]mysqlstore.AgentProfile, error)
	GetAgentProfile(context.Context, uint64, uint64) (mysqlstore.AgentProfile, error)
	CreateAgentProfile(context.Context, mysqlstore.AgentProfileInput) (mysqlstore.AgentProfile, error)
	PublishAgentProfile(context.Context, uint64, uint64, uint64, string, string) error
	ArchiveAgentProfile(context.Context, uint64, uint64, uint64) error
	UpsertAgentProfileAssignment(context.Context, mysqlstore.AgentProfileAssignmentInput) (mysqlstore.AgentProfileAssignment, error)
	GetAgentProfileAssignment(context.Context, uint64, uint64, string) (mysqlstore.AgentProfileAssignment, error)
	UpsertAgentProfileChannelBinding(context.Context, mysqlstore.AgentProfileChannelBindingInput) (mysqlstore.AgentProfileChannelBinding, error)
	GetAgentProfileChannelBinding(context.Context, uint64, uint64) (mysqlstore.AgentProfileChannelBinding, error)
	ArchiveAgentProfileChannelBinding(context.Context, uint64, uint64) error
	ListAgentTeams(context.Context, uint64, uint64, string, int) ([]mysqlstore.AgentTeam, error)
	GetAgentTeam(context.Context, uint64, uint64) (mysqlstore.AgentTeam, error)
	CreateAgentTeam(context.Context, mysqlstore.AgentTeamInput) (mysqlstore.AgentTeam, error)
	PublishAgentTeam(context.Context, uint64, uint64, uint64, string, string) error
	ArchiveAgentTeam(context.Context, uint64, uint64, uint64) error
	CreateAgentTeamMember(context.Context, mysqlstore.AgentTeamMemberInput) (mysqlstore.AgentTeamMember, error)
	ArchiveAgentTeamMembers(context.Context, uint64, uint64) error
	ListAgentTeamMembers(context.Context, uint64, uint64, int) ([]mysqlstore.AgentTeamMember, error)
	CreateAgentTeamBinding(context.Context, mysqlstore.AgentTeamBindingInput) (mysqlstore.AgentTeamBinding, error)
	ArchiveAgentTeamBindings(context.Context, uint64, uint64) error
	ListAgentTeamBindings(context.Context, uint64, uint64, int) ([]mysqlstore.AgentTeamBinding, error)
	ListAgentTeamRuns(context.Context, uint64, uint64, int) ([]mysqlstore.AgentTeamRun, error)
	GetAgentTeamRun(context.Context, uint64, string) (mysqlstore.AgentTeamRun, error)
	ListAgentTeamRunEvents(context.Context, uint64, string, int) ([]mysqlstore.AgentTeamRunEvent, error)
	ListAgentTeamMailbox(context.Context, uint64, string, int) ([]mysqlstore.AgentTeamMailbox, error)
	UpdateAgentTeamRun(context.Context, mysqlstore.AgentTeamRunUpdate) error
	ListChannelAccounts(context.Context, uint64, mysqlstore.ListOptions) ([]mysqlstore.ChannelAccount, error)
	ListAgentProfileConversationSummaries(context.Context, uint64, uint64, int) ([]mysqlstore.AgentProfileConversationSummary, error)
	ListAgentProfileTeamLinks(context.Context, uint64, uint64, int) ([]mysqlstore.AgentProfileTeamLink, error)
}

func decodeExecutionOverride(raw string) agentruntime.AgentExecutionOverride {
	var override agentruntime.AgentExecutionOverride
	if strings.TrimSpace(raw) == "" {
		return override
	}
	if err := json.Unmarshal([]byte(raw), &override); err != nil {
		return agentruntime.AgentExecutionOverride{ReasonCodes: []string{"execution_override_invalid_json"}}
	}
	return agentruntime.NormalizeExecutionOverride(override)
}

func (s *Service) profileRepo() (agentProfileRepository, error) {
	repo, ok := s.repo.(agentProfileRepository)
	if !ok {
		return nil, fmt.Errorf("agent profile storage is not configured")
	}
	return repo, nil
}

func (s *Service) ListAgentProfiles(ctx context.Context, status string, limit int) ([]mysqlstore.AgentProfile, error) {
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return nil, err
	}
	repo, err := s.profileRepo()
	if err != nil {
		return nil, err
	}
	return repo.ListAgentProfiles(ctx, resolved.TenantID, resolved.UserID, status, limit)
}

func (s *Service) GetAgentProfile(ctx context.Context, key string, version uint) (mysqlstore.AgentProfile, error) {
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return mysqlstore.AgentProfile{}, err
	}
	repo, err := s.profileRepo()
	if err != nil {
		return mysqlstore.AgentProfile{}, err
	}
	items, err := repo.ListAgentProfiles(ctx, resolved.TenantID, resolved.UserID, "", 500)
	if err != nil {
		return mysqlstore.AgentProfile{}, err
	}
	for _, item := range items {
		if item.ProfileKey == strings.TrimSpace(key) && (version == 0 || item.ProfileVersion == version) && item.Status != "archived" {
			return item, nil
		}
	}
	return mysqlstore.AgentProfile{}, mysqlstore.ErrNotFound
}

func (s *Service) ListAgentProfileConversations(ctx context.Context, key string, version uint, limit int) (mysqlstore.AgentProfileConversationCatalog, error) {
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return mysqlstore.AgentProfileConversationCatalog{}, err
	}
	repo, err := s.profileRepo()
	if err != nil {
		return mysqlstore.AgentProfileConversationCatalog{}, err
	}
	profile, err := s.GetAgentProfile(ctx, key, uint(version))
	if err != nil {
		return mysqlstore.AgentProfileConversationCatalog{}, err
	}
	conversations, err := repo.ListAgentProfileConversationSummaries(ctx, resolved.TenantID, profile.ID, limit)
	if err != nil {
		return mysqlstore.AgentProfileConversationCatalog{}, err
	}
	teams, err := repo.ListAgentProfileTeamLinks(ctx, resolved.TenantID, profile.ID, limit)
	if err != nil {
		return mysqlstore.AgentProfileConversationCatalog{}, err
	}
	var messageCount, runCount uint64
	for _, item := range conversations {
		messageCount += item.MessageCount
		runCount += item.RunCount
	}
	return mysqlstore.AgentProfileConversationCatalog{Profile: profile, Conversations: conversations, Teams: teams, MessageCount: messageCount, RunCount: runCount}, nil
}

func (s *Service) SaveAgentProfile(ctx context.Context, input mysqlstore.AgentProfileInput) (mysqlstore.AgentProfile, error) {
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return mysqlstore.AgentProfile{}, err
	}
	repo, err := s.profileRepo()
	if err != nil {
		return mysqlstore.AgentProfile{}, err
	}
	input.TenantID = resolved.TenantID
	if input.OwnerUserID == 0 && input.Scope == string(agentprofile.ScopeUserPrivate) {
		input.OwnerUserID = resolved.UserID
	}
	if input.OwnerKey == "" {
		if input.OwnerUserID > 0 {
			input.OwnerKey = fmt.Sprintf("user:%d", input.OwnerUserID)
		} else {
			input.OwnerKey = "shared"
		}
	}
	if input.CreatedByUserID == 0 {
		input.CreatedByUserID = resolved.UserID
	}
	if input.UpdatedByUserID == 0 {
		input.UpdatedByUserID = resolved.UserID
	}
	if input.ProfileVersion == 0 {
		items, listErr := repo.ListAgentProfiles(ctx, resolved.TenantID, resolved.UserID, "", 500)
		if listErr != nil {
			return mysqlstore.AgentProfile{}, listErr
		}
		input.ProfileVersion = nextProfileVersion(items, input.ProfileKey, input.OwnerKey)
	}
	if input.Status == "" {
		input.Status = "draft"
	}
	if input.RequestedHash == "" {
		if document, decodeErr := agentprofile.DecodeProfileDocument([]byte(input.ConfigJSON)); decodeErr == nil {
			input.RequestedHash, _ = document.CanonicalHash()
		}
	}
	if input.EffectiveHash == "" {
		input.EffectiveHash = input.RequestedHash
	}
	return repo.CreateAgentProfile(ctx, input)
}

func (s *Service) ValidateAgentProfile(_ context.Context, input mysqlstore.AgentProfileInput) (agentprofile.ValidationReport, error) {
	document, err := agentprofile.DecodeProfileDocument([]byte(input.ConfigJSON))
	if err != nil {
		return agentprofile.ValidationReport{}, err
	}
	return agentprofile.Validate(document, agentprofile.ValidationContext{}), nil
}

type serviceProfileSource struct{ service *Service }

func (source serviceProfileSource) FindPublishedProfile(ctx context.Context, _ uint64, _ uint64, key string, version uint) (agentprofile.Profile, error) {
	profile, err := source.service.GetAgentProfile(ctx, key, version)
	if err != nil {
		return agentprofile.Profile{}, agentprofile.ErrProfileNotFound
	}
	if profile.Status != string(agentprofile.StatusPublished) {
		return agentprofile.Profile{}, agentprofile.ErrProfileNotFound
	}
	document, err := agentprofile.DecodeProfileDocument([]byte(profile.ConfigJSON))
	if err != nil {
		return agentprofile.Profile{}, err
	}
	return agentprofile.Profile{ID: profile.ID, TenantID: profile.TenantID, OwnerUserID: profile.OwnerUserID, Key: profile.ProfileKey, Scope: agentprofile.Scope(profile.Scope), DisplayName: profile.DisplayName, Description: profile.Description, Version: profile.ProfileVersion, Status: agentprofile.Status(profile.Status), Config: document, RequestedHash: profile.RequestedHash, EffectiveHash: profile.EffectiveHash}, nil
}

func (source serviceProfileSource) FindAssignment(ctx context.Context, tenantID, userID uint64, surface string) (agentprofile.ProfileAssignment, error) {
	repo, err := source.service.profileRepo()
	if err != nil {
		return agentprofile.ProfileAssignment{}, agentprofile.ErrAssignmentNotFound
	}
	assignment, err := repo.GetAgentProfileAssignment(ctx, tenantID, userID, surface)
	if err != nil {
		return agentprofile.ProfileAssignment{}, agentprofile.ErrAssignmentNotFound
	}
	profile, err := repo.GetAgentProfile(ctx, tenantID, assignment.ProfileID)
	if err != nil {
		return agentprofile.ProfileAssignment{}, agentprofile.ErrAssignmentNotFound
	}
	return agentprofile.ProfileAssignment{ProfileKey: profile.ProfileKey, ProfileVersion: profile.ProfileVersion}, nil
}

func (s *Service) ResolveAgentProfile(ctx context.Context, request agentprofile.ResolveRequest) (agentprofile.EffectiveProfile, error) {
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return agentprofile.EffectiveProfile{}, err
	}
	request.TenantID, request.UserID = resolved.TenantID, resolved.UserID
	policy := agentprofile.RuntimePolicy{}
	if request.Surface == agentprofile.SurfaceTenantAgent || request.Surface == agentprofile.SurfaceChannelTeam {
		policy.AllowWorkspace = true
		policy.AllowGit = true
	}
	return agentprofile.NewResolver(agentprofile.NewCatalog(serviceProfileSource{service: s}), policy).Resolve(ctx, request)
}

func (s *Service) PublishAgentProfile(ctx context.Context, key string, version uint) error {
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return err
	}
	repo, err := s.profileRepo()
	if err != nil {
		return err
	}
	profile, err := s.GetAgentProfile(ctx, key, version)
	if err != nil {
		return err
	}
	document, err := agentprofile.DecodeProfileDocument([]byte(profile.ConfigJSON))
	if err != nil {
		return err
	}
	report := agentprofile.Validate(document, agentprofile.ValidationContext{})
	if !report.Valid {
		return fmt.Errorf("%w: %s", ErrInvalidRequest, report.Issues[0].Message)
	}
	hash, err := document.CanonicalHash()
	if err != nil {
		return err
	}
	validationJSON, _ := json.Marshal(report)
	return repo.PublishAgentProfile(ctx, resolved.TenantID, profile.ID, resolved.UserID, string(validationJSON), hash)
}

func (s *Service) ArchiveAgentProfile(ctx context.Context, key string, version uint) error {
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return err
	}
	repo, err := s.profileRepo()
	if err != nil {
		return err
	}
	profile, err := s.GetAgentProfile(ctx, key, version)
	if err != nil {
		return err
	}
	return repo.ArchiveAgentProfile(ctx, resolved.TenantID, profile.ID, resolved.UserID)
}

func (s *Service) RollbackAgentProfile(ctx context.Context, key string, version uint) (mysqlstore.AgentProfile, error) {
	profile, err := s.GetAgentProfile(ctx, key, version)
	if err != nil {
		return mysqlstore.AgentProfile{}, err
	}
	return s.SaveAgentProfile(ctx, mysqlstore.AgentProfileInput{OwnerUserID: profile.OwnerUserID, OwnerKey: profile.OwnerKey, ProfileKey: profile.ProfileKey, Scope: profile.Scope, DisplayName: profile.DisplayName, Description: profile.Description, ConfigJSON: profile.ConfigJSON, RequestedHash: profile.RequestedHash, EffectiveHash: profile.EffectiveHash, Status: "draft"})
}

func (s *Service) GetAgentProfileChannelBinding(ctx context.Context, key string, version uint) (mysqlstore.AgentProfileChannelBinding, error) {
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return mysqlstore.AgentProfileChannelBinding{}, err
	}
	repo, err := s.profileRepo()
	if err != nil {
		return mysqlstore.AgentProfileChannelBinding{}, err
	}
	profile, err := s.GetAgentProfile(ctx, key, version)
	if err != nil {
		return mysqlstore.AgentProfileChannelBinding{}, err
	}
	return repo.GetAgentProfileChannelBinding(ctx, resolved.TenantID, profile.ID)
}

func (s *Service) UpsertAgentProfileChannelBinding(ctx context.Context, key string, version uint, input mysqlstore.AgentProfileChannelBindingInput) (mysqlstore.AgentProfileChannelBinding, error) {
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return mysqlstore.AgentProfileChannelBinding{}, err
	}
	repo, err := s.profileRepo()
	if err != nil {
		return mysqlstore.AgentProfileChannelBinding{}, err
	}
	profile, err := s.GetAgentProfile(ctx, key, version)
	if err != nil {
		return mysqlstore.AgentProfileChannelBinding{}, err
	}
	input.TenantID, input.ProfileID, input.CreatedByUserID = resolved.TenantID, profile.ID, resolved.UserID
	if input.BindingKey == "" {
		input.BindingKey = key
	}
	return repo.UpsertAgentProfileChannelBinding(ctx, input)
}

func (s *Service) ArchiveAgentProfileChannelBinding(ctx context.Context, key string, version uint) error {
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return err
	}
	repo, err := s.profileRepo()
	if err != nil {
		return err
	}
	profile, err := s.GetAgentProfile(ctx, key, version)
	if err != nil {
		return err
	}
	return repo.ArchiveAgentProfileChannelBinding(ctx, resolved.TenantID, profile.ID)
}

func (s *Service) GetAgentProfileAssignment(ctx context.Context, surface string) (mysqlstore.AgentProfileAssignment, error) {
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return mysqlstore.AgentProfileAssignment{}, err
	}
	repo, err := s.profileRepo()
	if err != nil {
		return mysqlstore.AgentProfileAssignment{}, err
	}
	return repo.GetAgentProfileAssignment(ctx, resolved.TenantID, resolved.UserID, surface)
}

func (s *Service) UpsertAgentProfileAssignment(ctx context.Context, input mysqlstore.AgentProfileAssignmentInput) (mysqlstore.AgentProfileAssignment, error) {
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return mysqlstore.AgentProfileAssignment{}, err
	}
	repo, err := s.profileRepo()
	if err != nil {
		return mysqlstore.AgentProfileAssignment{}, err
	}
	profile, err := repo.GetAgentProfile(ctx, resolved.TenantID, input.ProfileID)
	if err != nil {
		return mysqlstore.AgentProfileAssignment{}, err
	}
	if profile.Status != string(agentprofile.StatusPublished) {
		return mysqlstore.AgentProfileAssignment{}, fmt.Errorf("%w: profile assignment requires a published profile", ErrInvalidRequest)
	}
	input.TenantID, input.UserID, input.AssignedByUserID = resolved.TenantID, resolved.UserID, resolved.UserID
	return repo.UpsertAgentProfileAssignment(ctx, input)
}

func nextProfileVersion(items []mysqlstore.AgentProfile, key, ownerKey string) uint {
	var version uint = 1
	for _, item := range items {
		if item.ProfileKey == key && item.OwnerKey == ownerKey && item.ProfileVersion >= version {
			version = item.ProfileVersion + 1
		}
	}
	return version
}

// Team methods are intentionally kept in the same optional service so a server
// can enable Profile and Team APIs together without changing the legacy tenant
// Repository interface implemented by existing fakes.
func (s *Service) ListAgentTeams(ctx context.Context, status string, limit int) ([]mysqlstore.AgentTeam, error) {
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return nil, err
	}
	repo, err := s.profileRepo()
	if err != nil {
		return nil, err
	}
	return repo.ListAgentTeams(ctx, resolved.TenantID, resolved.UserID, status, limit)
}
func (s *Service) GetAgentTeam(ctx context.Context, key string, version uint) (mysqlstore.AgentTeam, error) {
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return mysqlstore.AgentTeam{}, err
	}
	repo, err := s.profileRepo()
	if err != nil {
		return mysqlstore.AgentTeam{}, err
	}
	items, err := repo.ListAgentTeams(ctx, resolved.TenantID, resolved.UserID, "", 500)
	if err != nil {
		return mysqlstore.AgentTeam{}, err
	}
	for _, item := range items {
		if item.TeamKey == key && (version == 0 || item.TeamVersion == version) && item.Status != "archived" {
			return item, nil
		}
	}
	return mysqlstore.AgentTeam{}, mysqlstore.ErrNotFound
}
func (s *Service) SaveAgentTeam(ctx context.Context, input mysqlstore.AgentTeamInput) (mysqlstore.AgentTeam, error) {
	if input.SchemaVersion > 1 {
		return mysqlstore.AgentTeam{}, fmt.Errorf("%w: edit Workflow V2 through the Team workspace", ErrInvalidRequest)
	}
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return mysqlstore.AgentTeam{}, err
	}
	repo, err := s.profileRepo()
	if err != nil {
		return mysqlstore.AgentTeam{}, err
	}
	input.TenantID, input.CreatedByUserID, input.UpdatedByUserID = resolved.TenantID, resolved.UserID, resolved.UserID
	if input.OwnerKey == "" {
		input.OwnerKey = "shared"
	}
	if input.TeamVersion == 0 {
		items, listErr := repo.ListAgentTeams(ctx, resolved.TenantID, resolved.UserID, "", 500)
		if listErr != nil {
			return mysqlstore.AgentTeam{}, listErr
		}
		for _, item := range items {
			if item.TeamKey == input.TeamKey && item.TeamVersion >= input.TeamVersion {
				input.TeamVersion = item.TeamVersion + 1
			}
		}
		if input.TeamVersion == 0 {
			input.TeamVersion = 1
		}
	}
	if input.Status == "" {
		input.Status = "draft"
	}
	if input.RequestedHash == "" {
		input.RequestedHash = canonicalJSONHash(input.PolicyJSON)
	}
	if input.EffectiveHash == "" {
		input.EffectiveHash = input.RequestedHash
	}
	return repo.CreateAgentTeam(ctx, input)
}

func canonicalJSONHash(raw string) string {
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(raw)); err != nil {
		return ""
	}
	sum := sha256.Sum256(compact.Bytes())
	return hex.EncodeToString(sum[:])
}
func (s *Service) ValidateAgentTeam(ctx context.Context, input mysqlstore.AgentTeamInput, members []mysqlstore.AgentTeamMemberInput, bindings []mysqlstore.AgentTeamBindingInput) (agentteam.ValidationReport, error) {
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return agentteam.ValidationReport{}, err
	}
	repo, err := s.profileRepo()
	if err != nil {
		return agentteam.ValidationReport{}, err
	}
	var policy agentteam.TeamPolicy
	if err := json.Unmarshal([]byte(input.PolicyJSON), &policy); err != nil {
		return agentteam.ValidationReport{}, err
	}
	team := agentteam.Team{TenantID: resolved.TenantID, Key: input.TeamKey, Version: input.TeamVersion, Status: agentteam.StatusPublished, Policy: policy}
	published := make(map[agentteam.ProfileRef]bool)
	profiles, err := repo.ListAgentProfiles(ctx, resolved.TenantID, resolved.UserID, "published", 500)
	if err != nil {
		return agentteam.ValidationReport{}, err
	}
	for _, profile := range profiles {
		published[agentteam.ProfileRef{Key: profile.ProfileKey, Version: profile.ProfileVersion}] = true
	}
	for _, member := range members {
		profile, getErr := repo.GetAgentProfile(ctx, resolved.TenantID, member.ProfileID)
		if getErr != nil {
			return agentteam.ValidationReport{}, getErr
		}
		team.Members = append(team.Members, agentteam.Member{TenantID: resolved.TenantID, Key: member.MemberKey, ProfileKey: profile.ProfileKey, ProfileVersion: profile.ProfileVersion, Role: agentteam.Role(member.Role), AccountKey: fmt.Sprint(member.AccountID), ExecutionOverride: decodeExecutionOverride(member.ExecutionOverrideJSON)})
	}
	for _, binding := range bindings {
		team.Bindings = append(team.Bindings, agentteam.Binding{TenantID: resolved.TenantID, Provider: binding.Provider, AccountKey: fmt.Sprint(binding.AccountID), ExternalChatID: binding.ExternalChatID, ExternalThreadID: binding.ExternalThreadID, Trigger: agentteam.TriggerPolicy(binding.TriggerPolicy)})
	}
	return agentteam.Validate(team, agentteam.ValidationContext{PublishedProfiles: published}), nil
}
func (s *Service) PublishAgentTeam(ctx context.Context, key string, version uint) error {
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return err
	}
	repo, err := s.profileRepo()
	if err != nil {
		return err
	}
	team, err := s.GetAgentTeam(ctx, key, version)
	if err != nil {
		return err
	}
	if team.SchemaVersion > 1 {
		return fmt.Errorf("%w: use the Team workflow workspace for V2 changes", ErrInvalidRequest)
	}
	hash := team.EffectiveHash
	if hash == "" {
		hash = team.RequestedHash
	}
	return repo.PublishAgentTeam(ctx, resolved.TenantID, team.ID, resolved.UserID, "{}", hash)
}
func (s *Service) ArchiveAgentTeam(ctx context.Context, key string, version uint) error {
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return err
	}
	repo, err := s.profileRepo()
	if err != nil {
		return err
	}
	team, err := s.GetAgentTeam(ctx, key, version)
	if err != nil {
		return err
	}
	return repo.ArchiveAgentTeam(ctx, resolved.TenantID, team.ID, resolved.UserID)
}
func (s *Service) RollbackAgentTeam(ctx context.Context, key string, version uint) (mysqlstore.AgentTeam, error) {
	team, err := s.GetAgentTeam(ctx, key, version)
	if err != nil {
		return mysqlstore.AgentTeam{}, err
	}
	if team.SchemaVersion > 1 {
		return mysqlstore.AgentTeam{}, fmt.Errorf("%w: use the Team workflow workspace for V2 changes", ErrInvalidRequest)
	}
	return s.SaveAgentTeam(ctx, mysqlstore.AgentTeamInput{OwnerUserID: team.OwnerUserID, OwnerKey: team.OwnerKey, TeamKey: team.TeamKey, Scope: team.Scope, DisplayName: team.DisplayName, Description: team.Description, PolicyJSON: team.PolicyJSON, RequestedHash: team.RequestedHash, EffectiveHash: team.EffectiveHash, Status: "draft"})
}
func (s *Service) ReplaceAgentTeamMembers(ctx context.Context, key string, version uint, inputs []mysqlstore.AgentTeamMemberInput) ([]mysqlstore.AgentTeamMember, error) {
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return nil, err
	}
	repo, err := s.profileRepo()
	if err != nil {
		return nil, err
	}
	team, err := s.GetAgentTeam(ctx, key, version)
	if err != nil {
		return nil, err
	}
	if team.SchemaVersion > 1 {
		return nil, fmt.Errorf("%w: use the Team workflow workspace for V2 changes", ErrInvalidRequest)
	}
	if err := repo.ArchiveAgentTeamMembers(ctx, resolved.TenantID, team.ID); err != nil {
		return nil, err
	}
	result := make([]mysqlstore.AgentTeamMember, 0, len(inputs))
	for _, input := range inputs {
		input.TenantID, input.TeamID = resolved.TenantID, team.ID
		item, createErr := repo.CreateAgentTeamMember(ctx, input)
		if createErr != nil {
			return nil, createErr
		}
		result = append(result, item)
	}
	return result, nil
}
func (s *Service) ListAgentTeamMembers(ctx context.Context, key string, version uint, limit int) ([]mysqlstore.AgentTeamMember, error) {
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return nil, err
	}
	repo, err := s.profileRepo()
	if err != nil {
		return nil, err
	}
	team, err := s.GetAgentTeam(ctx, key, version)
	if err != nil {
		return nil, err
	}
	return repo.ListAgentTeamMembers(ctx, resolved.TenantID, team.ID, limit)
}
func (s *Service) ReplaceAgentTeamBindings(ctx context.Context, key string, version uint, inputs []mysqlstore.AgentTeamBindingInput) ([]mysqlstore.AgentTeamBinding, error) {
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return nil, err
	}
	repo, err := s.profileRepo()
	if err != nil {
		return nil, err
	}
	team, err := s.GetAgentTeam(ctx, key, version)
	if err != nil {
		return nil, err
	}
	if team.SchemaVersion > 1 {
		return nil, fmt.Errorf("%w: use the Team workflow workspace for V2 changes", ErrInvalidRequest)
	}
	if err := repo.ArchiveAgentTeamBindings(ctx, resolved.TenantID, team.ID); err != nil {
		return nil, err
	}
	result := make([]mysqlstore.AgentTeamBinding, 0, len(inputs))
	for _, input := range inputs {
		input.TenantID, input.TeamID = resolved.TenantID, team.ID
		item, createErr := repo.CreateAgentTeamBinding(ctx, input)
		if createErr != nil {
			return nil, createErr
		}
		result = append(result, item)
	}
	return result, nil
}
func (s *Service) ListAgentTeamBindings(ctx context.Context, key string, version uint, limit int) ([]mysqlstore.AgentTeamBinding, error) {
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return nil, err
	}
	repo, err := s.profileRepo()
	if err != nil {
		return nil, err
	}
	team, err := s.GetAgentTeam(ctx, key, version)
	if err != nil {
		return nil, err
	}
	return repo.ListAgentTeamBindings(ctx, resolved.TenantID, team.ID, limit)
}
func (s *Service) ListAgentTeamRuns(ctx context.Context, key string, version uint, limit int) ([]mysqlstore.AgentTeamRun, error) {
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return nil, err
	}
	repo, err := s.profileRepo()
	if err != nil {
		return nil, err
	}
	team, err := s.GetAgentTeam(ctx, key, version)
	if err != nil {
		return nil, err
	}
	return repo.ListAgentTeamRuns(ctx, resolved.TenantID, team.ID, limit)
}
func (s *Service) GetAgentTeamRun(ctx context.Context, runID string) (mysqlstore.AgentTeamRun, error) {
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return mysqlstore.AgentTeamRun{}, err
	}
	repo, err := s.profileRepo()
	if err != nil {
		return mysqlstore.AgentTeamRun{}, err
	}
	return repo.GetAgentTeamRun(ctx, resolved.TenantID, runID)
}
func (s *Service) ListAgentTeamRunEvents(ctx context.Context, key string, version uint, runID string, limit int) ([]mysqlstore.AgentTeamRunEvent, error) {
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return nil, err
	}
	repo, err := s.profileRepo()
	if err != nil {
		return nil, err
	}
	team, err := s.GetAgentTeam(ctx, key, version)
	if err != nil {
		return nil, err
	}
	run, err := repo.GetAgentTeamRun(ctx, resolved.TenantID, runID)
	if err != nil {
		return nil, err
	}
	if run.TeamID != team.ID {
		return nil, mysqlstore.ErrNotFound
	}
	return repo.ListAgentTeamRunEvents(ctx, resolved.TenantID, runID, limit)
}

func (s *Service) ListAgentTeamMailbox(ctx context.Context, key string, version uint, runID string, limit int) ([]mysqlstore.AgentTeamMailbox, error) {
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return nil, err
	}
	repo, err := s.profileRepo()
	if err != nil {
		return nil, err
	}
	team, err := s.GetAgentTeam(ctx, key, version)
	if err != nil {
		return nil, err
	}
	run, err := repo.GetAgentTeamRun(ctx, resolved.TenantID, runID)
	if err != nil {
		return nil, err
	}
	if run.TeamID != team.ID {
		return nil, mysqlstore.ErrNotFound
	}
	return repo.ListAgentTeamMailbox(ctx, resolved.TenantID, runID, limit)
}

func (s *Service) CancelAgentTeamRun(ctx context.Context, runID string) error {
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return err
	}
	repo, err := s.profileRepo()
	if err != nil {
		return err
	}
	run, getErr := repo.GetAgentTeamRun(ctx, resolved.TenantID, runID)
	if getErr != nil {
		return getErr
	}
	team, getErr := repo.GetAgentTeam(ctx, resolved.TenantID, run.TeamID)
	if getErr != nil {
		return getErr
	}
	if team.SchemaVersion > 1 {
		return fmt.Errorf("%w: cancel this Run in the Team workflow workspace", ErrInvalidRequest)
	}
	return repo.UpdateAgentTeamRun(ctx, mysqlstore.AgentTeamRunUpdate{TenantID: resolved.TenantID, RunID: runID, Status: "cancelled"})
}
func (s *Service) ListChannelAccounts(ctx context.Context, limit int) ([]mysqlstore.ChannelAccount, error) {
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return nil, err
	}
	repo, err := s.profileRepo()
	if err != nil {
		return nil, err
	}
	return repo.ListChannelAccounts(ctx, resolved.TenantID, mysqlstore.ListOptions{Limit: limit})
}

var _ interface {
	ListAgentProfiles(context.Context, string, int) ([]mysqlstore.AgentProfile, error)
} = (*Service)(nil)

func sortProfiles(items []mysqlstore.AgentProfile) {
	sort.Slice(items, func(i, j int) bool { return items[i].ProfileKey < items[j].ProfileKey })
}
