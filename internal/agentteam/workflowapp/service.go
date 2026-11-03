// Package workflowapp composes persisted Profile/Team resources with the pure
// workflow scheduler. It owns use cases, not HTTP, model protocols or screen.
package workflowapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/konglong87/super-niuma/internal/agentprofile"
	"github.com/konglong87/super-niuma/internal/agentteam/workflow"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

type Repository interface {
	ListAgentTeams(context.Context, uint64, uint64, string, int) ([]mysqlstore.AgentTeam, error)
	ListAgentTeamMembers(context.Context, uint64, uint64, int) ([]mysqlstore.AgentTeamMember, error)
	ListAgentProfiles(context.Context, uint64, uint64, string, int) ([]mysqlstore.AgentProfile, error)
	GetAgentProfile(context.Context, uint64, uint64) (mysqlstore.AgentProfile, error)
	CreateAgentProfile(context.Context, mysqlstore.AgentProfileInput) (mysqlstore.AgentProfile, error)
	CreateWorkflowTeam(context.Context, mysqlstore.AgentTeamInput, []mysqlstore.AgentTeamMemberInput) (mysqlstore.AgentTeam, error)
}
type Preflighter interface {
	Preflight(context.Context, workflow.Agent) (workflow.Agent, error)
}
type Binding struct {
	ProfileID      uint64 `json:"profile_id,omitempty"`
	Ref            string `json:"ref"`
	ProfileKey     string `json:"profile_key"`
	ProfileVersion uint   `json:"profile_version"`
}
type Template struct {
	ID          uint64              `json:"id"`
	Key         string              `json:"key"`
	Version     uint                `json:"version"`
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Definition  workflow.Definition `json:"definition"`
	Agents      []Binding           `json:"agents"`
}
type SaveRequest struct {
	Key         string              `json:"key"`
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Definition  workflow.Definition `json:"definition"`
	Agents      []Binding           `json:"agents"`
}
type StartRequest struct {
	Version uint   `json:"version"`
	Input   string `json:"input"`
}
type Service struct {
	repo     Repository
	store    workflow.Store
	engine   *workflow.Engine
	executor workflow.Executor
	redact   func(string) string
}

func New(repo Repository, store workflow.Store, executor workflow.Executor, redact func(string) string) *Service {
	if redact == nil {
		redact = workflow.Redact
	}
	return &Service{repo: repo, store: store, executor: executor, redact: redact, engine: workflow.NewEngine(store, executor, workflow.EngineOptions{Sanitize: redact})}
}
func (s *Service) Start(ctx context.Context) error { return s.engine.Start(ctx) }
func (s *Service) Close(ctx context.Context) error { return s.engine.Close(ctx) }
func (s *Service) Redact(value string) string      { return s.redact(value) }
func (s *Service) List(ctx context.Context, scope workflow.Scope) ([]Template, error) {
	rows, err := s.repo.ListAgentTeams(ctx, scope.TenantID, scope.UserID, "published", 500)
	if err != nil {
		return nil, err
	}
	out := []Template{}
	seen := map[string]bool{}
	for _, row := range rows {
		if row.SchemaVersion != workflow.SchemaVersion || seen[row.TeamKey] {
			continue
		}
		item, err := s.template(ctx, scope, row)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
		seen[row.TeamKey] = true
	}
	return out, nil
}
func (s *Service) find(ctx context.Context, scope workflow.Scope, key string, version uint) (mysqlstore.AgentTeam, error) {
	rows, err := s.repo.ListAgentTeams(ctx, scope.TenantID, scope.UserID, "published", 500)
	if err != nil {
		return mysqlstore.AgentTeam{}, err
	}
	for _, row := range rows {
		if row.SchemaVersion == workflow.SchemaVersion && row.TeamKey == key && (version == 0 || row.TeamVersion == version) {
			return row, nil
		}
	}
	return mysqlstore.AgentTeam{}, workflow.ErrNotFound
}
func (s *Service) template(ctx context.Context, scope workflow.Scope, row mysqlstore.AgentTeam) (Template, error) {
	def, err := decodeDefinition(row.PolicyJSON)
	if err != nil {
		return Template{}, err
	}
	members, err := s.repo.ListAgentTeamMembers(ctx, scope.TenantID, row.ID, workflow.MaxNodes)
	if err != nil {
		return Template{}, err
	}
	item := Template{ID: row.ID, Key: row.TeamKey, Version: row.TeamVersion, Name: row.DisplayName, Description: row.Description, Definition: def, Agents: []Binding{}}
	for _, member := range members {
		p, err := s.repo.GetAgentProfile(ctx, scope.TenantID, member.ProfileID)
		if err != nil {
			return Template{}, err
		}
		if !profileVisible(p, scope) {
			return Template{}, workflow.ErrNotFound
		}
		item.Agents = append(item.Agents, Binding{Ref: member.MemberKey, ProfileID: p.ID, ProfileKey: p.ProfileKey, ProfileVersion: p.ProfileVersion})
	}
	return item, nil
}
func (s *Service) Get(ctx context.Context, scope workflow.Scope, key string, version uint) (Template, error) {
	row, err := s.find(ctx, scope, key, version)
	if err != nil {
		if errors.Is(err, mysqlstore.ErrInvalidInput) {
			return Template{}, workflow.ErrConflict
		}
		return Template{}, err
	}
	return s.template(ctx, scope, row)
}

