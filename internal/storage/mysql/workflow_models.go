package mysql

import "time"

const workflowSourceDesktop = "desktop"

// The embedded V1 model keeps a single run identity; only the V2 store writes
// these extensions. NULL idempotency keys leave legacy rows unconstrained.
type gormWorkflowRun struct {
	Legacy             gormAgentTeamRun `gorm:"embedded"`
	SourceKind         string           `gorm:"column:source_kind;default:channel"`
	OwnerUserID        *uint64          `gorm:"column:owner_user_id"`
	WorkflowVersion    uint             `gorm:"column:workflow_version;default:1"`
	WorkflowRevision   uint64           `gorm:"column:workflow_revision;default:0"`
	WorkflowStateJSON  *string          `gorm:"column:workflow_state_json"`
	IdempotencyKeyHash *string          `gorm:"column:idempotency_key_hash"`
	Fingerprint        *string          `gorm:"column:workflow_fingerprint"`
	// Dedicated index fields are declared in SQLite DDL because tenant_id lives
	// in the embedded V1 model and must not alter that model's write contract.
}

func (gormWorkflowRun) TableName() string { return "agent_team_runs" }

type gormWorkflowNode struct {
	TenantID  uint64 `gorm:"primaryKey;autoIncrement:false"`
	RunID     string `gorm:"primaryKey"`
	NodeID    string `gorm:"primaryKey"`
	Status    string
	StateJSON string
}

func (gormWorkflowNode) TableName() string { return "agent_team_workflow_nodes" }

type gormWorkflowAttempt struct {
	TenantID  uint64 `gorm:"primaryKey;autoIncrement:false"`
	RunID     string `gorm:"primaryKey"`
	NodeID    string `gorm:"primaryKey"`
	AttemptNo int    `gorm:"primaryKey;autoIncrement:false"`
	TaskID    uint64 `gorm:"uniqueIndex:uk_workflow_attempt_task"`
	Epoch     uint64
	Status    string
	StateJSON string
}

func (gormWorkflowAttempt) TableName() string { return "agent_team_workflow_attempts" }

type gormWorkflowArtifact struct {
	ID        string `gorm:"primaryKey"`
	TenantID  uint64
	RunID     string
	NodeID    string
	AttemptNo int
	Kind      string
	Content   string
	SHA256    string
	CreatedAt time.Time
}

func (gormWorkflowArtifact) TableName() string { return "agent_team_workflow_artifacts" }

type gormWorkflowTaskBinding struct {
	TaskID    uint64 `gorm:"primaryKey;autoIncrement:false"`
	TenantID  uint64
	UserID    uint64
	RunID     string
	NodeID    string
	AttemptNo int
}

func (gormWorkflowTaskBinding) TableName() string { return "agent_team_workflow_task_bindings" }
