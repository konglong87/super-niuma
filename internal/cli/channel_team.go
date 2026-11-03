package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/konglong87/super-niuma/internal/agentprofile"
	"github.com/konglong87/super-niuma/internal/agentteam"
	channelcontract "github.com/konglong87/super-niuma/internal/channel"
	channelruntime "github.com/konglong87/super-niuma/internal/channel/runtime"
	"github.com/konglong87/super-niuma/internal/observability"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	tenantservice "github.com/konglong87/super-niuma/internal/tenant"
)

type channelTeamRuntime struct {
	tenantID       uint64
	accountID      uint64
	userID         uint64
	tenantKey      string
	userKey        string
	accountKey     string
	workspace      string
	permissionMode string
	model          string
	repo           *mysqlstore.GormRepository
	service        *tenantservice.Service
	baseRunner     cliChannelRunner
}

func teamDispatchFunc(runtime *channelTeamRuntime) channelruntime.TeamDispatchFunc {
	if runtime == nil {
		return nil
	}
	return runtime.Dispatch
}

func newChannelTeamRuntime(ctx context.Context, repo *mysqlstore.GormRepository, service *tenantservice.Service, adapter channelcontract.Adapter, opts options, tenantID, accountID, userID uint64, tenantKey, userKey, accountKey, workspace, permissionMode, model string) (*channelTeamRuntime, agentteam.Router, error) {
	if repo == nil || service == nil || adapter == nil || tenantID == 0 || accountID == 0 || userID == 0 {
		return nil, agentteam.Router{}, errors.New("channel team runtime requires repository, service, adapter and identities")
	}
	accounts, err := repo.ListChannelAccounts(ctx, tenantID, mysqlstore.ListOptions{Limit: 500})
	if err != nil {
		return nil, agentteam.Router{}, err
	}
	accountKeys := make(map[uint64]string, len(accounts))
	for _, account := range accounts {
		accountKeys[account.ID] = account.AccountKey
	}
	teams, err := repo.ListAgentTeams(ctx, tenantID, userID, string(agentteam.StatusPublished), 500)
	if err != nil {
		return nil, agentteam.Router{}, err
	}
	bindings := make([]agentteam.Binding, 0)
	for _, team := range teams {
		if team.SchemaVersion > 1 {
			continue
		}
		rows, listErr := repo.ListAgentTeamBindings(ctx, tenantID, team.ID, 500)
		if listErr != nil {
			return nil, agentteam.Router{}, listErr
		}
		for _, row := range rows {
			if row.AccountID != accountID || row.Status != "active" {
				continue
			}
			bindings = append(bindings, agentteam.Binding{TenantID: tenantID, TeamID: team.ID, TeamVersion: team.TeamVersion, Provider: row.Provider, AccountKey: accountKeys[row.AccountID], ExternalChatID: row.ExternalChatID, ExternalThreadID: row.ExternalThreadID, Trigger: agentteam.TriggerPolicy(row.TriggerPolicy)})
		}
	}
	botKeys := map[string]struct{}{}
	if identityProvider, ok := adapter.(interface{ BotOpenID(context.Context) string }); ok {
		if openID := strings.TrimSpace(identityProvider.BotOpenID(ctx)); openID != "" {
			botKeys[openID] = struct{}{}
		}
	}
	router := agentteam.NewRouter(bindings, botKeys)
	runtime := &channelTeamRuntime{tenantID: tenantID, accountID: accountID, userID: userID, tenantKey: tenantKey, userKey: userKey, accountKey: accountKey, workspace: workspace, permissionMode: permissionMode, model: model, repo: repo, service: service, baseRunner: cliChannelRunner{opts: opts, repo: repo}}
	return runtime, router, nil
}

func (r *channelTeamRuntime) Dispatch(ctx context.Context, input channelruntime.TeamDispatchInput) (channelruntime.TeamDispatchResult, error) {
	if r == nil {
		return channelruntime.TeamDispatchResult{}, errors.New("team runtime is not configured")
	}
	runCtx := observability.WithRequestValues(ctx, observability.TraceID(ctx), r.userKey, r.tenantKey)
	return r.execute(runCtx, input)
}