var teamKey = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]{0,63}$`)

func (s *Service) Save(ctx context.Context, scope workflow.Scope, req SaveRequest) (Template, error) {
	req.Name = strings.TrimSpace(req.Name)
	req.Description = strings.TrimSpace(req.Description)
	if scope.TenantID == 0 || scope.UserID == 0 {
		return Template{}, workflow.ErrNotFound
	}
	if req.Name == "" || len(req.Name) > 128 || len(req.Description) > 4096 {
		return Template{}, &workflow.ValidationError{Field: "name", Message: "enter a Team name up to 128 bytes and a description up to 4096 bytes"}
	}
	if req.Key == "" {
		req.Key = "workflow-" + uuid.NewString()
	}
	if !teamKey.MatchString(req.Key) {
		return Template{}, &workflow.ValidationError{Field: "key", Message: "invalid Team key"}
	}
	for _, node := range req.Definition.Nodes {
		if strings.TrimSpace(node.Instruction) == "" {
			return Template{}, &workflow.ValidationError{Field: "nodes.instruction", Message: "describe what each step should deliver"}
		}
	}
	agents, err := s.resolve(ctx, scope, req.Agents)
	if err != nil {
		return Template{}, err
	}
	plan, err := workflow.Compile(req.Definition, agents)
	if err != nil {
		return Template{}, err
	}
	version := uint(1)
	rows, err := s.repo.ListAgentTeams(ctx, scope.TenantID, scope.UserID, "", 500)
	if err != nil {
		return Template{}, err
	}
	for _, row := range rows {
		if row.TeamKey != req.Key {
			continue
		}
		if row.SchemaVersion != workflow.SchemaVersion || row.OwnerUserID != scope.UserID {
			return Template{}, workflow.ErrNotFound
		}
		if row.TeamVersion >= version {
			version = row.TeamVersion + 1
		}
	}
	raw, err := json.Marshal(plan.Definition)
	if err != nil {
		return Template{}, err
	}
	now := time.Now().UTC()
	members := make([]mysqlstore.AgentTeamMemberInput, 0, len(plan.Agents))
	for _, a := range plan.Agents {
		members = append(members, mysqlstore.AgentTeamMemberInput{TenantID: scope.TenantID, MemberKey: a.Ref, ProfileID: a.ProfileID, Role: a.RoleType, Status: "active"})
	}
	row, err := s.repo.CreateWorkflowTeam(ctx, mysqlstore.AgentTeamInput{TenantID: scope.TenantID, OwnerUserID: scope.UserID, OwnerKey: fmt.Sprintf("user:%d", scope.UserID), TeamKey: req.Key, TeamVersion: version, Scope: "user_private", DisplayName: s.redact(req.Name), Description: s.redact(req.Description), Status: "published", SchemaVersion: workflow.SchemaVersion, PolicyJSON: string(raw), RequestedHash: plan.Hash, EffectiveHash: plan.Hash, ValidationJSON: `{"valid":true}`, CreatedByUserID: scope.UserID, UpdatedByUserID: scope.UserID, PublishedAt: &now}, members)
	if err != nil {
		return Template{}, err
	}
	return s.template(ctx, scope, row)
}
func profileVisible(p mysqlstore.AgentProfile, scope workflow.Scope) bool {
	return p.TenantID == scope.TenantID && (p.Scope == "tenant_shared" || p.OwnerUserID == scope.UserID)
}
func (s *Service) resolve(ctx context.Context, scope workflow.Scope, bindings []Binding) ([]workflow.Agent, error) {
	if len(bindings) == 0 || len(bindings) > workflow.MaxNodes {
		return nil, &workflow.ValidationError{Field: "agents", Message: "choose 1 to 16 published Agents"}
	}
	profiles, err := s.repo.ListAgentProfiles(ctx, scope.TenantID, scope.UserID, "published", 500)
	if err != nil {
		return nil, err
	}
	out := make([]workflow.Agent, 0, len(bindings))
	for _, binding := range bindings {
		var row mysqlstore.AgentProfile
		found := false
		for _, p := range profiles {
			if p.ProfileKey == binding.ProfileKey && p.ProfileVersion == binding.ProfileVersion && (binding.ProfileID == 0 || p.ID == binding.ProfileID) && profileVisible(p, scope) {
				if !found || p.Scope == "user_private" && p.OwnerUserID == scope.UserID {
					row = p
					found = true
				}
			}
		}
		if !found {
			return nil, &workflow.ValidationError{Field: "agents.profile", Message: "a selected Agent version is not published or is outside your workspace"}
		}
		doc, err := agentprofile.DecodeProfileDocument([]byte(row.ConfigJSON))
		if err != nil {
			return nil, err
		}
		report := agentprofile.Validate(doc, agentprofile.ValidationContext{})
		if !report.Valid {
			return nil, &workflow.ValidationError{Field: "agents.profile", Message: "selected Agent profile is invalid"}
		}
		kind := string(doc.Execution.Kind)
		if kind == "" {
			kind = "text"
		}
		role := doc.Identity.RoleType
		if role == "" {
			role = agentprofile.RoleTypeGeneral
		}
		prompt := strings.TrimSpace(strings.Join([]string{doc.Prompt.Persona, doc.Prompt.SystemAddendum}, "\n\n"))
		out = append(out, workflow.Agent{Ref: binding.Ref, ProfileID: row.ID, ProfileKey: row.ProfileKey, ProfileVersion: row.ProfileVersion, ProfileHash: row.EffectiveHash, Name: row.DisplayName, RoleType: role, Kind: kind, SystemPrompt: s.redact(prompt), Provider: doc.Execution.Provider, Model: doc.Execution.Model, Effort: doc.Execution.Effort, MaxOutputTokens: doc.Execution.MaxTokens, Tools: append([]string{}, doc.Capabilities.Tools.Allow...)})
	}
	return out, nil
}
func decodeDefinition(raw string) (workflow.Definition, error) {
	var d workflow.Definition
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&d); err != nil {
		return d, &workflow.ValidationError{Field: "definition", Message: "invalid workflow definition"}
	}
	return d, nil
}
func (s *Service) StartRun(ctx context.Context, scope workflow.Scope, key string, req StartRequest, requestKey string) (workflow.Run, bool, error) {
	if req.Version == 0 {
		return workflow.Run{}, false, &workflow.ValidationError{Field: "version", Message: "select a fixed Team version"}
	}
	requestBytes, _ := json.Marshal(struct {
		Key     string
		Version uint
		Input   string
	}{key, req.Version, req.Input})
	keyHash, fingerprint, err := workflow.Identity(scope, requestKey, requestBytes)
	if err != nil {
		return workflow.Run{}, false, err
	}
	// Return a committed result before revalidating changed provider credentials.
	if indexed, ok := s.store.(interface {
		ByKey(context.Context, workflow.Scope, string) (workflow.Run, error)
	}); ok {
		existing, findErr := indexed.ByKey(ctx, scope, keyHash)
		if findErr == nil {
			if existing.Fingerprint != fingerprint {
				return workflow.Run{}, false, workflow.ErrIdempotencyConflict
			}
			return existing, false, nil
		}
		if !errors.Is(findErr, workflow.ErrNotFound) {
			return workflow.Run{}, false, findErr
		}
	} else {
		existing, listErr := s.store.List(ctx, scope, workflow.EventPageLimit)
		if listErr != nil {
			return workflow.Run{}, false, listErr
		}
		for _, run := range existing {
			if run.KeyHash == keyHash {
				if run.Fingerprint != fingerprint {
					return workflow.Run{}, false, workflow.ErrIdempotencyConflict
				}
				return run, false, nil
			}
		}
	}
	item, err := s.Get(ctx, scope, key, req.Version)
	if err != nil {
		return workflow.Run{}, false, err
	}
	agents, err := s.resolve(ctx, scope, item.Agents)
	if err != nil {
		return workflow.Run{}, false, err
	}
	if preflight, ok := s.executor.(Preflighter); ok {
		for i, a := range agents {
			resolved, err := preflight.Preflight(ctx, a)
			if err != nil {
				return workflow.Run{}, false, err
			}
			agents[i] = resolved
		}
	}
	plan, err := workflow.Compile(item.Definition, agents)
	if err != nil {
		return workflow.Run{}, false, err
	}
	// Preflight registers credentials in the redactor; sanitize before persistence.
	for i := range plan.Agents {
		plan.Agents[i].SystemPrompt = s.redact(plan.Agents[i].SystemPrompt)
	}
	run := workflow.Run{Scope: scope, TeamID: item.ID, TeamKey: item.Key, TeamVersion: item.Version, Name: item.Name, Input: s.redact(req.Input), Plan: plan, KeyHash: keyHash, Fingerprint: fingerprint}
	return s.engine.Create(ctx, run)
}
func (s *Service) Runs(ctx context.Context, scope workflow.Scope, key string, version uint) ([]workflow.Run, error) {
	if _, err := s.find(ctx, scope, key, version); err != nil {
		return nil, err
	}
	runs, err := s.store.List(ctx, scope, workflow.EventPageLimit)
	if err != nil {
		return nil, err
	}
	out := []workflow.Run{}
	for _, r := range runs {
		if r.TeamKey == key && (version == 0 || r.TeamVersion == version) {
			out = append(out, r)
		}
	}
	return out, nil
}
func (s *Service) Run(ctx context.Context, scope workflow.Scope, id string) (workflow.Run, error) {
	return s.store.Get(ctx, scope, id)
}
func (s *Service) Events(ctx context.Context, scope workflow.Scope, id string, after uint64, limit int) ([]workflow.Event, error) {
	return s.store.Events(ctx, scope, id, after, limit)
}
func (s *Service) Cancel(ctx context.Context, scope workflow.Scope, id string) (workflow.Run, error) {
	return s.engine.Cancel(ctx, scope, id)
}
func (s *Service) Recover(ctx context.Context, scope workflow.Scope, id string, revision uint64) (workflow.Run, error) {
	r, err := s.store.Get(ctx, scope, id)
	if err != nil {
		return workflow.Run{}, err
	}
	if preflight, ok := s.executor.(Preflighter); ok {
		for _, a := range r.Plan.Agents {
			resolved, err := preflight.Preflight(ctx, a)
			if err != nil {
				return workflow.Run{}, err
			}
			if resolved.Provider != a.Provider || resolved.Model != a.Model || resolved.RouteHash != a.RouteHash {
				return workflow.Run{}, &workflow.ValidationError{Field: "model", Message: "the saved model route changed; start a new Run"}
			}
		}
	}
	return s.engine.Recover(ctx, scope, id, revision)
}

// Builtins are editable starting points, not a different execution engine.
type Builtin struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

const (
	TemplateSequential = "research-review"
	TemplateParallel   = "parallel-review"
)

func Builtins() []Builtin {
	return []Builtin{{TemplateSequential, "规划 → 调研 → 审查", "适合资料整理、方案分析与文案审查，按步骤传递结果。"}, {TemplateParallel, "规划 → 双路调研 → 审查", "两位调研员并行分析不同角度，全部完成后统一审查。"}}
}
func (s *Service) CreateBuiltin(ctx context.Context, scope workflow.Scope, key string) (Template, error) {
	var meta Builtin
	for _, b := range Builtins() {
		if b.Key == key {
			meta = b
		}
	}
	if meta.Key == "" {
		return Template{}, workflow.ErrNotFound
	}
	roles := []struct{ ref, role, name, focus string }{
		{agentprofile.RoleTypePlanner, agentprofile.RoleTypePlanner, "规划 / Planner", "梳理目标、输入和约束，制定可执行的有限步骤及验收标准。"},
		{agentprofile.RoleTypeResearcher, agentprofile.RoleTypeResearcher, "调研 / Researcher", "仅根据提供的资料分析可行方案、支持证据和信息缺口。"},
		{agentprofile.RoleTypeReviewer, agentprofile.RoleTypeReviewer, "审查 / Reviewer", "对照目标与约束审查结果，区分事实与假设，交付明确结论。"},
	}
	if key == TemplateParallel {
		roles[1].name = "方案调研 / Researcher"
		roles = append(roles, struct{ ref, role, name, focus string }{"risk_researcher", agentprofile.RoleTypeResearcher, "风险调研 / Risk reviewer", "独立分析风险、反面证据和替代方案，不重复未经证实的结论。"})
	}

	bindings := []Binding{}
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	for _, spec := range roles {
		role := spec.role
		descriptor, _ := agentprofile.LookupRoleType(role)
		doc := agentprofile.ProfileDocument{SchemaVersion: agentprofile.SchemaVersionV2, Identity: agentprofile.IdentityPolicy{RoleType: role, DisplayName: spec.name}, Prompt: agentprofile.PromptPolicy{Mode: agentprofile.PromptModeChat, Persona: spec.focus + " 仅使用本次提供的资料，不声称已经联网或执行工具；清晰区分事实、假设与未知信息，并使用用户的语言回答。"}, Capabilities: agentprofile.CapabilityPolicy{Tools: agentprofile.ToolPolicy{Allow: []string{}, Deny: []string{}}}, Execution: agentprofile.ExecutionPolicy{Kind: agentprofile.ExecutionKindText, MaxTurns: 1, MaxTokens: 2048}, Safety: agentprofile.SafetyPolicy{PermissionMode: agentprofile.PermissionModeDeny, Sandbox: agentprofile.SandboxRequired}}
		report := agentprofile.Validate(doc, agentprofile.ValidationContext{})
		if !report.Valid {
			return Template{}, errors.New("built-in Agent definition failed validation")
		}
		raw, _ := doc.CanonicalJSON()
		hash, _ := doc.CanonicalHash()
		now := time.Now().UTC()
		p, err := s.repo.CreateAgentProfile(ctx, mysqlstore.AgentProfileInput{TenantID: scope.TenantID, OwnerUserID: scope.UserID, OwnerKey: fmt.Sprintf("user:%d", scope.UserID), ProfileKey: "workflow-" + spec.ref + "-" + suffix, ProfileVersion: 1, Scope: "user_private", DisplayName: spec.name, Description: descriptor.Description, Status: "published", ConfigJSON: string(raw), RequestedHash: hash, EffectiveHash: hash, ValidationJSON: `{"valid":true}`, CreatedByUserID: scope.UserID, UpdatedByUserID: scope.UserID, PublishedAt: &now})
		if err != nil {
			return Template{}, err
		}
		bindings = append(bindings, Binding{Ref: spec.ref, ProfileID: p.ID, ProfileKey: p.ProfileKey, ProfileVersion: p.ProfileVersion})
	}
	nodes := []workflow.Node{{ID: "plan", AgentRef: agentprofile.RoleTypePlanner, Instruction: "梳理本次目标、输入资料与约束，给出简明计划和验收标准。"}, {ID: "research", AgentRef: agentprofile.RoleTypeResearcher, Instruction: "按照上一步计划分析用户提供的资料，列出证据、结论和待确认信息；不要假装已联网搜索。", DependsOn: []string{"plan"}}, {ID: "review", AgentRef: agentprofile.RoleTypeReviewer, Instruction: "对照目标审查前面结果，纠正无证据的断言，给出清晰的最终答复和剩余风险。", DependsOn: []string{"research"}}}
	if key == TemplateParallel {
		nodes = []workflow.Node{nodes[0], {ID: "research_a", AgentRef: agentprofile.RoleTypeResearcher, Instruction: "依据规划结果分析可行方案与支持证据，提出最推荐的做法。", DependsOn: []string{"plan"}}, {ID: "research_b", AgentRef: "risk_researcher", Instruction: "独立分析风险、信息缺口和替代方案；说明哪些结论仍需验证。", DependsOn: []string{"plan"}}, {ID: "review", AgentRef: agentprofile.RoleTypeReviewer, Instruction: "比较两路调研结果，处理冲突，给出最终建议、依据和明确的不确定项。", DependsOn: []string{"research_a", "research_b"}}}
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].Ref < bindings[j].Ref })
	return s.Save(ctx, scope, SaveRequest{Name: meta.Name, Description: meta.Description, Definition: workflow.Definition{SchemaVersion: workflow.SchemaVersion, Nodes: nodes}, Agents: bindings})
}