func (r *channelTeamRuntime) execute(ctx context.Context, input channelruntime.TeamDispatchInput) (channelruntime.TeamDispatchResult, error) {
	route := input.Route
	msg := input.Message
	team, err := r.loadTeam(ctx, route)
	if err != nil {
		return channelruntime.TeamDispatchResult{}, err
	}
	memberRunner := &channelTeamMemberRunner{base: r.baseRunner, repo: r.repo, service: r.service, tenantID: r.tenantID, userID: input.UserID, tenantKey: r.tenantKey, userKey: r.userKey, conversationID: input.Run.ConversationID, sessionID: input.Run.SessionID, scope: input.Scope, message: msg, workspace: r.workspace, permissionMode: r.permissionMode, model: r.model}
	runRecorder := agentteam.NewMySQLRunRecorder(r.repo, r.repo)
	orchestrator := agentteam.NewOrchestratorWithRecorder(memberRunner, agentteam.NewMySQLMailbox(r.repo), runRecorder, runRecorder)
	teamOptions := agentteam.RunOptions{InboxEventID: input.Inbox.ID, SourceAccountID: r.accountID, ConversationID: input.Run.ConversationID}
	var result agentteam.TeamRunResult
	if input.Stream != nil {
		result, err = orchestrator.RunWithOptionsAndStream(ctx, team, teamOptions, agentteam.TextSink(func(text string) error {
			return input.Stream.OnDelta(ctx, channelcontract.Delta{Kind: channelcontract.DeltaText, Text: text})
		}), msg.Text)
	} else {
		result, err = orchestrator.RunWithOptions(ctx, team, teamOptions, msg.Text)
	}
	if err != nil {
		return channelruntime.TeamDispatchResult{}, err
	}
	return channelruntime.TeamDispatchResult{FinalText: result.Final.Content, Status: result.Status, Error: result.Error}, nil
}

func (r *channelTeamRuntime) loadTeam(ctx context.Context, route agentteam.RouteResult) (agentteam.Team, error) {
	record, err := r.repo.GetAgentTeam(ctx, r.tenantID, route.TeamID)
	if err != nil || record.TeamVersion != route.TeamVersion || record.Status != string(agentteam.StatusPublished) {
		if err == nil {
			err = mysqlstore.ErrNotFound
		}
		return agentteam.Team{}, err
	}
	if record.SchemaVersion > 1 {
		return agentteam.Team{}, fmt.Errorf("Team workflow schema %d is not supported by the V1 channel runtime", record.SchemaVersion)
	}
	var policy agentteam.TeamPolicy
	if err := json.Unmarshal([]byte(record.PolicyJSON), &policy); err != nil {
		return agentteam.Team{}, err
	}
	rows, err := r.repo.ListAgentTeamMembers(ctx, r.tenantID, record.ID, 500)
	if err != nil {
		return agentteam.Team{}, err
	}
	members := make([]agentteam.Member, 0, len(rows))
	for _, row := range rows {
		profile, profileErr := r.repo.GetAgentProfile(ctx, r.tenantID, row.ProfileID)
		if profileErr != nil {
			return agentteam.Team{}, profileErr
		}
		members = append(members, agentteam.Member{TenantID: r.tenantID, Key: row.MemberKey, ProfileKey: profile.ProfileKey, ProfileVersion: profile.ProfileVersion, Role: agentteam.Role(row.Role), AccountKey: fmt.Sprint(row.AccountID), ToolPolicyJSON: row.ToolPolicyJSON, WorkspacePolicyJSON: row.WorkspacePolicyJSON})
	}
	bindings, err := r.repo.ListAgentTeamBindings(ctx, r.tenantID, record.ID, 500)
	if err != nil {
		return agentteam.Team{}, err
	}
	teamBindings := make([]agentteam.Binding, 0, len(bindings))
	for _, row := range bindings {
		teamBindings = append(teamBindings, agentteam.Binding{TenantID: r.tenantID, TeamID: record.ID, TeamVersion: record.TeamVersion, Provider: row.Provider, AccountKey: fmt.Sprint(row.AccountID), ExternalChatID: row.ExternalChatID, ExternalThreadID: row.ExternalThreadID, Trigger: agentteam.TriggerPolicy(row.TriggerPolicy)})
	}
	return agentteam.Team{TenantID: r.tenantID, ID: record.ID, Key: record.TeamKey, Version: record.TeamVersion, Status: agentteam.StatusPublished, EffectiveHash: record.EffectiveHash, Policy: policy, Members: members, Bindings: teamBindings}, nil
}

type channelTeamMemberRunner struct {
	base           cliChannelRunner
	repo           *mysqlstore.GormRepository
	service        *tenantservice.Service
	tenantID       uint64
	userID         uint64
	tenantKey      string
	userKey        string
	conversationID uint64
	sessionID      uint64
	scope          channelcontract.Scope
	message        channelcontract.InboundMessage
	workspace      string
	permissionMode string
	model          string
}

func (r *channelTeamMemberRunner) Run(ctx context.Context, member agentteam.Member, prompt string) (agentteam.MemberResult, error) {
	runner, input, err := r.prepare(ctx, member, prompt)
	if err != nil {
		return agentteam.MemberResult{MemberKey: member.Key, Status: agentteam.StatusFailed, Error: err.Error()}, err
	}
	runResult, err := runner.Run(ctx, input)
	if err != nil {
		return agentteam.MemberResult{MemberKey: member.Key, Status: agentteam.StatusFailed, Error: err.Error()}, err
	}
	return agentteam.MemberResult{MemberKey: member.Key, Status: agentteam.StatusCompleted, Content: runResult.FinalText, Turns: 1}, nil
}

func (r *channelTeamMemberRunner) RunStream(ctx context.Context, member agentteam.Member, prompt string, sink agentteam.TextSink) (agentteam.MemberResult, error) {
	runner, input, err := r.prepare(ctx, member, prompt)
	if err != nil {
		return agentteam.MemberResult{MemberKey: member.Key, Status: agentteam.StatusFailed, Error: err.Error()}, err
	}
	runResult, err := runner.RunStream(ctx, input, teamDeltaSink{sink: sink})
	if err != nil {
		return agentteam.MemberResult{MemberKey: member.Key, Status: agentteam.StatusFailed, Error: err.Error()}, err
	}
	return agentteam.MemberResult{MemberKey: member.Key, Status: agentteam.StatusCompleted, Content: runResult.FinalText, Turns: 1}, nil
}

func (r *channelTeamMemberRunner) prepare(ctx context.Context, member agentteam.Member, prompt string) (cliChannelRunner, channelcontract.RunInput, error) {
	profileCtx := observability.WithRequestValues(ctx, observability.TraceID(ctx), r.userKey, r.tenantKey)
	effective, err := r.service.ResolveAgentProfile(profileCtx, agentprofile.ResolveRequest{Surface: agentprofile.SurfaceChannelTeam, ProfileKey: member.ProfileKey, ProfileVersion: member.ProfileVersion})
	if err != nil {
		return cliChannelRunner{}, channelcontract.RunInput{}, err
	}
	opts := r.base.opts
	if err := applyAgentProfileToOptions(&opts, effective); err != nil {
		return cliChannelRunner{}, channelcontract.RunInput{}, err
	}
	if opts.model == "" {
		opts.model = r.model
	}
	sessionID, err := r.repo.UpsertSession(profileCtx, mysqlstore.SessionInput{TenantID: r.tenantID, UserID: r.userID, SessionKey: fmt.Sprintf("team:%d:%d:%s:%s", r.conversationID, r.userID, r.scope.HashHex(), member.Key), Title: "Team member " + member.Key, Status: "active", Model: opts.model, CWD: r.workspace})
	if err != nil {
		return cliChannelRunner{}, channelcontract.RunInput{}, err
	}
	runner := cliChannelRunner{opts: opts, repo: r.repo, permissionBroker: r.base.permissionBroker}
	return runner, channelcontract.RunInput{TenantID: r.tenantID, UserID: r.userID, TenantSessionID: sessionID, ConversationID: r.conversationID, RunID: "team-member-" + member.Key, Scope: r.scope, RuntimeFingerprint: channelruntime.DefaultFingerprint, Prompt: prompt, Model: opts.model, WorkspaceRealpath: r.workspace, PermissionMode: r.permissionMode, ExternalUserID: r.message.ExternalUserID, ExternalChatID: r.message.ExternalConversationID, ReplyToMessageID: r.message.ExternalMessageID, ChatType: r.message.ChatType, ModelProvider: opts.providerName}, nil
}

type teamDeltaSink struct {
	sink agentteam.TextSink
}

func (s teamDeltaSink) OnDelta(ctx context.Context, delta channelcontract.Delta) error {
	if delta.Kind != channelcontract.DeltaText || s.sink == nil {
		return nil
	}
	return s.sink.OnText(delta.Text)
}
