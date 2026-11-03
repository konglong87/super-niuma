package mysql

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/goal"
	"github.com/konglong87/super-niuma/internal/observability"
	"github.com/konglong87/super-niuma/internal/quota"
	"github.com/konglong87/super-niuma/internal/telemetry"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type GormRepository struct {
	db                 *gorm.DB
	lockDB             *sql.DB
	ownsLockDB         bool
	logger             *slog.Logger
	workflowTaskGuards atomic.Bool
}

func isSQLite(db *gorm.DB) bool {
	return db != nil && db.Dialector.Name() == "sqlite"
}

func channelTimeColumn(db *gorm.DB, column string) string {
	if isSQLite(db) {
		return "datetime(" + column + ")"
	}
	return column
}

// IsSQLite reports whether this repository uses the desktop SQLite dialect.
// Callers use this to avoid wiring MySQL-only background workers onto the
// local desktop queue.
func (r *GormRepository) IsSQLite() bool {
	return r != nil && isSQLite(r.db)
}

// jsonTextEqualsPredicate builds a dialect-aware "extract JSON text = ?" predicate.
// MySQL needs JSON_UNQUOTE around JSON_EXTRACT to compare the raw scalar text,
// while SQLite has no JSON_UNQUOTE and json_extract already returns the unquoted
// scalar. Column and path are always package constants, never user input.
func jsonTextEqualsPredicate(db *gorm.DB, column, path string) string {
	if isSQLite(db) {
		return fmt.Sprintf("json_extract(%s, '%s') = ?", column, path)
	}
	return fmt.Sprintf("JSON_UNQUOTE(JSON_EXTRACT(%s, '%s')) = ?", column, path)
}

var _ agenttasks.Store = (*GormRepository)(nil)

type gormTenant struct {
	ID           uint64     `gorm:"column:id;primaryKey"`
	TenantKey    string     `gorm:"column:tenant_key"`
	Name         string     `gorm:"column:name"`
	Status       string     `gorm:"column:status"`
	SettingsJSON *string    `gorm:"column:settings_json"`
	DeletedAt    *time.Time `gorm:"column:deleted_at"`
}

func (gormTenant) TableName() string { return "tenants" }

type gormTenantUserEnsure struct {
	ID        uint64     `gorm:"column:id;primaryKey"`
	TenantID  uint64     `gorm:"column:tenant_id"`
	UserKey   string     `gorm:"column:user_key"`
	CreatedAt time.Time  `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt time.Time  `gorm:"column:updated_at;autoUpdateTime"`
	DeletedAt *time.Time `gorm:"column:deleted_at"`
}

func (gormTenantUserEnsure) TableName() string { return "tenant_users" }

type gormTenantUser struct {
	ID           uint64     `gorm:"column:id;primaryKey"`
	TenantID     uint64     `gorm:"column:tenant_id"`
	UserKey      string     `gorm:"column:user_key"`
	Email        *string    `gorm:"column:email"`
	DisplayName  *string    `gorm:"column:display_name"`
	Role         string     `gorm:"column:role"`
	Status       string     `gorm:"column:status"`
	UserInfoJSON *string    `gorm:"column:user_info_json"`
	MetadataJSON *string    `gorm:"column:metadata_json"`
	DeletedAt    *time.Time `gorm:"column:deleted_at"`
}

func (gormTenantUser) TableName() string { return "tenant_users" }

type gormMemory struct {
	ID           uint64     `gorm:"column:id;primaryKey"`
	TenantID     uint64     `gorm:"column:tenant_id"`
	UserID       uint64     `gorm:"column:user_id"`
	MemoryKey    string     `gorm:"column:memory_key"`
	Category     string     `gorm:"column:category"`
	Content      string     `gorm:"column:content"`
	MetadataJSON *string    `gorm:"column:metadata_json"`
	Importance   int        `gorm:"column:importance"`
	EmbeddingRef *string    `gorm:"column:embedding_ref"`
	Source       *string    `gorm:"column:source"`
	CreatedAt    time.Time  `gorm:"column:created_at"`
	UpdatedAt    time.Time  `gorm:"column:updated_at"`
	DeletedAt    *time.Time `gorm:"column:deleted_at"`
}

func (gormMemory) TableName() string { return "tenant_user_memories" }

type gormSkill struct {
	ID              uint64     `gorm:"column:id;primaryKey"`
	TenantID        uint64     `gorm:"column:tenant_id"`
	SkillKey        string     `gorm:"column:skill_key"`
	Name            string     `gorm:"column:name"`
	Description     *string    `gorm:"column:description"`
	ContentMD       string     `gorm:"column:content_md"`
	ConfigJSON      *string    `gorm:"column:config_json"`
	PackageRef      *string    `gorm:"column:package_ref"`
	PackageSHA256   *string    `gorm:"column:package_sha256"`
	ManifestJSON    *string    `gorm:"column:manifest_json"`
	RuntimeRef      *string    `gorm:"column:runtime_ref"`
	Version         uint       `gorm:"column:version"`
	Enabled         bool       `gorm:"column:enabled"`
	CreatedByUserID *uint64    `gorm:"column:created_by_user_id"`
	DeletedAt       *time.Time `gorm:"column:deleted_at"`
	CreatedAt       time.Time  `gorm:"column:created_at"`
	UpdatedAt       time.Time  `gorm:"column:updated_at"`
}

func (gormSkill) TableName() string { return "tenant_skills" }

type gormSkillOverride struct {
	ID         uint64    `gorm:"column:id;primaryKey"`
	TenantID   uint64    `gorm:"column:tenant_id"`
	UserID     uint64    `gorm:"column:user_id"`
	SkillID    uint64    `gorm:"column:skill_id"`
	Enabled    bool      `gorm:"column:enabled"`
	ConfigJSON *string   `gorm:"column:config_json"`
	CreatedAt  time.Time `gorm:"column:created_at"`
	UpdatedAt  time.Time `gorm:"column:updated_at"`
}

func (gormSkillOverride) TableName() string { return "tenant_user_skill_overrides" }

type gormDocument struct {
	ID          uint64  `gorm:"column:id;primaryKey"`
	TenantID    uint64  `gorm:"column:tenant_id"`
	UserID      uint64  `gorm:"column:user_id"`
	DocType     string  `gorm:"column:doc_type"`
	Title       *string `gorm:"column:title"`
	ContentMD   *string `gorm:"column:content_md"`
	ContentJSON *string `gorm:"column:content_json"`
	Version     uint    `gorm:"column:version"`
	Active      bool    `gorm:"column:is_active"`
}

func (gormDocument) TableName() string { return "tenant_user_documents" }

type gormKnowledgeDocument struct {
	ID           uint64     `gorm:"column:id;primaryKey"`
	TenantID     uint64     `gorm:"column:tenant_id"`
	UserID       uint64     `gorm:"column:user_id"`
	Title        string     `gorm:"column:title"`
	SourceType   string     `gorm:"column:source_type"`
	Content      string     `gorm:"column:content"`
	MetadataJSON *string    `gorm:"column:metadata_json"`
	Status       string     `gorm:"column:status"`
	CreatedAt    time.Time  `gorm:"column:created_at"`
	UpdatedAt    time.Time  `gorm:"column:updated_at"`
	DeletedAt    *time.Time `gorm:"column:deleted_at"`
}

func (gormKnowledgeDocument) TableName() string { return "tenant_knowledge_documents" }

type gormKnowledgeChunk struct {
	ID           uint64     `gorm:"column:id;primaryKey"`
	TenantID     uint64     `gorm:"column:tenant_id"`
	DocumentID   uint64     `gorm:"column:document_id"`
	ChunkIndex   uint       `gorm:"column:chunk_index"`
	Content      string     `gorm:"column:content"`
	MetadataJSON *string    `gorm:"column:metadata_json"`
	EmbeddingRef *string    `gorm:"column:embedding_ref"`
	CreatedAt    time.Time  `gorm:"column:created_at"`
	UpdatedAt    time.Time  `gorm:"column:updated_at"`
	DeletedAt    *time.Time `gorm:"column:deleted_at"`
}

func (gormKnowledgeChunk) TableName() string { return "tenant_knowledge_chunks" }

type gormProfile struct {
	ID                     uint64    `gorm:"column:id;primaryKey"`
	TenantID               uint64    `gorm:"column:tenant_id"`
	UserID                 uint64    `gorm:"column:user_id"`
	ProfileVersion         uint      `gorm:"column:profile_version"`
	Summary                *string   `gorm:"column:summary"`
	ProfileJSON            string    `gorm:"column:profile_json"`
	GeneratedFromSessionID *uint64   `gorm:"column:generated_from_session_id"`
	CreatedAt              time.Time `gorm:"column:created_at"`
}

func (gormProfile) TableName() string { return "tenant_user_profiles" }

type gormSession struct {
	ID            uint64     `gorm:"column:id;primaryKey"`
	TenantID      uint64     `gorm:"column:tenant_id"`
	UserID        uint64     `gorm:"column:user_id"`
	SessionKey    string     `gorm:"column:session_key"`
	Title         *string    `gorm:"column:title"`
	Status        string     `gorm:"column:status"`
	Model         *string    `gorm:"column:model"`
	CWD           *string    `gorm:"column:cwd"`
	MetadataJSON  *string    `gorm:"column:metadata_json"`
	LastMessageAt *time.Time `gorm:"column:last_message_at"`
}

func (gormSession) TableName() string { return "tenant_sessions" }

type gormMessage struct {
	ID           uint64  `gorm:"column:id;primaryKey"`
	TenantID     uint64  `gorm:"column:tenant_id"`
	UserID       uint64  `gorm:"column:user_id"`
	SessionID    uint64  `gorm:"column:session_id"`
	TurnIndex    uint    `gorm:"column:turn_index"`
	Role         string  `gorm:"column:role"`
	Content      *string `gorm:"column:content"`
	ContentJSON  *string `gorm:"column:content_json"`
	ToolID       *string `gorm:"column:tool_id"`
	ToolName     *string `gorm:"column:tool_name"`
	IsError      bool    `gorm:"column:is_error"`
	Model        *string `gorm:"column:model"`
	InputTokens  uint    `gorm:"column:input_tokens"`
	OutputTokens uint    `gorm:"column:output_tokens"`
	TraceID      *string `gorm:"column:trace_id"`
}

func (gormMessage) TableName() string { return "tenant_session_messages" }

type gormAgentTask struct {
	ID                 uint64     `gorm:"column:id;primaryKey"`
	TenantID           uint64     `gorm:"column:tenant_id"`
	UserID             uint64     `gorm:"column:user_id"`
	ParentSessionID    *uint64    `gorm:"column:parent_session_id"`
	SubagentSessionKey *string    `gorm:"column:subagent_session_key"`
	AgentName          *string    `gorm:"column:agent_name"`
	Description        *string    `gorm:"column:description"`
	Prompt             *string    `gorm:"column:prompt"`
	Status             string     `gorm:"column:status"`
	Model              *string    `gorm:"column:model"`
	ResultJSON         *string    `gorm:"column:result_json"`
	MetadataJSON       *string    `gorm:"column:metadata_json"`
	TraceID            *string    `gorm:"column:trace_id"`
	IdempotencyKey     *string    `gorm:"column:idempotency_key"`
	StartedAt          time.Time  `gorm:"column:started_at;->"`
	FinishedAt         *time.Time `gorm:"column:finished_at"`
}

func (gormAgentTask) TableName() string { return "tenant_agent_tasks" }

type gormSessionLink struct {
	ID               uint64    `gorm:"column:id;primaryKey"`
	TenantID         uint64    `gorm:"column:tenant_id"`
	UserID           uint64    `gorm:"column:user_id"`
	TargetSessionID  uint64    `gorm:"column:target_session_id"`
	SourceKind       string    `gorm:"column:source_kind"`
	SourceSessionKey string    `gorm:"column:source_session_key"`
	SourceSessionID  *uint64   `gorm:"column:source_session_id"`
	RelationType     string    `gorm:"column:relation_type"`
	Status           string    `gorm:"column:status"`
	MetadataJSON     *string   `gorm:"column:metadata_json"`
	CreatedByUserID  uint64    `gorm:"column:created_by_user_id"`
	CreatedAt        time.Time `gorm:"column:created_at"`
	UpdatedAt        time.Time `gorm:"column:updated_at"`
}

func (gormSessionLink) TableName() string { return "tenant_session_links" }

type gormAgentTaskEvent struct {
	ID          uint64  `gorm:"column:id;primaryKey"`
	TenantID    uint64  `gorm:"column:tenant_id"`
	UserID      uint64  `gorm:"column:user_id"`
	TaskID      uint64  `gorm:"column:task_id"`
	EventType   string  `gorm:"column:event_type"`
	PayloadJSON *string `gorm:"column:payload_json"`
	TraceID     *string `gorm:"column:trace_id"`
}

func (gormAgentTaskEvent) TableName() string { return "tenant_agent_task_events" }

type gormGoal struct {
	ID                   uint64    `gorm:"column:id;primaryKey"`
	TenantID             uint64    `gorm:"column:tenant_id"`
	UserID               uint64    `gorm:"column:user_id"`
	GoalKey              string    `gorm:"column:goal_key"`
	Objective            string    `gorm:"column:objective"`
	Status               string    `gorm:"column:status"`
	SessionID            *string   `gorm:"column:session_id"`
	CWD                  *string   `gorm:"column:cwd"`
	Model                *string   `gorm:"column:model"`
	TurnBudget           int       `gorm:"column:turn_budget"`
	TokenBudget          int       `gorm:"column:token_budget"`
	TurnsUsed            int       `gorm:"column:turns_used"`
	InputTokens          int       `gorm:"column:input_tokens"`
	OutputTokens         int       `gorm:"column:output_tokens"`
	LastBlocker          *string   `gorm:"column:last_blocker"`
	RepeatedBlockerCount int       `gorm:"column:repeated_blocker_count"`
	LastCheckpoint       *string   `gorm:"column:last_checkpoint"`
	LastReason           *string   `gorm:"column:last_reason"`
	LastNextAction       *string   `gorm:"column:last_next_action"`
	ErrorMessage         *string   `gorm:"column:error_message"`
	CreatedAt            time.Time `gorm:"column:created_at"`
	UpdatedAt            time.Time `gorm:"column:updated_at"`
}

func (gormGoal) TableName() string { return "tenant_goals" }

type gormGoalEvent struct {
	ID           uint64    `gorm:"column:id;primaryKey"`
	TenantID     uint64    `gorm:"column:tenant_id"`
	UserID       uint64    `gorm:"column:user_id"`
	GoalID       uint64    `gorm:"column:goal_id"`
	EventKey     string    `gorm:"column:event_key"`
	EventType    string    `gorm:"column:event_type"`
	Message      *string   `gorm:"column:message"`
	SessionID    *string   `gorm:"column:session_id"`
	TurnIndex    int       `gorm:"column:turn_index"`
	InputTokens  int       `gorm:"column:input_tokens"`
	OutputTokens int       `gorm:"column:output_tokens"`
	DurationMS   int64     `gorm:"column:duration_ms"`
	Checkpoint   *string   `gorm:"column:checkpoint"`
	Status       *string   `gorm:"column:status"`
	Reason       *string   `gorm:"column:reason"`
	NextAction   *string   `gorm:"column:next_action"`
	BlockerKey   *string   `gorm:"column:blocker_key"`
	ErrorMessage *string   `gorm:"column:error_message"`
	CreatedAt    time.Time `gorm:"column:created_at"`
}

func (gormGoalEvent) TableName() string { return "tenant_goal_events" }

type gormGoalPlan struct {
	ID            uint64    `gorm:"column:id;primaryKey"`
	TenantID      uint64    `gorm:"column:tenant_id"`
	UserID        uint64    `gorm:"column:user_id"`
	GoalID        uint64    `gorm:"column:goal_id"`
	Version       int       `gorm:"column:version"`
	Summary       *string   `gorm:"column:summary"`
	CurrentStepID *string   `gorm:"column:current_step_id"`
	PlanJSON      string    `gorm:"column:plan_json"`
	CreatedAt     time.Time `gorm:"column:created_at"`
	UpdatedAt     time.Time `gorm:"column:updated_at"`
}

func (gormGoalPlan) TableName() string { return "tenant_goal_plans" }

type gormGoalEvidence struct {
	ID           uint64    `gorm:"column:id;primaryKey"`
	TenantID     uint64    `gorm:"column:tenant_id"`
	UserID       uint64    `gorm:"column:user_id"`
	GoalID       uint64    `gorm:"column:goal_id"`
	EvidenceKey  string    `gorm:"column:evidence_key"`
	EvidenceType string    `gorm:"column:evidence_type"`
	Summary      string    `gorm:"column:summary"`
	Command      *string   `gorm:"column:command"`
	ExitCode     *int      `gorm:"column:exit_code"`
	Passed       bool      `gorm:"column:passed"`
	PayloadJSON  *string   `gorm:"column:payload_json"`
	CreatedAt    time.Time `gorm:"column:created_at"`
}

func (gormGoalEvidence) TableName() string { return "tenant_goal_evidence" }

type gormAuditLog struct {
	ID           uint64  `gorm:"column:id;primaryKey"`
	TenantID     uint64  `gorm:"column:tenant_id"`
	ActorUserID  *uint64 `gorm:"column:actor_user_id"`
	Action       string  `gorm:"column:action"`
	ResourceType string  `gorm:"column:resource_type"`
	ResourceID   *string `gorm:"column:resource_id"`
	MetadataJSON *string `gorm:"column:metadata_json"`
	TraceID      *string `gorm:"column:trace_id"`
}

func (gormAuditLog) TableName() string { return "tenant_audit_logs" }

type gormTelemetryEvent struct {
	ID                                  uint64     `gorm:"column:id;primaryKey"`
	TenantID                            uint64     `gorm:"column:tenant_id"`
	UserID                              *uint64    `gorm:"column:user_id"`
	EventName                           string     `gorm:"column:event_name"`
	Category                            *string    `gorm:"column:category"`
	Source                              *string    `gorm:"column:source"`
	Status                              *string    `gorm:"column:status"`
	TraceID                             *string    `gorm:"column:trace_id"`
	SessionID                           *uint64    `gorm:"column:session_id"`
	ResourceType                        *string    `gorm:"column:resource_type"`
	ResourceID                          *string    `gorm:"column:resource_id"`
	Model                               *string    `gorm:"column:model"`
	ToolName                            *string    `gorm:"column:tool_name"`
	DurationMS                          *int64     `gorm:"column:duration_ms"`
	InputTokens                         int        `gorm:"column:input_tokens"`
	OutputTokens                        int        `gorm:"column:output_tokens"`
	CacheCreationInputTokens            int        `gorm:"column:cache_creation_input_tokens"`
	CacheReadInputTokens                int        `gorm:"column:cache_read_input_tokens"`
	CacheCreationEphemeral1hInputTokens int        `gorm:"column:cache_creation_ephemeral_1h_input_tokens"`
	CacheCreationEphemeral5mInputTokens int        `gorm:"column:cache_creation_ephemeral_5m_input_tokens"`
	ErrorMessage                        *string    `gorm:"column:error_message"`
	PropertiesJSON                      *string    `gorm:"column:properties_json"`
	OccurredAt                          time.Time  `gorm:"column:occurred_at"`
	CreatedAt                           *time.Time `gorm:"column:created_at"`
}

func (gormTelemetryEvent) TableName() string { return "tenant_telemetry_events" }

func NewGormRepository(db *gorm.DB, logger *slog.Logger) *GormRepository {
	return &GormRepository{db: db, logger: logger}
}

func OpenGormRepository(ctx context.Context, dsn string, logger *slog.Logger) (*GormRepository, error) {
	dsn = DriverDSN(dsn)
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("mysql dsn is required")
	}
	db, err := gorm.Open(gormmysql.Open(dsn), &gorm.Config{SkipDefaultTransaction: true})
	if err != nil {
		return nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	// 池参数必须在第一次用连接之前设好，否则 Ping 建出来的那条就不受管(AUDIT-P0-11)。
	cfg := applyPoolConfig(sqlDB, PoolConfigFromEnv())
	observability.Info(ctx, logger, "mysql.pool", "mysql.OpenGormRepository", "mysql connection pool configured",
		"max_open_conns", cfg.MaxOpenConns,
		"max_idle_conns", cfg.MaxIdleConns,
		"conn_max_lifetime", cfg.ConnMaxLifetime.String(),
		"conn_max_idle_time", cfg.ConnMaxIdleTime.String(),
	)
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	lockDB, err := sql.Open("mysql", dsn)
	if err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	applyPoolConfig(lockDB, cfg)
	if err := lockDB.PingContext(ctx); err != nil {
		_ = lockDB.Close()
		_ = sqlDB.Close()
		return nil, err
	}
	return &GormRepository{db: db, lockDB: lockDB, ownsLockDB: true, logger: logger}, nil
}

// PoolStats 交出底层连接池的实时状态，用于验证池参数确实生效以及排查连接耗尽。
func (r *GormRepository) PoolStats() (sql.DBStats, error) {
	if r == nil || r.db == nil {
		return sql.DBStats{}, errors.New("repository is not open")
	}
	sqlDB, err := r.db.DB()
	if err != nil {
		return sql.DBStats{}, err
	}
	return sqlDB.Stats(), nil
}

func (r *GormRepository) Close() error {
	if r == nil || r.db == nil {
		return nil
	}
	sqlDB, err := r.db.DB()
	if err != nil {
		return err
	}
	var lockErr error
	if r.ownsLockDB && r.lockDB != nil && r.lockDB != sqlDB {
		lockErr = r.lockDB.Close()
	}
	dbErr := sqlDB.Close()
	if lockErr != nil {
		return lockErr
	}
	return dbErr
}

func (r *GormRepository) GetTenantByKey(ctx context.Context, tenantKey string) (Tenant, error) {
	r.log(ctx, "tenant.get", "mysql.GormRepository.GetTenantByKey", "get tenant by key", "tenantkey", tenantKey)
	var tenant Tenant
	err := r.with(ctx).Table("tenants").
		Select("id, tenant_key, name, status, COALESCE(settings_json, '') AS settings_json").
		Where("tenant_key = ? AND deleted_at IS NULL", tenantKey).
		Limit(1).
		Take(&tenant).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Tenant{}, ErrNotFound
	}
	return tenant, err
}

func (r *GormRepository) UpsertTenant(ctx context.Context, input TenantInput) (uint64, error) {
	r.log(ctx, "tenant.upsert", "mysql.GormRepository.UpsertTenant", "upsert tenant", "tenantkey", input.TenantKey)
	if input.Status == "" {
		input.Status = "active"
	}
	if input.Name == "" {
		input.Name = input.TenantKey
	}
	row := gormTenant{
		TenantKey:    input.TenantKey,
		Name:         input.Name,
		Status:       input.Status,
		SettingsJSON: nullableStringPtr(input.SettingsJSON),
	}
	if err := r.with(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "tenant_key"}},
		DoUpdates: clause.Assignments(map[string]any{
			"name":          row.Name,
			"status":        row.Status,
			"settings_json": row.SettingsJSON,
			"deleted_at":    nil,
		}),
	}).Create(&row).Error; err != nil {
		return 0, err
	}
	tenant, err := r.GetTenantByKey(ctx, input.TenantKey)
	if err != nil {
		return 0, err
	}
	return tenant.ID, nil
}

func (r *GormRepository) ArchiveTenant(ctx context.Context, tenantKey string) error {
	r.log(ctx, "tenant.archive", "mysql.GormRepository.ArchiveTenant", "archive tenant", "tenantkey", tenantKey)
	result := r.with(ctx).Table("tenants").
		Where("tenant_key = ? AND deleted_at IS NULL", tenantKey).
		Updates(map[string]any{"status": "archived", "deleted_at": time.Now().UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *GormRepository) ListTenants(ctx context.Context, limit int) ([]Tenant, error) {
	r.log(ctx, "tenant.list", "mysql.GormRepository.ListTenants", "list tenants")
	var tenants []Tenant
	err := r.with(ctx).Table("tenants").
		Select("id, tenant_key, name, status, COALESCE(settings_json, '') AS settings_json").
		Where("deleted_at IS NULL").
		Order("tenant_key ASC").
		Limit(normalizeLimit(limit)).
		Find(&tenants).Error
	return tenants, err
}

func (r *GormRepository) ListTenantIDs(ctx context.Context, limit int) ([]uint64, error) {
	if limit <= 0 || limit > 5000 {
		limit = 5000
	}
	var ids []uint64
	if err := r.with(ctx).Table("tenants").Select("id").Where("deleted_at IS NULL").Order("id ASC").Limit(limit).Scan(&ids).Error; err != nil {
		return nil, err
	}
	return ids, nil
}

func (r *GormRepository) ListTenantsFiltered(ctx context.Context, opts ListOptions) ([]Tenant, error) {
	r.log(ctx, "tenant.list_filtered", "mysql.GormRepository.ListTenantsFiltered", "list tenants filtered")
	var tenants []Tenant
	query := r.with(ctx).Table("tenants").
		Select("id, tenant_key, name, status, COALESCE(settings_json, '') AS settings_json").
		Where("deleted_at IS NULL")
	if search, pattern := normalizeSearch(opts.Search); search != "" {
		query = query.Where("tenant_key LIKE ? OR name LIKE ? OR status LIKE ?", pattern, pattern, pattern)
	}
	err := query.
		Order("tenant_key ASC").
		Limit(normalizeLimit(opts.Limit)).
		Offset(int(opts.Cursor)).
		Find(&tenants).Error
	return tenants, err
}

func (r *GormRepository) UpsertUser(ctx context.Context, input UserInput) (uint64, error) {
	r.log(ctx, "user.upsert", "mysql.GormRepository.UpsertUser", "upsert tenant user")
	if input.Role == "" {
		input.Role = "member"
	}
	if input.Status == "" {
		input.Status = "active"
	}
	row := gormTenantUser{
		TenantID:     input.TenantID,
		UserKey:      input.UserKey,
		Email:        nullableStringPtr(input.Email),
		DisplayName:  nullableStringPtr(input.DisplayName),
		Role:         input.Role,
		Status:       input.Status,
		UserInfoJSON: nullableStringPtr(input.UserInfoJSON),
		MetadataJSON: nullableStringPtr(input.MetadataJSON),
	}
	db := r.with(ctx)
	if isSQLite(db) {
		err := db.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "tenant_id"}, {Name: "user_key"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"email", "display_name", "role", "status", "user_info_json", "metadata_json", "deleted_at",
			}),
		}).Create(&row).Error
		if err != nil {
			return 0, err
		}
		return r.userIDByKey(ctx, input.TenantID, input.UserKey)
	}
	err := db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "tenant_id"}, {Name: "user_key"}},
		DoUpdates: clause.Assignments(map[string]any{
			"id":             gorm.Expr("LAST_INSERT_ID(id)"),
			"email":          gorm.Expr("VALUES(email)"),
			"display_name":   gorm.Expr("VALUES(display_name)"),
			"role":           gorm.Expr("VALUES(role)"),
			"status":         gorm.Expr("VALUES(status)"),
			"user_info_json": gorm.Expr("VALUES(user_info_json)"),
			"metadata_json":  gorm.Expr("VALUES(metadata_json)"),
		}),
	}).Create(&row).Error
	if err != nil || row.ID != 0 {
		return gormInsertedID(row.ID, err)
	}
	return r.userIDByKey(ctx, input.TenantID, input.UserKey)
}

func (r *GormRepository) EnsureUser(ctx context.Context, tenantID uint64, userKey string) (uint64, error) {
	r.log(ctx, "user.ensure", "mysql.GormRepository.EnsureUser", "ensure tenant user identity")
	row := gormTenantUserEnsure{TenantID: tenantID, UserKey: userKey}
	db := r.with(ctx)
	if isSQLite(db) {
		err := db.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "user_key"}},
			DoUpdates: clause.Assignments(map[string]any{"deleted_at": nil}),
		}).Create(&row).Error
		if err != nil {
			return 0, err
		}
		return r.userIDByKey(ctx, tenantID, userKey)
	}
	err := db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "tenant_id"}, {Name: "user_key"}},
		DoUpdates: clause.Assignments(map[string]any{
			"id":         gorm.Expr("LAST_INSERT_ID(id)"),
			"deleted_at": nil,
		}),
	}).Create(&row).Error
	if err != nil || row.ID != 0 {
		return gormInsertedID(row.ID, err)
	}
	return r.userIDByKey(ctx, tenantID, userKey)
}

func (r *GormRepository) SetUserRole(ctx context.Context, tenantID, userID uint64, role string) error {
	r.log(ctx, "user.role.set", "mysql.GormRepository.SetUserRole", "set tenant user role")
	role = strings.TrimSpace(role)
	if tenantID == 0 || userID == 0 || role == "" {
		return ErrInvalidInput
	}
	result := r.with(ctx).Table("tenant_users").
		Where("tenant_id = ? AND id = ? AND deleted_at IS NULL", tenantID, userID).
		Updates(map[string]any{
			"role":       role,
			"updated_at": gorm.Expr("CURRENT_TIMESTAMP"),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *GormRepository) userIDByKey(ctx context.Context, tenantID uint64, userKey string) (uint64, error) {
	var out struct {
		ID uint64 `gorm:"column:id"`
	}
	err := r.with(ctx).Table("tenant_users").
		Select("id").
		Where("tenant_id = ? AND user_key = ? AND deleted_at IS NULL", tenantID, userKey).
		Limit(1).
		Take(&out).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	return gormInsertedID(out.ID, nil)
}

func (r *GormRepository) ArchiveUser(ctx context.Context, tenantID uint64, userKey string) error {
	r.log(ctx, "user.archive", "mysql.GormRepository.ArchiveUser", "archive tenant user")
	res := r.with(ctx).Table("tenant_users").
		Where("tenant_id = ? AND user_key = ? AND deleted_at IS NULL", tenantID, userKey).
		Updates(map[string]any{"status": "archived", "deleted_at": gorm.Expr("CURRENT_TIMESTAMP(6)")})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *GormRepository) GetUser(ctx context.Context, tenantID, userID uint64) (User, error) {
	r.log(ctx, "user.get", "mysql.GormRepository.GetUser", "get tenant user")
	user, err := scanUser(r.with(ctx).Table("tenant_users").
		Select("id, tenant_id, user_key, email, display_name, role, status, user_info_json, metadata_json, updated_at").
		Where("tenant_id = ? AND id = ? AND deleted_at IS NULL", tenantID, userID).
		Limit(1).
		Row())
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return user, err
}

func (r *GormRepository) ListUsers(ctx context.Context, tenantID uint64, limit int) ([]User, error) {
	r.log(ctx, "user.list", "mysql.GormRepository.ListUsers", "list tenant users")
	rows, err := r.with(ctx).Table("tenant_users").
		Select("id, tenant_id, user_key, email, display_name, role, status, user_info_json, metadata_json, updated_at").
		Where("tenant_id = ? AND deleted_at IS NULL", tenantID).
		Order("updated_at DESC").
		Limit(normalizeLimit(limit)).
		Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanUsers(rows)
}

func (r *GormRepository) ListUsersFiltered(ctx context.Context, tenantID uint64, opts ListOptions) ([]User, error) {
	r.log(ctx, "user.list_filtered", "mysql.GormRepository.ListUsersFiltered", "list tenant users filtered")
	query := r.with(ctx).Table("tenant_users").
		Select("id, tenant_id, user_key, email, display_name, role, status, user_info_json, metadata_json, updated_at").
		Where("tenant_id = ? AND deleted_at IS NULL", tenantID)
	if search, pattern := normalizeSearch(opts.Search); search != "" {
		query = query.Where("user_key LIKE ? OR email LIKE ? OR display_name LIKE ? OR role LIKE ? OR status LIKE ?", pattern, pattern, pattern, pattern, pattern)
	}
	rows, err := query.
		Order("updated_at DESC").
		Limit(normalizeLimit(opts.Limit)).
		Offset(int(opts.Cursor)).
		Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanUsers(rows)
}

func (r *GormRepository) UpsertMemory(ctx context.Context, input MemoryInput) (uint64, error) {
	r.log(ctx, "memory.upsert", "mysql.GormRepository.UpsertMemory", "upsert user memory")
	if input.Category == "" {
		input.Category = "general"
	}
	row := gormMemory{
		TenantID:     input.TenantID,
		UserID:       input.UserID,
		MemoryKey:    input.MemoryKey,
		Category:     input.Category,
		Content:      input.Content,
		MetadataJSON: nullableStringPtr(input.MetadataJSON),
		Importance:   input.Importance,
		EmbeddingRef: nullableStringPtr(input.EmbeddingRef),
		Source:       nullableStringPtr(input.Source),
	}
	if isSQLite(r.db) {
		var existing gormMemory
		err := r.with(ctx).Where("tenant_id = ? AND user_id = ? AND memory_key = ?", input.TenantID, input.UserID, input.MemoryKey).Take(&existing).Error
		if err == nil {
			err = r.with(ctx).Table("tenant_user_memories").Where("id = ?", existing.ID).Updates(map[string]any{
				"category": input.Category, "content": input.Content, "metadata_json": row.MetadataJSON,
				"importance": input.Importance, "embedding_ref": row.EmbeddingRef, "source": row.Source, "deleted_at": nil,
			}).Error
			return existing.ID, err
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, err
		}
		if err := r.with(ctx).Create(&row).Error; err != nil {
			return 0, err
		}
		return row.ID, nil
	}
	createErr := r.with(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "tenant_id"}, {Name: "user_id"}, {Name: "memory_key"}},
		DoUpdates: clause.Assignments(map[string]any{
			"id":            gorm.Expr("LAST_INSERT_ID(id)"),
			"category":      gorm.Expr("VALUES(category)"),
			"content":       gorm.Expr("VALUES(content)"),
			"metadata_json": gorm.Expr("VALUES(metadata_json)"),
			"importance":    gorm.Expr("VALUES(importance)"),
			"embedding_ref": gorm.Expr("VALUES(embedding_ref)"),
			"source":        gorm.Expr("VALUES(source)"),
			"deleted_at":    nil,
		}),
	}).Create(&row).Error
	id, err := gormInsertedID(row.ID, createErr)
	if err == nil || createErr != nil {
		return id, err
	}
	return r.memoryID(ctx, input.TenantID, input.UserID, input.MemoryKey)
}

func (r *GormRepository) memoryID(ctx context.Context, tenantID, userID uint64, memoryKey string) (uint64, error) {
	var id uint64
	err := r.with(ctx).Table("tenant_user_memories").
		Select("id").
		Where("tenant_id = ? AND user_id = ? AND memory_key = ? AND deleted_at IS NULL", tenantID, userID, memoryKey).
		Limit(1).
		Scan(&id).Error
	return gormInsertedID(id, err)
}

func (r *GormRepository) ListMemories(ctx context.Context, tenantID, userID uint64, category string, limit int) ([]Memory, error) {
	r.log(ctx, "memory.list", "mysql.GormRepository.ListMemories", "list user memories")
	query := r.with(ctx).Table("tenant_user_memories").
		Select("id, memory_key, category, content, COALESCE(metadata_json, ''), importance, COALESCE(source, ''), updated_at").
		Where("tenant_id = ? AND user_id = ? AND deleted_at IS NULL", tenantID, userID)
	if strings.TrimSpace(category) != "" {
		query = query.Where("category = ?", category)
	}
	rows, err := query.Order("importance DESC, updated_at DESC").Limit(normalizeLimit(limit)).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Memory
	for rows.Next() {
		var item Memory
		if err := rows.Scan(&item.ID, &item.MemoryKey, &item.Category, &item.Content, &item.MetadataJSON, &item.Importance, &item.Source, &item.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *GormRepository) UpsertSkill(ctx context.Context, input SkillInput) (uint64, error) {
	r.log(ctx, "skill.upsert", "mysql.GormRepository.UpsertSkill", "upsert tenant skill")
	if input.Version == 0 {
		input.Version = 1
	}
	row := gormSkill{
		TenantID:        input.TenantID,
		SkillKey:        input.SkillKey,
		Name:            input.Name,
		Description:     nullableStringPtr(input.Description),
		ContentMD:       input.ContentMD,
		ConfigJSON:      nullableStringPtr(input.ConfigJSON),
		PackageRef:      nullableStringPtr(input.PackageRef),
		PackageSHA256:   nullableStringPtr(input.PackageSHA256),
		ManifestJSON:    nullableStringPtr(input.ManifestJSON),
		RuntimeRef:      nullableStringPtr(input.RuntimeRef),
		Version:         input.Version,
		Enabled:         input.Enabled,
		CreatedByUserID: nullableUint64Ptr(input.CreatedByUserID),
	}
	conflictUpdates := map[string]any{
		"id":                 gorm.Expr("LAST_INSERT_ID(id)"),
		"name":               gorm.Expr("VALUES(name)"),
		"description":        gorm.Expr("VALUES(description)"),
		"content_md":         gorm.Expr("VALUES(content_md)"),
		"config_json":        gorm.Expr("VALUES(config_json)"),
		"package_ref":        gorm.Expr("VALUES(package_ref)"),
		"package_sha256":     gorm.Expr("VALUES(package_sha256)"),
		"manifest_json":      gorm.Expr("VALUES(manifest_json)"),
		"runtime_ref":        gorm.Expr("VALUES(runtime_ref)"),
		"enabled":            gorm.Expr("VALUES(enabled)"),
		"created_by_user_id": gorm.Expr("COALESCE(VALUES(created_by_user_id), created_by_user_id)"),
		"deleted_at":         nil,
	}
	if isSQLite(r.db) {
		conflictUpdates = map[string]any{
			"name":               gorm.Expr("excluded.name"),
			"description":        gorm.Expr("excluded.description"),
			"content_md":         gorm.Expr("excluded.content_md"),
			"config_json":        gorm.Expr("excluded.config_json"),
			"package_ref":        gorm.Expr("excluded.package_ref"),
			"package_sha256":     gorm.Expr("excluded.package_sha256"),
			"manifest_json":      gorm.Expr("excluded.manifest_json"),
			"runtime_ref":        gorm.Expr("excluded.runtime_ref"),
			"enabled":            gorm.Expr("excluded.enabled"),
			"created_by_user_id": gorm.Expr("COALESCE(excluded.created_by_user_id, created_by_user_id)"),
			"deleted_at":         nil,
		}
	}
	createErr := r.with(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "skill_key"}, {Name: "version"}},
		DoUpdates: clause.Assignments(conflictUpdates),
	}).Create(&row).Error
	id, err := gormInsertedID(row.ID, createErr)
	if err == nil || createErr != nil {
		return id, err
	}
	return r.skillID(ctx, input.TenantID, input.SkillKey, input.Version)
}

func (r *GormRepository) ListSkills(ctx context.Context, tenantID uint64, enabledOnly bool, limit int) ([]Skill, error) {
	r.log(ctx, "skill.list", "mysql.GormRepository.ListSkills", "list tenant skills")
	query := r.with(ctx).Table("tenant_skills").
		Select("id, skill_key, name, description, content_md, config_json, package_ref, package_sha256, manifest_json, runtime_ref, version, enabled, updated_at").
		Where("tenant_id = ? AND deleted_at IS NULL", tenantID)
	if enabledOnly {
		query = query.Where("enabled = ?", true)
	}
	rows, err := query.Order("skill_key ASC, version DESC").Limit(normalizeLimit(limit)).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSkills(rows)
}

func (r *GormRepository) GetSkill(ctx context.Context, tenantID uint64, skillKey string, version uint) (Skill, error) {
	r.log(ctx, "skill.get", "mysql.GormRepository.GetSkill", "get tenant skill")
	query := r.with(ctx).Table("tenant_skills").
		Select("id, skill_key, name, description, content_md, config_json, package_ref, package_sha256, manifest_json, runtime_ref, version, enabled, updated_at").
		Where("tenant_id = ? AND skill_key = ? AND deleted_at IS NULL", tenantID, skillKey)
	if version == 0 {
		query = query.Order("version DESC")
	} else {
		query = query.Where("version = ?", version)
	}
	skill, err := scanSkill(query.Limit(1).Row())
	if errors.Is(err, sql.ErrNoRows) {
		return Skill{}, ErrNotFound
	}
	return skill, err
}

func (r *GormRepository) RollbackSkillVersion(ctx context.Context, input SkillRollbackInput) (Skill, error) {
	r.log(ctx, "skill.rollback", "mysql.GormRepository.RollbackSkillVersion", "rollback tenant skill version")
	if input.FromVersion == 0 {
		return Skill{}, ErrNotFound
	}
	var out Skill
	err := withRetryableTransaction(ctx, func() error {
		return r.with(ctx).Transaction(func(tx *gorm.DB) error {
			lockedSkillQuery := func() *gorm.DB {
				return tx.Clauses(clause.Locking{Strength: "UPDATE"}).Table("tenant_skills").
					Select("id, skill_key, name, description, content_md, config_json, package_ref, package_sha256, manifest_json, runtime_ref, version, enabled, updated_at").
					Where("tenant_id = ? AND skill_key = ? AND deleted_at IS NULL", input.TenantID, input.SkillKey)
			}
			latest, err := scanSkill(lockedSkillQuery().Order("version DESC").Limit(1).Row())
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			if err != nil {
				return err
			}
			nextVersion := latest.Version + 1
			if input.TargetVersion > 0 {
				if input.TargetVersion <= latest.Version {
					return fmt.Errorf("%w: target version must be greater than latest version %d", ErrInvalidInput, latest.Version)
				}
				nextVersion = input.TargetVersion
			}
			source, err := scanSkill(lockedSkillQuery().Where("version = ?", input.FromVersion).Limit(1).Row())
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			if err != nil {
				return err
			}
			row := gormSkill{
				TenantID:        input.TenantID,
				SkillKey:        source.SkillKey,
				Name:            source.Name,
				Description:     nullableStringPtr(source.Description),
				ContentMD:       source.ContentMD,
				ConfigJSON:      nullableStringPtr(source.ConfigJSON),
				PackageRef:      nullableStringPtr(source.PackageRef),
				PackageSHA256:   nullableStringPtr(source.PackageSHA256),
				ManifestJSON:    nullableStringPtr(source.ManifestJSON),
				RuntimeRef:      nullableStringPtr(source.RuntimeRef),
				Version:         nextVersion,
				Enabled:         source.Enabled,
				CreatedByUserID: nullableUint64Ptr(input.CreatedByUserID),
			}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
			out = source
			out.ID = row.ID
			out.Version = nextVersion
			out.UpdatedAt = time.Time{}
			return nil
		})
	})
	if err != nil {
		return Skill{}, err
	}
	return out, nil
}

func (r *GormRepository) UpsertSkillOverride(ctx context.Context, input SkillOverrideInput) (uint64, error) {
	r.log(ctx, "skill_override.upsert", "mysql.GormRepository.UpsertSkillOverride", "upsert user skill override")
	row := gormSkillOverride{
		TenantID:   input.TenantID,
		UserID:     input.UserID,
		SkillID:    input.SkillID,
		Enabled:    input.Enabled,
		ConfigJSON: nullableStringPtr(input.ConfigJSON),
	}
	if isSQLite(r.db) {
		var existing gormSkillOverride
		err := r.with(ctx).Where("tenant_id = ? AND user_id = ? AND skill_id = ?", input.TenantID, input.UserID, input.SkillID).Take(&existing).Error
		if err == nil {
			err = r.with(ctx).Table("tenant_user_skill_overrides").Where("id = ?", existing.ID).Updates(map[string]any{
				"enabled": input.Enabled, "config_json": row.ConfigJSON, "updated_at": time.Now().UTC(),
			}).Error
			return existing.ID, err
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, err
		}
		if err := r.with(ctx).Create(&row).Error; err != nil {
			return 0, err
		}
		return row.ID, nil
	}
	createErr := r.with(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "tenant_id"}, {Name: "user_id"}, {Name: "skill_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"id":          gorm.Expr("LAST_INSERT_ID(id)"),
			"enabled":     gorm.Expr("VALUES(enabled)"),
			"config_json": gorm.Expr("VALUES(config_json)"),
			"updated_at":  gorm.Expr("CURRENT_TIMESTAMP(6)"),
		}),
	}).Create(&row).Error
	id, err := gormInsertedID(row.ID, createErr)
	if err == nil || createErr != nil {
		return id, err
	}
	return r.skillOverrideID(ctx, input.TenantID, input.UserID, input.SkillID)
}

func (r *GormRepository) skillID(ctx context.Context, tenantID uint64, skillKey string, version uint) (uint64, error) {
	var id uint64
	err := r.with(ctx).Table("tenant_skills").
		Select("id").
		Where("tenant_id = ? AND skill_key = ? AND version = ? AND deleted_at IS NULL", tenantID, skillKey, version).
		Limit(1).
		Scan(&id).Error
	return gormInsertedID(id, err)
}

func (r *GormRepository) skillOverrideID(ctx context.Context, tenantID, userID, skillID uint64) (uint64, error) {
	var id uint64
	err := r.with(ctx).Table("tenant_user_skill_overrides").
		Select("id").
		Where("tenant_id = ? AND user_id = ? AND skill_id = ?", tenantID, userID, skillID).
		Limit(1).
		Scan(&id).Error
	return gormInsertedID(id, err)
}

func (r *GormRepository) ListSkillOverrides(ctx context.Context, tenantID, userID uint64, limit int) ([]SkillOverride, error) {
	r.log(ctx, "skill_override.list", "mysql.GormRepository.ListSkillOverrides", "list user skill overrides")
	rows, err := r.skillOverrideQuery(ctx).
		Where("o.tenant_id = ? AND o.user_id = ? AND s.deleted_at IS NULL", tenantID, userID).
		Order("o.updated_at DESC").
		Limit(normalizeLimit(limit)).
		Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSkillOverrides(rows)
}

func (r *GormRepository) GetSkillOverride(ctx context.Context, tenantID, userID uint64, skillKey string, version uint) (SkillOverride, error) {
	r.log(ctx, "skill_override.get", "mysql.GormRepository.GetSkillOverride", "get user skill override")
	query := r.skillOverrideQuery(ctx).
		Where("o.tenant_id = ? AND o.user_id = ? AND s.skill_key = ? AND s.deleted_at IS NULL", tenantID, userID, skillKey)
	if version == 0 {
		query = query.Order("s.version DESC")
	} else {
		query = query.Where("s.version = ?", version)
	}
	override, err := scanSkillOverride(query.Limit(1).Row())
	if errors.Is(err, sql.ErrNoRows) {
		return SkillOverride{}, ErrNotFound
	}
	return override, err
}

func (r *GormRepository) ListEffectiveSkills(ctx context.Context, tenantID, userID uint64, enabledOnly bool, limit int) ([]EffectiveSkill, error) {
	r.log(ctx, "skill.effective_list", "mysql.GormRepository.ListEffectiveSkills", "list effective user skills")
	query := r.effectiveSkillQuery(ctx, userID).
		Where("s.tenant_id = ? AND s.deleted_at IS NULL", tenantID)
	if enabledOnly {
		query = query.Where("COALESCE(o.enabled, s.enabled) = ?", true)
	}
	rows, err := query.Order("s.skill_key ASC, s.version DESC").Limit(normalizeLimit(limit)).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEffectiveSkills(rows)
}

func (r *GormRepository) GetEffectiveSkill(ctx context.Context, tenantID, userID uint64, skillKey string, version uint) (EffectiveSkill, error) {
	r.log(ctx, "skill.effective_get", "mysql.GormRepository.GetEffectiveSkill", "get effective user skill")
	query := r.effectiveSkillQuery(ctx, userID).
		Where("s.tenant_id = ? AND s.skill_key = ? AND s.deleted_at IS NULL", tenantID, skillKey)
	if version == 0 {
		query = query.Order("s.version DESC")
	} else {
		query = query.Where("s.version = ?", version)
	}
	skill, err := scanEffectiveSkill(query.Limit(1).Row())
	if errors.Is(err, sql.ErrNoRows) {
		return EffectiveSkill{}, ErrNotFound
	}
	return skill, err
}

func (r *GormRepository) SaveDocument(ctx context.Context, input DocumentInput) (uint64, error) {
	r.log(ctx, "document.save", "mysql.GormRepository.SaveDocument", "save user document")
	if input.Version == 0 {
		input.Version = 1
	}
	row := gormDocument{
		TenantID:    input.TenantID,
		UserID:      input.UserID,
		DocType:     input.DocType,
		Title:       nullableStringPtr(input.Title),
		ContentMD:   nullableStringPtr(input.ContentMD),
		ContentJSON: nullableStringPtr(input.ContentJSON),
		Version:     input.Version,
		Active:      input.Active,
	}
	err := r.with(ctx).Create(&row).Error
	return gormInsertedID(row.ID, err)
}

func (r *GormRepository) GetActiveDocument(ctx context.Context, tenantID, userID uint64, docType string) (Document, error) {
	r.log(ctx, "document.get_active", "mysql.GormRepository.GetActiveDocument", "get active user document")
	doc, err := scanDocument(r.with(ctx).Table("tenant_user_documents").
		Select("id, doc_type, title, content_md, content_json, version, is_active").
		Where("tenant_id = ? AND user_id = ? AND doc_type = ? AND is_active = ?", tenantID, userID, docType, true).
		Order("version DESC").
		Limit(1).
		Row())
	if errors.Is(err, sql.ErrNoRows) {
		return Document{}, ErrNotFound
	}
	return doc, err
}

func (r *GormRepository) ListDocuments(ctx context.Context, tenantID, userID uint64, docType string, limit int) ([]Document, error) {
	r.log(ctx, "document.list", "mysql.GormRepository.ListDocuments", "list user documents")
	query := r.with(ctx).Table("tenant_user_documents").
		Select("id, doc_type, title, content_md, content_json, version, is_active").
		Where("tenant_id = ? AND user_id = ?", tenantID, userID)
	if strings.TrimSpace(docType) != "" {
		query = query.Where("doc_type = ?", docType).Order("version DESC")
	} else {
		query = query.Order("doc_type ASC, version DESC")
	}
	rows, err := query.Limit(normalizeLimit(limit)).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDocuments(rows)
}

func (r *GormRepository) SaveKnowledgeDocument(ctx context.Context, input KnowledgeDocumentInput, chunks []KnowledgeChunkInput) (uint64, error) {
	r.log(ctx, "knowledge.document.save", "mysql.GormRepository.SaveKnowledgeDocument", "save tenant knowledge document")
	if input.SourceType == "" {
		input.SourceType = "manual"
	}
	if input.Status == "" {
		input.Status = "active"
	}
	var id uint64
	err := r.with(ctx).Transaction(func(tx *gorm.DB) error {
		row := gormKnowledgeDocument{
			TenantID:     input.TenantID,
			UserID:       input.UserID,
			Title:        input.Title,
			SourceType:   input.SourceType,
			Content:      input.Content,
			MetadataJSON: nullableStringPtr(input.MetadataJSON),
			Status:       input.Status,
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		id = row.ID
		for _, chunk := range chunks {
			if strings.TrimSpace(chunk.Content) == "" {
				continue
			}
			item := gormKnowledgeChunk{
				TenantID:     input.TenantID,
				DocumentID:   row.ID,
				ChunkIndex:   chunk.ChunkIndex,
				Content:      chunk.Content,
				MetadataJSON: nullableStringPtr(chunk.MetadataJSON),
				EmbeddingRef: nullableStringPtr(chunk.EmbeddingRef),
			}
			if err := tx.Create(&item).Error; err != nil {
				return err
			}
		}
		return nil
	})
	return gormInsertedID(id, err)
}

func (r *GormRepository) ListKnowledgeDocuments(ctx context.Context, tenantID, userID uint64, limit int) ([]KnowledgeDocument, error) {
	r.log(ctx, "knowledge.document.list", "mysql.GormRepository.ListKnowledgeDocuments", "list tenant knowledge documents")
	query := r.with(ctx).Table("tenant_knowledge_documents").
		Select("id, tenant_id, user_id, title, source_type, content, metadata_json, status, updated_at").
		Where("tenant_id = ? AND deleted_at IS NULL", tenantID)
	if userID != 0 {
		query = query.Where("user_id = ?", userID)
	}
	rows, err := query.Order("updated_at DESC").Limit(normalizeLimit(limit)).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanKnowledgeDocuments(rows)
}

func (r *GormRepository) SearchKnowledgeChunks(ctx context.Context, tenantID uint64, opts KnowledgeSearchOptions) ([]KnowledgeChunk, error) {
	r.log(ctx, "knowledge.chunk.search", "mysql.GormRepository.SearchKnowledgeChunks", "search tenant knowledge chunks")
	search := buildKnowledgeSearchQuery(opts.Query)
	if search.Match == "" {
		return nil, nil
	}
	contentLike := search.likeAny("LOWER(c.content)")
	titleLike := search.likeAny("LOWER(d.title)")
	patterns := search.likePatterns()
	if isSQLite(r.db) {
		if contentLike == "" {
			return nil, nil
		}
		contentLike = search.likeAnySQLite("LOWER(c.content)")
		titleLike = search.likeAnySQLite("LOWER(d.title)")
		score := "CASE WHEN " + contentLike + " THEN 10 ELSE 0 END + CASE WHEN " + titleLike + " THEN 3 ELSE 0 END"
		scoreArgs := make([]any, 0, len(patterns)*2)
		scoreArgs = append(scoreArgs, patterns...)
		scoreArgs = append(scoreArgs, patterns...)
		filterArgs := []any{tenantID, tenantID, "active", opts.UserID, opts.UserID}
		filterArgs = append(filterArgs, patterns...)
		filterArgs = append(filterArgs, patterns...)
		rows, err := r.with(ctx).Table("tenant_knowledge_chunks c").
			Select("c.id, c.document_id, d.title, d.source_type, c.chunk_index, c.content, c.metadata_json, c.embedding_ref, ("+score+") AS score, 'like' AS search_mode", scoreArgs...).
			Joins("INNER JOIN tenant_knowledge_documents d ON d.id = c.document_id").
			Where("c.tenant_id = ? AND d.tenant_id = ? AND d.status = ? AND d.deleted_at IS NULL AND c.deleted_at IS NULL", filterArgs[:3]...).
			Where(contentLike+" OR "+titleLike, filterArgs[5:]...).
			Where("? = 0 OR d.user_id = ?", opts.UserID, opts.UserID).
			Order("score DESC, d.updated_at DESC, c.chunk_index ASC").
			Limit(normalizeLimit(opts.Limit)).
			Rows()
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return scanKnowledgeChunks(rows)
	}
	score := "MATCH(c.content) AGAINST (? IN NATURAL LANGUAGE MODE) * 20"
	selectArgs := []any{search.Match}
	filter := "MATCH(c.content) AGAINST (? IN NATURAL LANGUAGE MODE)"
	filterArgs := []any{search.Match}
	if contentLike != "" {
		score += " + CASE WHEN " + contentLike + " THEN 10 ELSE 0 END + CASE WHEN " + titleLike + " THEN 3 ELSE 0 END"
		selectArgs = append(selectArgs, patterns...)
		selectArgs = append(selectArgs, patterns...)
		filter += " OR " + contentLike + " OR " + titleLike
		filterArgs = append(filterArgs, patterns...)
		filterArgs = append(filterArgs, patterns...)
	}
	selectArgs = append(selectArgs, search.Match)
	rows, err := r.with(ctx).Table("tenant_knowledge_chunks c").
		Select("c.id, c.document_id, d.title, d.source_type, c.chunk_index, c.content, c.metadata_json, c.embedding_ref, ("+score+") AS score, CASE WHEN MATCH(c.content) AGAINST (? IN NATURAL LANGUAGE MODE) > 0 THEN 'fulltext' ELSE 'like' END AS search_mode", selectArgs...).
		Joins("INNER JOIN tenant_knowledge_documents d ON d.id = c.document_id").
		Where("c.tenant_id = ? AND d.tenant_id = ? AND d.status = ? AND d.deleted_at IS NULL AND c.deleted_at IS NULL", tenantID, tenantID, "active").
		Where("? = 0 OR d.user_id = ?", opts.UserID, opts.UserID).
		Where(filter, filterArgs...).
		Order("score DESC, d.updated_at DESC, c.chunk_index ASC").
		Limit(normalizeLimit(opts.Limit)).
		Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanKnowledgeChunks(rows)
}

func (r *GormRepository) SaveProfile(ctx context.Context, input ProfileInput) (uint64, error) {
	r.log(ctx, "profile.save", "mysql.GormRepository.SaveProfile", "save user profile")
	if input.ProfileVersion == 0 {
		input.ProfileVersion = 1
	}
	row := gormProfile{
		TenantID:               input.TenantID,
		UserID:                 input.UserID,
		ProfileVersion:         input.ProfileVersion,
		Summary:                nullableStringPtr(input.Summary),
		ProfileJSON:            input.ProfileJSON,
		GeneratedFromSessionID: nullableUint64Ptr(input.GeneratedFromSessionID),
	}
	err := r.with(ctx).Create(&row).Error
	return gormInsertedID(row.ID, err)
}

func (r *GormRepository) GetProfile(ctx context.Context, tenantID, userID uint64, version uint) (Profile, error) {
	r.log(ctx, "profile.get", "mysql.GormRepository.GetProfile", "get user profile")
	query := r.with(ctx).Table("tenant_user_profiles").
		Select("id, profile_version, summary, profile_json, COALESCE(generated_from_session_id, 0), created_at").
		Where("tenant_id = ? AND user_id = ?", tenantID, userID)
	if version == 0 {
		query = query.Order("profile_version DESC, created_at DESC")
	} else {
		query = query.Where("profile_version = ?", version)
	}
	profile, err := scanProfile(query.Limit(1).Row())
	if errors.Is(err, sql.ErrNoRows) {
		return Profile{}, ErrNotFound
	}
	return profile, err
}

func (r *GormRepository) UpsertSession(ctx context.Context, input SessionInput) (uint64, error) {
	r.log(ctx, "session.upsert", "mysql.GormRepository.UpsertSession", "upsert tenant session")
	return upsertSessionTx(r.with(ctx), input)
}

// upsertSessionTx 只写会话行，不绑定 ctx 上的连接，这样普通路径和
// SaveQueryTurn 的事务路径可以共用同一段 SQL。
func upsertSessionTx(db *gorm.DB, input SessionInput) (uint64, error) {
	if input.Status == "" {
		input.Status = "active"
	}
	now := time.Now().UTC()
	row := gormSession{
		TenantID:      input.TenantID,
		UserID:        input.UserID,
		SessionKey:    input.SessionKey,
		Title:         nullableStringPtr(input.Title),
		Status:        input.Status,
		Model:         nullableStringPtr(input.Model),
		CWD:           nullableStringPtr(input.CWD),
		MetadataJSON:  nullableStringPtr(input.MetadataJSON),
		LastMessageAt: &now,
	}
	if isSQLite(db) {
		err := db.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "tenant_id"}, {Name: "user_id"}, {Name: "session_key"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"title", "status", "model", "cwd", "metadata_json", "last_message_at",
			}),
		}).Create(&row).Error
		if err != nil {
			return 0, err
		}
		_ = db.Table("tenant_sessions").Where("id = ?", row.ID).
			Update("started_at", time.Now().UTC()).Error
		var id uint64
		err = db.Table("tenant_sessions").
			Select("id").
			Where("tenant_id = ? AND user_id = ? AND session_key = ?", input.TenantID, input.UserID, input.SessionKey).
			Scan(&id).Error
		return gormInsertedID(id, err)
	}
	err := db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "tenant_id"}, {Name: "user_id"}, {Name: "session_key"}},
		DoUpdates: clause.Assignments(map[string]any{
			"id":              gorm.Expr("LAST_INSERT_ID(id)"),
			"title":           gorm.Expr("VALUES(title)"),
			"status":          gorm.Expr("VALUES(status)"),
			"model":           gorm.Expr("VALUES(model)"),
			"cwd":             gorm.Expr("VALUES(cwd)"),
			"metadata_json":   gorm.Expr("VALUES(metadata_json)"),
			"last_message_at": gorm.Expr("CURRENT_TIMESTAMP(6)"),
		}),
	}).Create(&row).Error
	return gormInsertedID(row.ID, err)
}

func (r *GormRepository) ListSessions(ctx context.Context, tenantID, userID uint64, limit int) ([]Session, error) {
	r.log(ctx, "session.list", "mysql.GormRepository.ListSessions", "list tenant user sessions")
	rows, err := r.with(ctx).Table("tenant_sessions").
		Select("id, session_key, title, status, model, cwd, started_at, last_message_at").
		Where("tenant_id = ? AND user_id = ? AND archived_at IS NULL", tenantID, userID).
		Order("last_message_at DESC, started_at DESC").
		Limit(normalizeLimit(limit)).
		Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSessions(rows)
}

func (r *GormRepository) GetSession(ctx context.Context, tenantID, userID, sessionID uint64) (Session, error) {
	r.log(ctx, "session.get", "mysql.GormRepository.GetSession", "get tenant session")
	return getSessionTx(r.with(ctx), tenantID, userID, sessionID)
}

// GetSessionByKey returns one active session only when its key belongs to the
// requested tenant and user. Session control uses keys as external references,
// so the owner predicates are part of the lookup rather than a caller-side
// follow-up check.
func (r *GormRepository) GetSessionByKey(ctx context.Context, tenantID, userID uint64, sessionKey string) (Session, error) {
	r.log(ctx, "session.get_by_key", "mysql.GormRepository.GetSessionByKey", "get tenant session by key")
	rows, err := r.with(ctx).Table("tenant_sessions").
		Select("id, session_key, title, status, model, cwd, started_at, last_message_at").
		Where("tenant_id = ? AND user_id = ? AND session_key = ? AND archived_at IS NULL", tenantID, userID, sessionKey).
		Limit(1).
		Rows()
	if err != nil {
		return Session{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return Session{}, err
		}
		return Session{}, ErrNotFound
	}
	return scanSession(rows)
}

// getSessionTx 是消息写入前那道租户归属校验的唯一实现：会话必须属于本租户本用户。
func getSessionTx(db *gorm.DB, tenantID, userID, sessionID uint64) (Session, error) {
	rows, err := db.Table("tenant_sessions").
		Select("id, session_key, title, status, model, cwd, started_at, last_message_at").
		Where("tenant_id = ? AND user_id = ? AND id = ? AND archived_at IS NULL", tenantID, userID, sessionID).
		Limit(1).
		Rows()
	if err != nil {
		return Session{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		return Session{}, ErrNotFound
	}
	return scanSession(rows)
}

func (r *GormRepository) UpdateSession(ctx context.Context, input SessionInput) error {
	r.log(ctx, "session.update", "mysql.GormRepository.UpdateSession", "update tenant session")
	updates := map[string]any{}
	if strings.TrimSpace(input.Title) != "" {
		updates["title"] = input.Title
	}
	if strings.TrimSpace(input.Status) != "" {
		updates["status"] = input.Status
	}
	if strings.TrimSpace(input.Model) != "" {
		updates["model"] = input.Model
	}
	if strings.TrimSpace(input.CWD) != "" {
		updates["cwd"] = input.CWD
	}
	if strings.TrimSpace(input.MetadataJSON) != "" {
		updates["metadata_json"] = input.MetadataJSON
	}
	if len(updates) == 0 {
		_, err := r.GetSession(ctx, input.TenantID, input.UserID, input.ID)
		return err
	}
	result := r.with(ctx).Table("tenant_sessions").
		Where("tenant_id = ? AND user_id = ? AND id = ? AND archived_at IS NULL", input.TenantID, input.UserID, input.ID).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		_, err := r.GetSession(ctx, input.TenantID, input.UserID, input.ID)
		return err
	}
	return nil
}

func (r *GormRepository) ArchiveSession(ctx context.Context, tenantID, userID, sessionID uint64) error {
	r.log(ctx, "session.archive", "mysql.GormRepository.ArchiveSession", "archive tenant session")
	result := r.with(ctx).Table("tenant_sessions").
		Where("tenant_id = ? AND user_id = ? AND id = ? AND archived_at IS NULL", tenantID, userID, sessionID).
		Updates(map[string]any{"status": "archived", "archived_at": time.Now().UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *GormRepository) UpsertMessage(ctx context.Context, input MessageInput) (uint64, error) {
	r.log(ctx, "message.upsert", "mysql.GormRepository.UpsertMessage", "upsert session message")
	if input.TraceID == "" {
		input.TraceID = observability.TraceID(ctx)
	}
	if _, err := getSessionTx(r.with(ctx), input.TenantID, input.UserID, input.SessionID); err != nil {
		return 0, err
	}
	return upsertMessageRowTx(r.with(ctx), input)
}

// upsertMessageRowTx 只写消息行，归属校验由调用方负责：
// 普通路径先调 getSessionTx，SaveQueryTurn 在同一事务里校验一次后复用。
func upsertMessageRowTx(db *gorm.DB, input MessageInput) (uint64, error) {
	row := gormMessage{
		TenantID:     input.TenantID,
		UserID:       input.UserID,
		SessionID:    input.SessionID,
		TurnIndex:    input.TurnIndex,
		Role:         input.Role,
		Content:      nullableStringPtr(input.Content),
		ContentJSON:  nullableStringPtr(input.ContentJSON),
		ToolID:       nullableStringPtr(input.ToolID),
		ToolName:     nullableStringPtr(input.ToolName),
		IsError:      input.IsError,
		Model:        nullableStringPtr(input.Model),
		InputTokens:  input.InputTokens,
		OutputTokens: input.OutputToken,
		TraceID:      nullableStringPtr(input.TraceID),
	}
	if isSQLite(db) {
		err := db.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "session_id"}, {Name: "turn_index"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"role", "content", "content_json", "tool_id", "tool_name", "is_error",
				"model", "input_tokens", "output_tokens", "trace_id",
			}),
		}).Create(&row).Error
		if err != nil {
			return 0, err
		}
		if err := db.Table("tenant_session_messages").Where("id = ?", row.ID).
			Update("created_at", time.Now().UTC()).Error; err != nil {
			return 0, err
		}
		var id uint64
		err = db.Table("tenant_session_messages").
			Select("id").
			Where("session_id = ? AND turn_index = ?", input.SessionID, input.TurnIndex).
			Scan(&id).Error
		return gormInsertedID(id, err)
	}
	err := db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "session_id"}, {Name: "turn_index"}},
		DoUpdates: clause.Assignments(map[string]any{
			"id":            gorm.Expr("LAST_INSERT_ID(id)"),
			"role":          gorm.Expr("VALUES(role)"),
			"content":       gorm.Expr("VALUES(content)"),
			"content_json":  gorm.Expr("VALUES(content_json)"),
			"tool_id":       gorm.Expr("VALUES(tool_id)"),
			"tool_name":     gorm.Expr("VALUES(tool_name)"),
			"is_error":      gorm.Expr("VALUES(is_error)"),
			"model":         gorm.Expr("VALUES(model)"),
			"input_tokens":  gorm.Expr("VALUES(input_tokens)"),
			"output_tokens": gorm.Expr("VALUES(output_tokens)"),
			"trace_id":      gorm.Expr("VALUES(trace_id)"),
		}),
	}).Create(&row).Error
	return gormInsertedID(row.ID, err)
}

func (r *GormRepository) ListMessages(ctx context.Context, tenantID, userID, sessionID uint64, limit int) ([]Message, error) {
	r.log(ctx, "message.list", "mysql.GormRepository.ListMessages", "list tenant session messages")
	rows, err := r.with(ctx).Table("tenant_session_messages").
		Select("id, session_id, turn_index, role, content, content_json, tool_id, tool_name, is_error, model, input_tokens, output_tokens, trace_id, created_at").
		Where("tenant_id = ? AND user_id = ? AND session_id = ?", tenantID, userID, sessionID).
		Order("turn_index ASC").
		Limit(normalizeLimit(limit)).
		Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMessages(rows)
}

func (r *GormRepository) CreateAgentTask(ctx context.Context, input agenttasks.TaskInput) (uint64, error) {
	r.log(ctx, "agent_task.create", "mysql.GormRepository.CreateAgentTask", "create agent task")
	if input.Status == "" {
		input.Status = agenttasks.StatusRunning
	}
	if input.TraceID == "" {
		input.TraceID = observability.TraceID(ctx)
	}
	create := func(db *gorm.DB) (uint64, error) {
		row := newGormAgentTask(input)
		err := db.Create(&row).Error
		if err == nil && isSQLite(db) {
			err = db.Table("tenant_agent_tasks").Where("id = ?", row.ID).
				Update("started_at", time.Now().UTC()).Error
		}
		return gormInsertedID(row.ID, err)
	}
	if input.ParentSessionID == 0 || input.AgentName != agenttasks.AgentNameWeb || input.Status != agenttasks.StatusReady && input.Status != agenttasks.StatusRunning {
		return create(r.with(ctx))
	}
	var id uint64
	err := r.with(ctx).Transaction(func(tx *gorm.DB) error {
		var sessionID uint64
		if err := tx.Table("tenant_sessions").Select("id").Where("tenant_id = ? AND user_id = ? AND id = ? AND archived_at IS NULL", input.TenantID, input.UserID, input.ParentSessionID).Clauses(clause.Locking{Strength: "UPDATE"}).Take(&sessionID).Error; err != nil {
			return err
		}
		var activeID uint64
		err := tx.Table("tenant_agent_tasks").Select("id").Where("tenant_id = ? AND user_id = ? AND parent_session_id = ? AND agent_name = ? AND status IN ?", input.TenantID, input.UserID, input.ParentSessionID, agenttasks.AgentNameWeb, []string{agenttasks.StatusReady, agenttasks.StatusRunning}).Limit(1).Take(&activeID).Error
		if err == nil {
			return ErrInvalidState
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		createdID, err := create(tx)
		id = createdID
		return err
	})
	return id, err
}

func newGormAgentTask(input agenttasks.TaskInput) gormAgentTask {
	return gormAgentTask{
		TenantID:           input.TenantID,
		UserID:             input.UserID,
		ParentSessionID:    nullableUint64Ptr(input.ParentSessionID),
		SubagentSessionKey: nullableStringPtr(input.SubagentSessionKey),
		AgentName:          nullableStringPtr(input.AgentName),
		Description:        nullableStringPtr(input.Description),
		Prompt:             nullableStringPtr(input.Prompt),
		Status:             input.Status,
		Model:              nullableStringPtr(input.Model),
		ResultJSON:         nullableStringPtr(input.ResultJSON),
		MetadataJSON:       nullableStringPtr(input.MetadataJSON),
		TraceID:            nullableStringPtr(input.TraceID),
		IdempotencyKey:     nullableStringPtr(input.IdempotencyKey),
	}
}

func (r *GormRepository) GetAgentTaskByIdempotencyKey(ctx context.Context, tenantID, userID uint64, idempotencyKey string) (AgentTask, error) {
	r.log(ctx, "agent_task.get_by_idempotency", "mysql.GormRepository.GetAgentTaskByIdempotencyKey", "get agent task by idempotency key")
	var row gormAgentTask
	err := r.with(ctx).Table("tenant_agent_tasks").
		Select("id, tenant_id, user_id, parent_session_id, subagent_session_key, agent_name, description, status, model, result_json, metadata_json, trace_id, idempotency_key, started_at, finished_at").
		Where("tenant_id = ? AND user_id = ? AND idempotency_key = ?", tenantID, userID, idempotencyKey).
		Limit(1).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return AgentTask{}, ErrNotFound
	}
	if err != nil {
		return AgentTask{}, err
	}
	return agentTaskFromGORM(row), nil
}

func (r *GormRepository) UpsertSessionLink(ctx context.Context, input SessionLinkInput) (uint64, error) {
	r.log(ctx, "session_link.upsert", "mysql.GormRepository.UpsertSessionLink", "upsert tenant session link")
	return upsertSessionLink(r.with(ctx), input)
}

func upsertSessionLink(db *gorm.DB, input SessionLinkInput) (uint64, error) {
	if input.Status == "" {
		input.Status = "active"
	}
	row := gormSessionLink{
		TenantID:         input.TenantID,
		UserID:           input.UserID,
		TargetSessionID:  input.TargetSessionID,
		SourceKind:       input.SourceKind,
		SourceSessionKey: input.SourceSessionKey,
		SourceSessionID:  nullableUint64Ptr(input.SourceSessionID),
		RelationType:     input.RelationType,
		Status:           input.Status,
		MetadataJSON:     nullableStringPtr(input.MetadataJSON),
		CreatedByUserID:  input.CreatedByUserID,
	}
	if isSQLite(db) {
		var existing gormSessionLink
		err := db.Where("tenant_id = ? AND user_id = ? AND target_session_id = ? AND source_kind = ? AND source_session_key = ? AND relation_type = ?", input.TenantID, input.UserID, input.TargetSessionID, input.SourceKind, input.SourceSessionKey, input.RelationType).Take(&existing).Error
		if err == nil {
			if err := db.Table("tenant_session_links").Where("id = ?", existing.ID).Updates(map[string]any{
				"status": input.Status, "metadata_json": row.MetadataJSON, "updated_at": time.Now().UTC(),
			}).Error; err != nil {
				return 0, err
			}
			return existing.ID, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, err
		}
		if err := db.Create(&row).Error; err != nil {
			return 0, err
		}
		return row.ID, nil
	}
	err := db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "tenant_id"}, {Name: "user_id"}, {Name: "target_session_id"}, {Name: "source_kind"}, {Name: "source_session_key"}, {Name: "relation_type"}},
		DoUpdates: clause.Assignments(map[string]any{
			"id":            gorm.Expr("LAST_INSERT_ID(id)"),
			"status":        gorm.Expr("VALUES(status)"),
			"metadata_json": gorm.Expr("VALUES(metadata_json)"),
			"updated_at":    gorm.Expr("CURRENT_TIMESTAMP(6)"),
		}),
	}).Create(&row).Error
	return gormInsertedID(row.ID, err)
}

// CreateHandoffLinkAndEvent persists a handoff relation and its immutable task
// event as one unit. Locking the ready task serializes same-task handoffs and
// closes the status race between validation and event append.
func (r *GormRepository) CreateHandoffLinkAndEvent(ctx context.Context, input agenttasks.HandoffLinkAndEventInput) (agenttasks.HandoffLinkAndEventResult, error) {
	r.log(ctx, "session_handoff.persist", "mysql.GormRepository.CreateHandoffLinkAndEvent", "persist session handoff link and task event")
	if input.TenantID == 0 || input.UserID == 0 || input.TargetSessionID == 0 || input.TargetTaskID == 0 || input.SourceKind == "" || input.SourceSessionKey == "" || input.RelationType != agenttasks.SessionHandoffRelationType || input.PayloadJSON == "" {
		return agenttasks.HandoffLinkAndEventResult{}, ErrInvalidInput
	}
	decoded, err := agenttasks.DecodeSessionHandoffEvent(input.PayloadJSON)
	if err != nil || decoded.TargetTaskID != input.TargetTaskID || decoded.SourceRef != input.SourceKind+":"+input.SourceSessionKey {
		return agenttasks.HandoffLinkAndEventResult{}, fmt.Errorf("%w: invalid session handoff event", ErrInvalidInput)
	}
	linkMetadata, err := json.Marshal(struct {
		Schema        string `json:"schema"`
		PackageID     string `json:"package_id"`
		PackageSHA256 string `json:"package_sha256"`
		TargetTaskID  uint64 `json:"target_task_id"`
	}{decoded.Schema, decoded.PackageID, decoded.PackageSHA256, decoded.TargetTaskID})
	if err != nil {
		return agenttasks.HandoffLinkAndEventResult{}, fmt.Errorf("%w: encode session handoff lineage", ErrInvalidInput)
	}
	if input.LinkStatus == "" {
		input.LinkStatus = agenttasks.SessionHandoffLinkStatus
	} else if input.LinkStatus != agenttasks.SessionHandoffLinkStatus {
		return agenttasks.HandoffLinkAndEventResult{}, ErrInvalidInput
	}
	if input.CreatedByUserID == 0 {
		input.CreatedByUserID = input.UserID
	}
	if input.TraceID == "" {
		input.TraceID = observability.TraceID(ctx)
	}

	var result agenttasks.HandoffLinkAndEventResult
	err = r.with(ctx).Transaction(func(tx *gorm.DB) error {
		var task struct {
			ID              uint64  `gorm:"column:id"`
			ParentSessionID *uint64 `gorm:"column:parent_session_id"`
			Status          string  `gorm:"column:status"`
		}
		err := tx.Table("tenant_agent_tasks").
			Select("id, parent_session_id, status").
			Where("tenant_id = ? AND user_id = ? AND id = ?", input.TenantID, input.UserID, input.TargetTaskID).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Take(&task).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if task.ParentSessionID == nil || *task.ParentSessionID != input.TargetSessionID {
			return ErrNotFound
		}
		if task.Status != agenttasks.StatusReady {
			return ErrInvalidState
		}

		var existing struct {
			ID uint64 `gorm:"column:id"`
		}
		err = tx.Table("tenant_agent_task_events").
			Select("id").
			Where("tenant_id = ? AND user_id = ? AND task_id = ? AND event_type = ? AND "+jsonTextEqualsPredicate(tx, "payload_json", "$.package_sha256")+" AND "+jsonTextEqualsPredicate(tx, "payload_json", "$.package_id"), input.TenantID, input.UserID, input.TargetTaskID, agenttasks.EventSessionHandoff, decoded.PackageSHA256, decoded.PackageID).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Take(&existing).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		linkID, err := upsertSessionLink(tx, SessionLinkInput{
			TenantID: input.TenantID, UserID: input.UserID, TargetSessionID: input.TargetSessionID,
			SourceKind: input.SourceKind, SourceSessionKey: input.SourceSessionKey, SourceSessionID: input.SourceSessionID,
			RelationType: input.RelationType, Status: input.LinkStatus, MetadataJSON: string(linkMetadata), CreatedByUserID: input.CreatedByUserID,
		})
		if err != nil {
			return err
		}
		result.LinkID = linkID
		if existing.ID != 0 {
			result.EventID = existing.ID
			result.Replayed = true
			return nil
		}

		row := gormAgentTaskEvent{
			TenantID: input.TenantID, UserID: input.UserID, TaskID: input.TargetTaskID,
			EventType: agenttasks.EventSessionHandoff, PayloadJSON: nullableStringPtr(input.PayloadJSON), TraceID: nullableStringPtr(input.TraceID),
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		result.EventID = row.ID
		return nil
	})
	if err != nil {
		return agenttasks.HandoffLinkAndEventResult{}, err
	}
	return result, nil
}

// CreateHandoffBatch persists every source link/event under one target-task
// lock and one transaction. A prior operation is replayed only when every
// package SHA is present; partial historical state is rejected.
func (r *GormRepository) CreateHandoffBatch(ctx context.Context, input agenttasks.HandoffBatchInput) (agenttasks.HandoffBatchResult, error) {
	r.log(ctx, "session_handoff.batch.persist", "mysql.GormRepository.CreateHandoffBatch", "persist session handoff batch")
	if input.TenantID == 0 || input.UserID == 0 || input.TargetSessionID == 0 || input.TargetTaskID == 0 || len(input.Items) == 0 || !validHandoffOperationIdentity(input.OperationIdentity) {
		return agenttasks.HandoffBatchResult{}, ErrInvalidInput
	}
	decoded := make([]agenttasks.DecodedSessionHandoffEvent, len(input.Items))
	seenPackages := make(map[string]struct{}, len(input.Items))
	seenSources := make(map[string]struct{}, len(input.Items))
	for index, item := range input.Items {
		item.TargetSessionID, item.TargetTaskID = input.TargetSessionID, input.TargetTaskID
		item.TenantID, item.UserID = input.TenantID, input.UserID
		if item.SourceKind == "" || item.SourceSessionKey == "" || item.RelationType != agenttasks.SessionHandoffRelationType || item.PayloadJSON == "" {
			return agenttasks.HandoffBatchResult{}, ErrInvalidInput
		}
		event, err := agenttasks.DecodeSessionHandoffEvent(item.PayloadJSON)
		if err != nil || event.TargetTaskID != input.TargetTaskID || event.SourceRef != item.SourceKind+":"+item.SourceSessionKey || event.OperationIdentity != input.OperationIdentity {
			return agenttasks.HandoffBatchResult{}, fmt.Errorf("%w: invalid session handoff batch event", ErrInvalidInput)
		}
		if _, exists := seenPackages[event.PackageSHA256]; exists {
			return agenttasks.HandoffBatchResult{}, ErrInvalidInput
		}
		if _, exists := seenSources[event.SourceRef]; exists {
			return agenttasks.HandoffBatchResult{}, ErrInvalidInput
		}
		seenPackages[event.PackageSHA256], seenSources[event.SourceRef] = struct{}{}, struct{}{}
		decoded[index], input.Items[index] = event, item
	}
	if input.CreatedByUserID == 0 {
		input.CreatedByUserID = input.UserID
	}
	if input.TraceID == "" {
		input.TraceID = observability.TraceID(ctx)
	}
	result := agenttasks.HandoffBatchResult{Items: make([]agenttasks.HandoffLinkAndEventResult, len(input.Items))}
	err := r.with(ctx).Transaction(func(tx *gorm.DB) error {
		var task struct {
			ID              uint64  `gorm:"column:id"`
			ParentSessionID *uint64 `gorm:"column:parent_session_id"`
			Status          string  `gorm:"column:status"`
		}
		err := tx.Table("tenant_agent_tasks").Select("id, parent_session_id, status").Where("tenant_id = ? AND user_id = ? AND id = ?", input.TenantID, input.UserID, input.TargetTaskID).Clauses(clause.Locking{Strength: "UPDATE"}).Take(&task).Error
		if errors.Is(err, gorm.ErrRecordNotFound) || err == nil && (task.ParentSessionID == nil || *task.ParentSessionID != input.TargetSessionID) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		var existing []gormAgentTaskEvent
		err = tx.Table("tenant_agent_task_events").Select("id, payload_json").Where("tenant_id = ? AND user_id = ? AND task_id = ? AND event_type = ? AND "+jsonTextEqualsPredicate(tx, "payload_json", "$.operation_identity"), input.TenantID, input.UserID, input.TargetTaskID, agenttasks.EventSessionHandoff, input.OperationIdentity).Clauses(clause.Locking{Strength: "UPDATE"}).Find(&existing).Error
		if err != nil {
			return err
		}
		if len(existing) > 0 {
			if len(existing) != len(input.Items) {
				return ErrInvalidState
			}
			eventIDs := make(map[string]uint64, len(existing))
			for _, row := range existing {
				if row.PayloadJSON == nil {
					return ErrInvalidState
				}
				stored, decodeErr := agenttasks.DecodeSessionHandoffEvent(*row.PayloadJSON)
				if decodeErr != nil || stored.OperationIdentity != input.OperationIdentity {
					return ErrInvalidState
				}
				eventIDs[stored.PackageSHA256] = row.ID
			}
			for index, expected := range decoded {
				id, found := eventIDs[expected.PackageSHA256]
				if !found || id == 0 {
					return ErrInvalidState
				}
				result.Items[index].EventID = id
			}
			result.Replayed = true
		}
		if !result.Replayed && task.Status != agenttasks.StatusReady {
			return ErrInvalidState
		}
		for index, item := range input.Items {
			event := decoded[index]
			metadata, err := json.Marshal(struct {
				Schema            string `json:"schema"`
				PackageID         string `json:"package_id"`
				PackageSHA256     string `json:"package_sha256"`
				OperationIdentity string `json:"operation_identity"`
				TargetTaskID      uint64 `json:"target_task_id"`
			}{event.Schema, event.PackageID, event.PackageSHA256, input.OperationIdentity, event.TargetTaskID})
			if err != nil {
				return err
			}
			linkID, err := upsertSessionLink(tx, SessionLinkInput{TenantID: input.TenantID, UserID: input.UserID, TargetSessionID: input.TargetSessionID, SourceKind: item.SourceKind, SourceSessionKey: item.SourceSessionKey, SourceSessionID: item.SourceSessionID, RelationType: item.RelationType, Status: agenttasks.SessionHandoffLinkStatus, MetadataJSON: string(metadata), CreatedByUserID: input.CreatedByUserID})
			if err != nil {
				return err
			}
			result.Items[index].LinkID = linkID
			if result.Replayed {
				result.Items[index].Replayed = true
				continue
			}
			row := gormAgentTaskEvent{TenantID: input.TenantID, UserID: input.UserID, TaskID: input.TargetTaskID, EventType: agenttasks.EventSessionHandoff, PayloadJSON: nullableStringPtr(item.PayloadJSON), TraceID: nullableStringPtr(input.TraceID)}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
			result.Items[index].EventID = row.ID
		}
		return nil
	})
	if err != nil {
		return agenttasks.HandoffBatchResult{}, err
	}
	return result, nil
}

// RecoverHandoffBatch resolves a fully committed operation without consulting
// mutable source state or requiring the target task to remain ready.
func (r *GormRepository) RecoverHandoffBatch(ctx context.Context, input agenttasks.HandoffRecoveryInput) (agenttasks.HandoffRecoveryResult, error) {
	r.log(ctx, "session_handoff.batch.recover", "mysql.GormRepository.RecoverHandoffBatch", "recover durable session handoff batch")
	if input.TenantID == 0 || input.UserID == 0 || input.TargetSessionID == 0 || input.TargetTaskID == 0 || !validHandoffOperationIdentity(input.OperationIdentity) || len(input.ExpectedSources) == 0 {
		return agenttasks.HandoffRecoveryResult{}, ErrInvalidInput
	}
	result := agenttasks.HandoffRecoveryResult{}
	err := r.with(ctx).Transaction(func(tx *gorm.DB) error {
		var task struct {
			ID              uint64  `gorm:"column:id"`
			ParentSessionID *uint64 `gorm:"column:parent_session_id"`
		}
		err := tx.Table("tenant_agent_tasks").Select("id, parent_session_id").Where("tenant_id = ? AND user_id = ? AND id = ?", input.TenantID, input.UserID, input.TargetTaskID).Take(&task).Error
		if errors.Is(err, gorm.ErrRecordNotFound) || err == nil && (task.ParentSessionID == nil || *task.ParentSessionID != input.TargetSessionID) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		var rows []gormAgentTaskEvent
		if err := tx.Table("tenant_agent_task_events").Select("id, payload_json").Where("tenant_id = ? AND user_id = ? AND task_id = ? AND event_type = ? AND "+jsonTextEqualsPredicate(tx, "payload_json", "$.operation_identity"), input.TenantID, input.UserID, input.TargetTaskID, agenttasks.EventSessionHandoff, input.OperationIdentity).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		if len(rows) != len(input.ExpectedSources) {
			return ErrInvalidState
		}
		bySource := make(map[string]agenttasks.HandoffRecoveredItem, len(rows))
		operationMetadataJSON := ""
		for index, row := range rows {
			if row.PayloadJSON == nil {
				return ErrInvalidState
			}
			event, decodeErr := agenttasks.DecodeSessionHandoffEvent(*row.PayloadJSON)
			if decodeErr != nil || event.OperationIdentity != input.OperationIdentity {
				return ErrInvalidState
			}
			if index == 0 {
				operationMetadataJSON = event.OperationMetadataJSON
			} else if event.OperationMetadataJSON != operationMetadataJSON {
				return ErrInvalidState
			}
			bySource[event.SourceRef] = agenttasks.HandoffRecoveredItem{EventID: row.ID, SourceRef: event.SourceRef, PackageID: event.PackageID, PackageSHA256: event.PackageSHA256, SourceCursor: event.SourceCursor, EstimatedTokens: event.EstimatedTokens}
		}
		result.Items = make([]agenttasks.HandoffRecoveredItem, len(input.ExpectedSources))
		for index, sourceRef := range input.ExpectedSources {
			item, found := bySource[sourceRef]
			if !found {
				return ErrInvalidState
			}
			kind, key, ok := strings.Cut(sourceRef, ":")
			if !ok {
				return ErrInvalidInput
			}
			var link struct {
				ID uint64 `gorm:"column:id"`
			}
			if err := tx.Table("tenant_session_links").Select("id").Where("tenant_id = ? AND user_id = ? AND target_session_id = ? AND source_kind = ? AND source_session_key = ? AND relation_type = ?", input.TenantID, input.UserID, input.TargetSessionID, kind, key, agenttasks.SessionHandoffRelationType).Take(&link).Error; err != nil {
				return err
			}
			item.LinkID = link.ID
			result.Items[index] = item
		}
		result.Found = true
		result.OperationMetadataJSON = operationMetadataJSON
		return nil
	})
	if err != nil {
		return agenttasks.HandoffRecoveryResult{}, err
	}
	return result, nil
}

func validHandoffOperationIdentity(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && strings.ToLower(value) == value
}

func (r *GormRepository) GetSessionLink(ctx context.Context, tenantID, userID, targetSessionID uint64, sourceKind, sourceSessionKey, relationType string) (SessionLink, error) {
	r.log(ctx, "session_link.get", "mysql.GormRepository.GetSessionLink", "get tenant session link")
	var row gormSessionLink
	err := r.with(ctx).Table("tenant_session_links").
		Where("tenant_id = ? AND user_id = ? AND target_session_id = ? AND source_kind = ? AND source_session_key = ? AND relation_type = ?", tenantID, userID, targetSessionID, sourceKind, sourceSessionKey, relationType).
		Limit(1).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return SessionLink{}, ErrNotFound
	}
	if err != nil {
		return SessionLink{}, err
	}
	return sessionLinkFromGORM(row), nil
}

func (r *GormRepository) ListSessionLinks(ctx context.Context, tenantID, userID, targetSessionID uint64, limit int) ([]SessionLink, error) {
	r.log(ctx, "session_link.list", "mysql.GormRepository.ListSessionLinks", "list tenant session links")
	var rows []gormSessionLink
	query := r.with(ctx).Table("tenant_session_links").
		Where("tenant_id = ? AND user_id = ? AND target_session_id = ?", tenantID, userID, targetSessionID).
		Order("created_at ASC, id ASC").
		Limit(normalizeLimit(limit))
	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]SessionLink, 0, len(rows))
	for _, row := range rows {
		out = append(out, sessionLinkFromGORM(row))
	}
	return out, nil
}

func agentTaskFromGORM(row gormAgentTask) AgentTask {
	return AgentTask{
		ID:                 row.ID,
		TenantID:           row.TenantID,
		UserID:             row.UserID,
		ParentSessionID:    valueUint64Ptr(row.ParentSessionID),
		SubagentSessionKey: valueStringPtr(row.SubagentSessionKey),
		AgentName:          valueStringPtr(row.AgentName),
		Description:        valueStringPtr(row.Description),
		Status:             row.Status,
		Model:              valueStringPtr(row.Model),
		ResultJSON:         valueStringPtr(row.ResultJSON),
		MetadataJSON:       valueStringPtr(row.MetadataJSON),
		TraceID:            valueStringPtr(row.TraceID),
		IdempotencyKey:     valueStringPtr(row.IdempotencyKey),
		StartedAt:          row.StartedAt,
		FinishedAt:         timeValue(row.FinishedAt),
	}
}

func sessionLinkFromGORM(row gormSessionLink) SessionLink {
	return SessionLink{
		ID:               row.ID,
		TenantID:         row.TenantID,
		UserID:           row.UserID,
		TargetSessionID:  row.TargetSessionID,
		SourceKind:       row.SourceKind,
		SourceSessionKey: row.SourceSessionKey,
		SourceSessionID:  valueUint64Ptr(row.SourceSessionID),
		RelationType:     row.RelationType,
		Status:           row.Status,
		MetadataJSON:     valueStringPtr(row.MetadataJSON),
		CreatedByUserID:  row.CreatedByUserID,
		CreatedAt:        row.CreatedAt,
		UpdatedAt:        row.UpdatedAt,
	}
}

func (r *GormRepository) FinishAgentTask(ctx context.Context, taskID uint64, status string, resultJSON string) error {
	r.log(ctx, "agent_task.finish", "mysql.GormRepository.FinishAgentTask", "finish agent task")
	if status == "" {
		status = agenttasks.StatusCompleted
	}
	result := r.workflowTaskWriteQuery(r.with(ctx).Table("tenant_agent_tasks")).
		Where("id = ?", taskID).
		Updates(map[string]any{
			"status":      status,
			"result_json": nullableStringPtr(resultJSON),
			"finished_at": time.Now().UTC(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		if err := r.checkAnyWorkflowTaskOwnership(r.with(ctx), taskID); err != nil {
			return err
		}
		return ErrNotFound
	}
	return nil
}

func (r *GormRepository) CancelAgentTask(ctx context.Context, tenantID, userID, taskID uint64, resultJSON string) error {
	r.log(ctx, "agent_task.cancel", "mysql.GormRepository.CancelAgentTask", "cancel agent task")
	result := r.workflowTaskWriteQuery(r.with(ctx).Table("tenant_agent_tasks")).
		Where("tenant_id = ? AND user_id = ? AND id = ?", tenantID, userID, taskID).
		Updates(map[string]any{
			"status":      agenttasks.StatusCancelled,
			"result_json": nullableStringPtr(resultJSON),
			"finished_at": time.Now().UTC(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		if err := r.checkWorkflowTaskOwnership(r.with(ctx), taskID, tenantID, userID); err != nil {
			return err
		}
		return ErrNotFound
	}
	return nil
}

// CancelAgentTaskIfRunning is the compare-and-set cancellation primitive for
// session control. A runner that reached a terminal status first wins; this
// method reports false instead of overwriting its result.
func (r *GormRepository) CancelAgentTaskIfRunning(ctx context.Context, tenantID, userID, taskID uint64, resultJSON string) (bool, error) {
	r.log(ctx, "agent_task.cancel_if_running", "mysql.GormRepository.CancelAgentTaskIfRunning", "cancel running tenant agent task")
	result := r.workflowTaskWriteQuery(r.with(ctx).Table("tenant_agent_tasks")).
		Where("tenant_id = ? AND user_id = ? AND id = ? AND status = ?", tenantID, userID, taskID, agenttasks.StatusRunning).
		Updates(map[string]any{
			"status":      agenttasks.StatusCancelled,
			"result_json": nullableStringPtr(resultJSON),
			"finished_at": time.Now().UTC(),
		})
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected == 0 {
		if err := r.checkWorkflowTaskOwnership(r.with(ctx), taskID, tenantID, userID); err != nil {
			return false, err
		}
	}
	return result.RowsAffected > 0, nil
}

func (r *GormRepository) GetAgentTaskStatus(ctx context.Context, tenantID, userID, taskID uint64) (string, error) {
	r.log(ctx, "agent_task.status", "mysql.GormRepository.GetAgentTaskStatus", "get agent task status")
	var row struct {
		Status string `gorm:"column:status"`
	}
	err := r.with(ctx).Table("tenant_agent_tasks").
		Select("status").
		Where("tenant_id = ? AND user_id = ? AND id = ?", tenantID, userID, taskID).
		Limit(1).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", ErrNotFound
	}
	return row.Status, err
}

func (r *GormRepository) GetAgentTask(ctx context.Context, tenantID, userID, taskID uint64) (AgentTask, error) {
	r.log(ctx, "agent_task.get", "mysql.GormRepository.GetAgentTask", "get agent task")
	rows, err := r.with(ctx).Table("tenant_agent_tasks").
		Select("id, tenant_id, user_id, COALESCE(parent_session_id, 0), COALESCE(subagent_session_key, ''), COALESCE(agent_name, ''), COALESCE(description, ''), status, COALESCE(model, ''), COALESCE(result_json, ''), COALESCE(metadata_json, ''), COALESCE(trace_id, ''), started_at, finished_at").
		Where("tenant_id = ? AND user_id = ? AND id = ?", tenantID, userID, taskID).
		Limit(1).
		Rows()
	if err != nil {
		return AgentTask{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return AgentTask{}, err
		}
		return AgentTask{}, ErrNotFound
	}
	task, err := scanAgentTask(rows)
	if err != nil {
		return AgentTask{}, err
	}
	return task, rows.Err()
}

func (r *GormRepository) UpdateAgentTask(ctx context.Context, taskID uint64, input agenttasks.TaskUpdate) error {
	r.log(ctx, "agent_task.update", "mysql.GormRepository.UpdateAgentTask", "update agent task")
	updates := map[string]any{}
	if strings.TrimSpace(input.Status) != "" {
		updates["status"] = strings.TrimSpace(input.Status)
		if isTerminalAgentTaskStatus(input.Status) {
			updates["finished_at"] = time.Now().UTC()
		}
	}
	if strings.TrimSpace(input.ResultJSON) != "" {
		updates["result_json"] = nullableStringPtr(input.ResultJSON)
	}
	if strings.TrimSpace(input.MetadataJSON) != "" {
		updates["metadata_json"] = nullableStringPtr(input.MetadataJSON)
	}
	if len(updates) == 0 {
		if !r.workflowTaskGuards.Load() {
			return nil
		}
		return r.checkWorkflowTaskOwnership(r.with(ctx), taskID, input.TenantID, input.UserID)
	}
	result := r.workflowTaskWriteQuery(r.with(ctx).Table("tenant_agent_tasks")).
		Where("tenant_id = ? AND user_id = ? AND id = ?", input.TenantID, input.UserID, taskID).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		if err := r.checkWorkflowTaskOwnership(r.with(ctx), taskID, input.TenantID, input.UserID); err != nil {
			return err
		}
		return ErrNotFound
	}
	return nil
}

func (r *GormRepository) ListAgentTasks(ctx context.Context, tenantID, userID uint64, limit int) ([]AgentTask, error) {
	r.log(ctx, "agent_task.list", "mysql.GormRepository.ListAgentTasks", "list agent tasks")
	rows, err := r.with(ctx).Table("tenant_agent_tasks").
		Select("id, tenant_id, user_id, COALESCE(parent_session_id, 0), COALESCE(subagent_session_key, ''), COALESCE(agent_name, ''), COALESCE(description, ''), status, COALESCE(model, ''), COALESCE(result_json, ''), COALESCE(metadata_json, ''), COALESCE(trace_id, ''), started_at, finished_at").
		Where("tenant_id = ? AND user_id = ?", tenantID, userID).
		Order("started_at DESC").
		Limit(normalizeLimit(limit)).
		Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAgentTasks(rows)
}

// ListLatestAgentTasksForSessions returns at most one latest task for every
// requested parent session in one query. The id tiebreaker makes equal
// started_at values deterministic.
func (r *GormRepository) ListLatestAgentTasksForSessions(ctx context.Context, tenantID, userID uint64, sessionIDs []uint64) ([]AgentTask, error) {
	if len(sessionIDs) == 0 {
		return nil, nil
	}
	r.log(ctx, "agent_task.list_latest_for_sessions", "mysql.GormRepository.ListLatestAgentTasksForSessions", "list latest tenant agent tasks for sessions", "sessions", len(sessionIDs))
	rows, err := r.with(ctx).Raw(`
SELECT id, tenant_id, user_id, parent_session_id, subagent_session_key, agent_name, description, status, model, result_json, metadata_json, trace_id, started_at, finished_at
FROM (
    SELECT id, tenant_id, user_id, COALESCE(parent_session_id, 0) AS parent_session_id, COALESCE(subagent_session_key, '') AS subagent_session_key, COALESCE(agent_name, '') AS agent_name, COALESCE(description, '') AS description, status, COALESCE(model, '') AS model, COALESCE(result_json, '') AS result_json, COALESCE(metadata_json, '') AS metadata_json, COALESCE(trace_id, '') AS trace_id, started_at, finished_at,
	        ROW_NUMBER() OVER (PARTITION BY parent_session_id ORDER BY CASE WHEN agent_name = ? THEN 0 ELSE 1 END, started_at DESC, id DESC) AS row_rank
    FROM tenant_agent_tasks
    WHERE tenant_id = ? AND user_id = ? AND parent_session_id IN ?
) AS ranked_tasks
WHERE row_rank = 1
ORDER BY parent_session_id ASC`, agenttasks.AgentNameWeb, tenantID, userID, sessionIDs).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAgentTasks(rows)
}

// ListStaleRunningAgentTasks 找出仍停在 running 但 started_at 已早于 startedBefore
// 的任务——这些是进程重启或超时后失联的孤儿。跨租户扫描，但每行都带 tenant_id /
// user_id，调用方据此按行归属写回。
func (r *GormRepository) ListStaleRunningAgentTasks(ctx context.Context, startedBefore time.Time, limit int) ([]AgentTask, error) {
	r.log(ctx, "agent_task.list_stale", "mysql.GormRepository.ListStaleRunningAgentTasks", "list stale running agent tasks")
	rows, err := r.workflowTaskWriteQuery(r.with(ctx).Table("tenant_agent_tasks")).
		Select("id, tenant_id, user_id, COALESCE(parent_session_id, 0), COALESCE(subagent_session_key, ''), COALESCE(agent_name, ''), COALESCE(description, ''), status, COALESCE(model, ''), COALESCE(result_json, ''), COALESCE(metadata_json, ''), COALESCE(trace_id, ''), started_at, finished_at").
		Where("status = ? AND started_at < ?", agenttasks.StatusRunning, startedBefore.UTC()).
		Order("started_at ASC").
		Limit(normalizeLimit(limit)).
		Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAgentTasks(rows)
}

// FailStaleAgentTask 把失联任务置 failed。WHERE 同时锁定 tenant_id / user_id
// （租户隔离）和 status = running（compare-and-set）：若真正的 runner 已经收尾，
// 这里返回 ErrNotFound 而不会覆盖它的结果。
func (r *GormRepository) FailStaleAgentTask(ctx context.Context, tenantID, userID, taskID uint64, resultJSON string) error {
	r.log(ctx, "agent_task.fail_stale", "mysql.GormRepository.FailStaleAgentTask", "fail stale running agent task")
	result := r.workflowTaskWriteQuery(r.with(ctx).Table("tenant_agent_tasks")).
		Where("tenant_id = ? AND user_id = ? AND id = ? AND status = ?", tenantID, userID, taskID, agenttasks.StatusRunning).
		Updates(map[string]any{
			"status":      agenttasks.StatusFailed,
			"result_json": nullableStringPtr(resultJSON),
			"finished_at": time.Now().UTC(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *GormRepository) AppendAgentTaskEvent(ctx context.Context, input agenttasks.EventInput) (uint64, error) {
	r.log(ctx, "agent_task_event.append", "mysql.GormRepository.AppendAgentTaskEvent", "append agent task event")
	if input.TraceID == "" {
		input.TraceID = observability.TraceID(ctx)
	}
	row := gormAgentTaskEvent{
		TenantID:    input.TenantID,
		UserID:      input.UserID,
		TaskID:      input.TaskID,
		EventType:   input.EventType,
		PayloadJSON: nullableStringPtr(input.PayloadJSON),
		TraceID:     nullableStringPtr(input.TraceID),
	}
	if r.workflowTaskGuards.Load() {
		err := r.appendWorkflowGuardedTaskEvent(ctx, &row)
		return gormInsertedID(row.ID, err)
	}
	err := r.with(ctx).Create(&row).Error
	if err == nil && isSQLite(r.db) {
		err = r.with(ctx).Table("tenant_agent_task_events").Where("id = ?", row.ID).
			Update("created_at", time.Now().UTC()).Error
	}
	return gormInsertedID(row.ID, err)
}

// GetAgentTaskEvent reads one event by its global ID while preserving the
// tenant/user ownership predicate required by evidence resolution.
func (r *GormRepository) GetAgentTaskEvent(ctx context.Context, tenantID, userID, eventID uint64) (AgentTaskEvent, error) {
	r.log(ctx, "agent_task_event.get", "mysql.GormRepository.GetAgentTaskEvent", "get tenant agent task event")
	rows, err := r.with(ctx).Table("tenant_agent_task_events").
		Select("id, task_id, event_type, COALESCE(payload_json, ''), COALESCE(trace_id, ''), created_at").
		Where("tenant_id = ? AND user_id = ? AND id = ?", tenantID, userID, eventID).
		Limit(1).
		Rows()
	if err != nil {
		return AgentTaskEvent{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return AgentTaskEvent{}, err
		}
		return AgentTaskEvent{}, ErrNotFound
	}
	event, err := scanAgentTaskEvent(rows)
	if err != nil {
		return AgentTaskEvent{}, err
	}
	return event, rows.Err()
}

func (r *GormRepository) ListAgentTaskEvents(ctx context.Context, tenantID, userID, taskID uint64, limit int) ([]AgentTaskEvent, error) {
	return r.ListAgentTaskEventsAfter(ctx, tenantID, userID, taskID, 0, limit)
}

func (r *GormRepository) ListAgentTaskEventsAfter(ctx context.Context, tenantID, userID, taskID uint64, afterID uint64, limit int) ([]AgentTaskEvent, error) {
	r.log(ctx, "agent_task_event.list", "mysql.GormRepository.ListAgentTaskEvents", "list agent task events")
	rows, err := r.with(ctx).Table("tenant_agent_task_events").
		Select("id, task_id, event_type, COALESCE(payload_json, ''), COALESCE(trace_id, ''), created_at").
		Where("tenant_id = ? AND user_id = ? AND task_id = ? AND id > ?", tenantID, userID, taskID, afterID).
		Order("id ASC").
		Limit(normalizeLimit(limit)).
		Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAgentTaskEvents(rows)
}

// maxAgentTaskEventBatch 给批量取事件兜个总上限；单任务额度乘任务数之后不能无限涨。
const maxAgentTaskEventBatch = 5000

// maxCompleteAgentTaskEventBatch is deliberately independent from paginated
// event reads. This path is an all-or-error permission window, not a feed.
const maxCompleteAgentTaskEventBatch = 10000

// ListAgentTaskEventsForTasks 一次取回多个任务的事件，取代「每个任务一条查询」的
// N+1(AUDIT-P1-26)。task_id IN (...) 走 idx_agent_task_events_task。
//
// limitPerTask 沿用逐任务查询时的语义（每个任务多少条），内部换算成总额度，
// 这样调用方换过来不会静默丢事件。
func (r *GormRepository) ListAgentTaskEventsForTasks(ctx context.Context, tenantID, userID uint64, taskIDs []uint64, limitPerTask int) ([]AgentTaskEvent, error) {
	if len(taskIDs) == 0 {
		return nil, nil
	}
	total := normalizeLimit(limitPerTask) * len(taskIDs)
	if total > maxAgentTaskEventBatch {
		total = maxAgentTaskEventBatch
	}
	r.log(ctx, "agent_task_event.list_batch", "mysql.GormRepository.ListAgentTaskEventsForTasks", "list agent task events for tasks",
		"tasks", len(taskIDs), "limit", total)
	rows, err := r.with(ctx).Table("tenant_agent_task_events").
		Select("id, task_id, event_type, COALESCE(payload_json, ''), COALESCE(trace_id, ''), created_at").
		Where("tenant_id = ? AND user_id = ? AND task_id IN ?", tenantID, userID, taskIDs).
		Order("id ASC").
		Limit(total).
		Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAgentTaskEvents(rows)
}

// ListAgentTaskEventsForTasksComplete reads a complete ordered event window for
// the requested tasks. It reads one extra row to turn a safety bound into an
// explicit error rather than silently omitting a later permission resolution.
func (r *GormRepository) ListAgentTaskEventsForTasksComplete(ctx context.Context, tenantID, userID uint64, taskIDs []uint64) ([]AgentTaskEvent, error) {
	if len(taskIDs) == 0 {
		return nil, nil
	}
	r.log(ctx, "agent_task_event.list_complete", "mysql.GormRepository.ListAgentTaskEventsForTasksComplete", "list complete tenant agent task events", "tasks", len(taskIDs), "limit", maxCompleteAgentTaskEventBatch)
	rows, err := r.with(ctx).Table("tenant_agent_task_events").
		Select("id, task_id, event_type, COALESCE(payload_json, ''), COALESCE(trace_id, ''), created_at").
		Where("tenant_id = ? AND user_id = ? AND task_id IN ?", tenantID, userID, taskIDs).
		Order("id ASC").
		Limit(maxCompleteAgentTaskEventBatch + 1).
		Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events, err := scanAgentTaskEvents(rows)
	if err != nil {
		return nil, err
	}
	if len(events) > maxCompleteAgentTaskEventBatch {
		return nil, fmt.Errorf("%w: requested tasks have more than %d events", ErrTooManyAgentTaskEvents, maxCompleteAgentTaskEventBatch)
	}
	return events, nil
}

func (r *GormRepository) CreateGoal(ctx context.Context, tenantID, userID uint64, input goal.CreateInput) (goal.Goal, error) {
	r.log(ctx, "goal.create", "mysql.GormRepository.CreateGoal", "create tenant goal")
	item, err := goal.NewGoal(input)
	if err != nil {
		return goal.Goal{}, err
	}
	if err := item.Validate(); err != nil {
		return goal.Goal{}, err
	}
	row := gormGoal{
		TenantID:    tenantID,
		UserID:      userID,
		GoalKey:     item.ID,
		Objective:   item.Objective,
		Status:      string(item.Status),
		SessionID:   nullableStringPtr(item.SessionID),
		CWD:         nullableStringPtr(item.CWD),
		Model:       nullableStringPtr(item.Model),
		TurnBudget:  item.TurnBudget,
		TokenBudget: item.TokenBudget,
		CreatedAt:   item.CreatedAt,
		UpdatedAt:   item.UpdatedAt,
	}
	if err := r.with(ctx).Create(&row).Error; err != nil {
		return goal.Goal{}, err
	}
	return item, nil
}

func (r *GormRepository) GetGoal(ctx context.Context, tenantID, userID uint64, goalID string) (goal.Goal, error) {
	r.log(ctx, "goal.get", "mysql.GormRepository.GetGoal", "get tenant goal")
	rows, err := r.goalSelect(ctx).
		Where("tenant_id = ? AND user_id = ? AND goal_key = ?", tenantID, userID, goalID).
		Limit(1).
		Rows()
	if err != nil {
		return goal.Goal{}, err
	}
	defer rows.Close()
	if rows.Next() {
		row, err := scanGoal(rows)
		if err != nil {
			return goal.Goal{}, err
		}
		return row.Goal, nil
	}
	if err := rows.Err(); err != nil {
		return goal.Goal{}, err
	}
	return goal.Goal{}, ErrNotFound
}

func (r *GormRepository) ListGoals(ctx context.Context, tenantID, userID uint64, filter goal.ListFilter, limit int) ([]goal.Goal, error) {
	r.log(ctx, "goal.list", "mysql.GormRepository.ListGoals", "list tenant goals")
	query := r.goalSelect(ctx).Where("tenant_id = ? AND user_id = ?", tenantID, userID)
	if status := goalListStatus(filter); status != "" {
		query = query.Where("status = ?", status)
	}
	rows, err := query.
		Order("updated_at DESC").
		Limit(normalizeLimit(limit)).
		Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanGoals(rows)
}

func (r *GormRepository) UpdateGoal(ctx context.Context, tenantID, userID uint64, item goal.Goal) error {
	r.log(ctx, "goal.update", "mysql.GormRepository.UpdateGoal", "update tenant goal")
	if err := item.Validate(); err != nil {
		return err
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = time.Now().UTC()
	}
	updates := map[string]any{
		"objective":              item.Objective,
		"status":                 string(item.Status),
		"session_id":             nullableStringPtr(item.SessionID),
		"cwd":                    nullableStringPtr(item.CWD),
		"model":                  nullableStringPtr(item.Model),
		"turn_budget":            item.TurnBudget,
		"token_budget":           item.TokenBudget,
		"turns_used":             item.TurnsUsed,
		"input_tokens":           item.InputTokens,
		"output_tokens":          item.OutputTokens,
		"last_blocker":           nullableStringPtr(item.LastBlocker),
		"repeated_blocker_count": item.RepeatedBlockerCount,
		"last_checkpoint":        nullableStringPtr(item.LastCheckpoint),
		"last_reason":            nullableStringPtr(item.LastReason),
		"last_next_action":       nullableStringPtr(item.LastNextAction),
		"error_message":          nullableStringPtr(item.Error),
		"updated_at":             item.UpdatedAt,
	}
	result := r.with(ctx).Model(&gormGoal{}).
		Where("tenant_id = ? AND user_id = ? AND goal_key = ?", tenantID, userID, item.ID).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *GormRepository) AppendGoalEvent(ctx context.Context, tenantID, userID uint64, event goal.Event) error {
	r.log(ctx, "goal_event.append", "mysql.GormRepository.AppendGoalEvent", "append tenant goal event")
	if strings.TrimSpace(event.ID) == "" {
		return errors.New("goal event id is required")
	}
	if strings.TrimSpace(event.GoalID) == "" {
		return errors.New("goal event goal id is required")
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	goalNumericID, err := r.goalNumericID(ctx, tenantID, userID, event.GoalID)
	if err != nil {
		return err
	}
	row := gormGoalEvent{
		TenantID:     tenantID,
		UserID:       userID,
		GoalID:       goalNumericID,
		EventKey:     event.ID,
		EventType:    string(event.Type),
		Message:      nullableStringPtr(event.Message),
		SessionID:    nullableStringPtr(event.SessionID),
		TurnIndex:    event.Turn,
		InputTokens:  event.InputTokens,
		OutputTokens: event.OutputTokens,
		DurationMS:   event.DurationMS,
		Checkpoint:   nullableStringPtr(event.Checkpoint),
		Status:       nullableStringPtr(string(event.Status)),
		Reason:       nullableStringPtr(event.Reason),
		NextAction:   nullableStringPtr(event.NextAction),
		BlockerKey:   nullableStringPtr(event.BlockerKey),
		ErrorMessage: nullableStringPtr(event.Error),
		CreatedAt:    event.CreatedAt,
	}
	return r.with(ctx).Create(&row).Error
}

func (r *GormRepository) ListGoalEvents(ctx context.Context, tenantID, userID uint64, goalID string, limit int) ([]goal.Event, error) {
	r.log(ctx, "goal_event.list", "mysql.GormRepository.ListGoalEvents", "list tenant goal events")
	goalNumericID, err := r.goalNumericID(ctx, tenantID, userID, goalID)
	if err != nil {
		return nil, err
	}
	rows, err := r.with(ctx).Table("tenant_goal_events").
		Select("event_key, event_type, COALESCE(message, ''), COALESCE(session_id, ''), turn_index, input_tokens, output_tokens, duration_ms, COALESCE(checkpoint, ''), COALESCE(status, ''), COALESCE(reason, ''), COALESCE(next_action, ''), COALESCE(blocker_key, ''), COALESCE(error_message, ''), created_at").
		Where("tenant_id = ? AND user_id = ? AND goal_id = ?", tenantID, userID, goalNumericID).
		Order("created_at ASC").
		Limit(normalizeLimit(limit)).
		Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanGoalEvents(rows, goalID)
}

func (r *GormRepository) SaveGoalPlan(ctx context.Context, tenantID, userID uint64, plan goal.GoalPlan) error {
	r.log(ctx, "goal_plan.save", "mysql.GormRepository.SaveGoalPlan", "save tenant goal plan")
	if err := plan.Validate(); err != nil {
		return err
	}
	goalNumericID, err := r.goalNumericID(ctx, tenantID, userID, plan.GoalID)
	if err != nil {
		return err
	}
	if plan.CreatedAt.IsZero() {
		plan.CreatedAt = time.Now().UTC()
	}
	if plan.UpdatedAt.IsZero() {
		plan.UpdatedAt = plan.CreatedAt
	}
	data, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	row := gormGoalPlan{
		TenantID:      tenantID,
		UserID:        userID,
		GoalID:        goalNumericID,
		Version:       plan.Version,
		Summary:       nullableStringPtr(plan.Summary),
		CurrentStepID: nullableStringPtr(plan.CurrentStepID),
		PlanJSON:      string(data),
		CreatedAt:     plan.CreatedAt,
		UpdatedAt:     plan.UpdatedAt,
	}
	return r.with(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "goal_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"version",
			"summary",
			"current_step_id",
			"plan_json",
			"updated_at",
		}),
	}).Create(&row).Error
}

func (r *GormRepository) GetGoalPlan(ctx context.Context, tenantID, userID uint64, goalID string) (goal.GoalPlan, bool, error) {
	r.log(ctx, "goal_plan.get", "mysql.GormRepository.GetGoalPlan", "get tenant goal plan")
	rows, err := r.with(ctx).Table("tenant_goal_plans AS p").
		Select("g.goal_key, p.plan_json").
		Joins("INNER JOIN tenant_goals g ON g.id = p.goal_id").
		Where("p.tenant_id = ? AND p.user_id = ? AND g.goal_key = ?", tenantID, userID, goalID).
		Limit(1).
		Rows()
	if err != nil {
		return goal.GoalPlan{}, false, err
	}
	defer rows.Close()
	if rows.Next() {
		var row goalPlanRow
		if err := rows.Scan(&row.GoalID, &row.PlanJSON); err != nil {
			return goal.GoalPlan{}, false, err
		}
		plan, err := decodeGoalPlan(row.GoalID, row.PlanJSON)
		if err != nil {
			return goal.GoalPlan{}, false, err
		}
		return plan, true, nil
	}
	if err := rows.Err(); err != nil {
		return goal.GoalPlan{}, false, err
	}
	return goal.GoalPlan{}, false, nil
}

func (r *GormRepository) AppendGoalEvidence(ctx context.Context, tenantID, userID uint64, evidence goal.GoalEvidence) error {
	r.log(ctx, "goal_evidence.append", "mysql.GormRepository.AppendGoalEvidence", "append tenant goal evidence")
	if err := evidence.Validate(); err != nil {
		return err
	}
	if evidence.CreatedAt.IsZero() {
		evidence.CreatedAt = time.Now().UTC()
	}
	goalNumericID, err := r.goalNumericID(ctx, tenantID, userID, evidence.GoalID)
	if err != nil {
		return err
	}
	row := gormGoalEvidence{
		TenantID:     tenantID,
		UserID:       userID,
		GoalID:       goalNumericID,
		EvidenceKey:  evidence.ID,
		EvidenceType: string(evidence.Type),
		Summary:      evidence.Summary,
		Command:      nullableStringPtr(evidence.Command),
		ExitCode:     evidence.ExitCode,
		Passed:       evidence.Passed,
		PayloadJSON:  nullableRawJSONPtr(evidence.Payload),
		CreatedAt:    evidence.CreatedAt,
	}
	return r.with(ctx).Create(&row).Error
}

func (r *GormRepository) ListGoalEvidence(ctx context.Context, tenantID, userID uint64, goalID string, limit int) ([]goal.GoalEvidence, error) {
	r.log(ctx, "goal_evidence.list", "mysql.GormRepository.ListGoalEvidence", "list tenant goal evidence")
	goalNumericID, err := r.goalNumericID(ctx, tenantID, userID, goalID)
	if err != nil {
		return nil, err
	}
	rows, err := r.with(ctx).Table("tenant_goal_evidence").
		Select("evidence_key, evidence_type, summary, COALESCE(command, ''), exit_code, passed, COALESCE(payload_json, ''), created_at").
		Where("tenant_id = ? AND user_id = ? AND goal_id = ?", tenantID, userID, goalNumericID).
		Order("created_at ASC").
		Limit(normalizeLimit(limit)).
		Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanGoalEvidence(rows, goalID)
}

func (r *GormRepository) goalNumericID(ctx context.Context, tenantID, userID uint64, goalID string) (uint64, error) {
	var id uint64
	err := r.with(ctx).Table("tenant_goals").
		Select("id").
		Where("tenant_id = ? AND user_id = ? AND goal_key = ?", tenantID, userID, goalID).
		Limit(1).
		Take(&id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, ErrNotFound
	}
	return id, err
}

func (r *GormRepository) InsertAuditLog(ctx context.Context, input AuditLogInput) (uint64, error) {
	r.log(ctx, "audit.insert", "mysql.GormRepository.InsertAuditLog", "insert tenant audit log")
	if input.TraceID == "" {
		input.TraceID = observability.TraceID(ctx)
	}
	row := gormAuditLog{
		TenantID:     input.TenantID,
		ActorUserID:  nullableUint64Ptr(input.ActorUserID),
		Action:       input.Action,
		ResourceType: input.ResourceType,
		ResourceID:   nullableStringPtr(input.ResourceID),
		MetadataJSON: nullableStringPtr(input.MetadataJSON),
		TraceID:      nullableStringPtr(input.TraceID),
	}
	err := r.with(ctx).Create(&row).Error
	return gormInsertedID(row.ID, err)
}

func (r *GormRepository) ListAuditLogs(ctx context.Context, tenantID uint64, limit int) ([]AuditLog, error) {
	r.log(ctx, "audit.list", "mysql.GormRepository.ListAuditLogs", "list tenant audit logs")
	rows, err := r.with(ctx).Table("tenant_audit_logs").
		Select("id, tenant_id, COALESCE(actor_user_id, 0), action, resource_type, COALESCE(resource_id, ''), COALESCE(metadata_json, ''), COALESCE(trace_id, ''), created_at").
		Where("tenant_id = ?", tenantID).
		Order("created_at DESC").
		Limit(normalizeLimit(limit)).
		Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAuditLogs(rows)
}

func (r *GormRepository) ListAuditLogsFiltered(ctx context.Context, tenantID uint64, opts ListOptions) ([]AuditLog, error) {
	r.log(ctx, "audit.list_filtered", "mysql.GormRepository.ListAuditLogsFiltered", "list tenant audit logs filtered")
	query := r.with(ctx).Table("tenant_audit_logs").
		Select("id, tenant_id, COALESCE(actor_user_id, 0), action, resource_type, COALESCE(resource_id, ''), COALESCE(metadata_json, ''), COALESCE(trace_id, ''), created_at").
		Where("tenant_id = ?", tenantID)
	query = applySearch(query, opts.Search, auditSearchSchema)
	rows, err := query.
		Order("created_at DESC").
		Limit(normalizeLimit(opts.Limit)).
		Offset(int(opts.Cursor)).
		Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAuditLogs(rows)
}

func (r *GormRepository) InsertTelemetryEvent(ctx context.Context, input TelemetryEventInput) (uint64, error) {
	event := telemetry.Normalize(ctx, input.Event)
	r.log(ctx, "telemetry.record", "mysql.GormRepository.InsertTelemetryEvent", "insert telemetry event", "event_name", event.Name)
	row := gormTelemetryEvent{
		TenantID:                            input.TenantID,
		UserID:                              nullableUint64Ptr(input.UserID),
		EventName:                           event.Name,
		Category:                            nullableStringPtr(event.Category),
		Source:                              nullableStringPtr(event.Source),
		Status:                              nullableStringPtr(event.Status),
		TraceID:                             nullableStringPtr(event.TraceID),
		SessionID:                           nullableUint64Ptr(event.SessionID),
		ResourceType:                        nullableStringPtr(event.ResourceType),
		ResourceID:                          nullableStringPtr(event.ResourceID),
		Model:                               nullableStringPtr(event.Model),
		ToolName:                            nullableStringPtr(event.ToolName),
		DurationMS:                          nullableInt64Ptr(event.DurationMS),
		InputTokens:                         event.InputTokens,
		OutputTokens:                        event.OutputTokens,
		CacheCreationInputTokens:            event.CacheCreationInputTokens,
		CacheReadInputTokens:                event.CacheReadInputTokens,
		CacheCreationEphemeral1hInputTokens: event.CacheCreationEphemeral1hInputTokens,
		CacheCreationEphemeral5mInputTokens: event.CacheCreationEphemeral5mInputTokens,
		ErrorMessage:                        nullableStringPtr(event.Error),
		PropertiesJSON:                      nullableStringPtr(telemetry.MarshalProperties(event.Properties)),
		OccurredAt:                          event.OccurredAt,
	}
	err := r.with(ctx).Create(&row).Error
	return gormInsertedID(row.ID, err)
}

func (r *GormRepository) ListTelemetryEvents(ctx context.Context, tenantID uint64, limit int) ([]TelemetryEvent, error) {
	r.log(ctx, "telemetry.list", "mysql.GormRepository.ListTelemetryEvents", "list tenant telemetry events")
	rows, err := r.telemetrySelect(ctx).
		Where("tenant_id = ?", tenantID).
		Order("occurred_at DESC, id DESC").
		Limit(normalizeLimit(limit)).
		Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTelemetryEvents(rows)
}

func (r *GormRepository) ListTelemetryEventsFiltered(ctx context.Context, tenantID uint64, opts ListOptions) ([]TelemetryEvent, error) {
	r.log(ctx, "telemetry.list_filtered", "mysql.GormRepository.ListTelemetryEventsFiltered", "list tenant telemetry events filtered")
	query := r.telemetrySelect(ctx).Where("tenant_id = ?", tenantID)
	query = applySearch(query, opts.Search, telemetrySearchSchema)
	rows, err := query.
		Order("occurred_at DESC, id DESC").
		Limit(normalizeLimit(opts.Limit)).
		Offset(int(opts.Cursor)).
		Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTelemetryEvents(rows)
}

func (r *GormRepository) GetTenantQuotaConfig(ctx context.Context, tenantID uint64) (QuotaConfig, error) {
	r.log(ctx, "quota.config.get", "mysql.GormRepository.GetTenantQuotaConfig", "get tenant quota config")
	cfg, err := scanQuotaConfig(r.with(ctx).Raw(selectQuotaConfigSQL, tenantID).Row())
	if errors.Is(err, sql.ErrNoRows) {
		return quota.DefaultConfig(tenantID), nil
	}
	return cfg, err
}

func (r *GormRepository) UpsertTenantQuotaConfig(ctx context.Context, input QuotaConfigInput) error {
	cfg, err := quota.NormalizeConfig(input)
	if err != nil {
		return err
	}
	r.log(ctx, "quota.config.save", "mysql.GormRepository.UpsertTenantQuotaConfig", "save tenant quota config")
	query := upsertQuotaConfigSQL
	if isSQLite(r.db) {
		query = upsertQuotaConfigSQLiteSQL
	}
	return r.with(ctx).Exec(query,
		cfg.TenantID,
		cfg.QuotaEnabled,
		nullableUint64PtrValue(cfg.QPSLimit),
		nullableUint64PtrValue(cfg.DailyTokenLimit),
		nullableUint64PtrValue(cfg.DailyMessageLimit),
		nullableUint64PtrValue(cfg.MaxConcurrentRequests),
		cfg.Timezone,
		cfg.ReserveOutputTokens,
		cfg.Status,
		nullableUint64(cfg.UpdatedByUserID),
	).Error
}

func (r *GormRepository) InsertUsageLedger(ctx context.Context, input UsageLedgerInput) (uint64, error) {
	r.log(ctx, "quota.ledger.insert", "mysql.GormRepository.InsertUsageLedger", "insert tenant usage ledger")
	result := r.with(ctx).Exec(insertUsageLedgerSQL,
		input.RequestID,
		input.TenantID,
		nullableUint64(input.UserID),
		nullableUint64(input.SessionID),
		nullableString(input.TraceID),
		input.Source,
		nullableString(input.Route),
		nullableString(input.Model),
		nullableString(input.Provider),
		input.Turn,
		nullableString(input.UsageSource),
		input.Status,
		input.Estimated,
		input.ReservedInputTokens,
		input.ReservedOutputTokens,
		input.InputTokens,
		input.OutputTokens,
		input.CacheReadInputTokens,
		input.CacheCreationInputTokens,
		input.CacheCreationEphemeral1h,
		input.CacheCreationEphemeral5m,
		input.TotalTokens,
		nullableString(input.ErrorCode),
		nullableString(input.ErrorMessage),
		input.StartedAt,
		input.FinishedAt,
	)
	return 0, result.Error
}

func (r *GormRepository) UpdateUsageLedgerSettlement(ctx context.Context, requestID string, input UsageLedgerInput) error {
	r.log(ctx, "quota.ledger.settle", "mysql.GormRepository.UpdateUsageLedgerSettlement", "settle tenant usage ledger")
	return r.with(ctx).Exec(updateUsageLedgerSQL,
		input.Provider,
		input.Turn,
		input.UsageSource,
		input.Status,
		input.Estimated,
		input.InputTokens,
		input.OutputTokens,
		input.CacheReadInputTokens,
		input.CacheCreationInputTokens,
		input.CacheCreationEphemeral1h,
		input.CacheCreationEphemeral5m,
		input.TotalTokens,
		nullableString(input.ErrorCode),
		nullableString(input.ErrorMessage),
		input.FinishedAt,
		requestID,
	).Error
}

func (r *GormRepository) UpsertUsageDailyDelta(ctx context.Context, delta UsageDailyDelta) error {
	r.log(ctx, "quota.usage.daily", "mysql.GormRepository.UpsertUsageDailyDelta", "upsert tenant daily usage delta")
	query := upsertUsageDailySQL
	if isSQLite(r.db) {
		query = upsertUsageDailySQLiteSQL
	}
	return r.with(ctx).Exec(query,
		delta.TenantID,
		delta.UsageDate,
		usageDimension(delta.Source),
		usageDimension(delta.Model),
		delta.RequestCount,
		delta.MessageCount,
		delta.InputTokens,
		delta.OutputTokens,
		delta.CacheReadInputTokens,
		delta.CacheCreationInputTokens,
		delta.TotalTokens,
		delta.RejectedCount,
	).Error
}

func (r *GormRepository) ListTenantUsageDaily(ctx context.Context, tenantID uint64, opts ListOptions) ([]UsageDaily, error) {
	r.log(ctx, "quota.usage.daily.list", "mysql.GormRepository.ListTenantUsageDaily", "list tenant usage daily")
	search, pattern := normalizeSearch(opts.Search)
	rows, err := r.with(ctx).Raw(listUsageDailySQL, tenantID, nil, nil, nil, nil, search, pattern, pattern, normalizeLimit(opts.Limit), opts.Cursor).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanUsageDailyRows(rows)
}

func (r *GormRepository) ListTenantUsageLedger(ctx context.Context, tenantID uint64, opts ListOptions) ([]UsageLedger, error) {
	r.log(ctx, "quota.usage.ledger.list", "mysql.GormRepository.ListTenantUsageLedger", "list tenant usage ledger")
	search, pattern := normalizeSearch(opts.Search)
	rows, err := r.with(ctx).Raw(listUsageLedgerSQL, tenantID, nil, nil, nil, nil, "", "", "", "", search, pattern, pattern, pattern, pattern, normalizeLimit(opts.Limit), opts.Cursor).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanUsageLedgerRows(rows)
}

func (r *GormRepository) InsertQuotaEvent(ctx context.Context, event QuotaEvent) (uint64, error) {
	r.log(ctx, "quota.event.insert", "mysql.GormRepository.InsertQuotaEvent", "insert tenant quota event")
	result := r.with(ctx).Exec(insertQuotaEventSQL,
		event.TenantID,
		nullableUint64(event.UserID),
		nullableString(event.RequestID),
		event.EventType,
		nullableString(event.LimitType),
		nullableUint64(event.LimitValue),
		nullableUint64(event.CurrentValue),
		nullableString(event.Source),
		nullableString(event.Route),
		nullableString(event.Model),
		nullableString(event.TraceID),
		nullableString(event.MetadataJSON),
	)
	return 0, result.Error
}

func (r *GormRepository) ListQuotaEvents(ctx context.Context, tenantID uint64, opts ListOptions) ([]QuotaEvent, error) {
	r.log(ctx, "quota.event.list", "mysql.GormRepository.ListQuotaEvents", "list tenant quota events")
	search, pattern := normalizeSearch(opts.Search)
	rows, err := r.with(ctx).Raw(listQuotaEventsSQL, tenantID, search, pattern, pattern, pattern, pattern, pattern, pattern, pattern, normalizeLimit(opts.Limit), opts.Cursor).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanQuotaEventRows(rows)
}

func (r *GormRepository) telemetrySelect(ctx context.Context) *gorm.DB {
	return r.with(ctx).Table("tenant_telemetry_events").
		Select("id, tenant_id, COALESCE(user_id, 0), event_name, COALESCE(category, ''), COALESCE(source, ''), COALESCE(status, ''), COALESCE(trace_id, ''), COALESCE(session_id, 0), COALESCE(resource_type, ''), COALESCE(resource_id, ''), COALESCE(model, ''), COALESCE(tool_name, ''), COALESCE(duration_ms, 0), input_tokens, output_tokens, cache_creation_input_tokens, cache_read_input_tokens, cache_creation_ephemeral_1h_input_tokens, cache_creation_ephemeral_5m_input_tokens, COALESCE(error_message, ''), COALESCE(properties_json, ''), occurred_at, created_at")
}

func (r *GormRepository) goalSelect(ctx context.Context) *gorm.DB {
	return r.with(ctx).Table("tenant_goals").
		Select("id, goal_key, objective, status, COALESCE(session_id, ''), COALESCE(cwd, ''), COALESCE(model, ''), turn_budget, token_budget, turns_used, input_tokens, output_tokens, COALESCE(last_blocker, ''), repeated_blocker_count, COALESCE(last_checkpoint, ''), COALESCE(last_reason, ''), COALESCE(last_next_action, ''), COALESCE(error_message, ''), created_at, updated_at")
}

func (r *GormRepository) skillOverrideQuery(ctx context.Context) *gorm.DB {
	return r.with(ctx).Table("tenant_user_skill_overrides AS o").
		Select("o.id, o.skill_id, s.skill_key, s.name, s.version, o.enabled, o.config_json, o.updated_at").
		Joins("INNER JOIN tenant_skills s ON s.id = o.skill_id")
}

func (r *GormRepository) effectiveSkillQuery(ctx context.Context, userID uint64) *gorm.DB {
	return r.with(ctx).Table("tenant_skills AS s").
		Select("s.id, s.skill_key, s.name, s.description, s.content_md, COALESCE(o.config_json, s.config_json), s.package_ref, s.package_sha256, s.manifest_json, s.runtime_ref, s.version, COALESCE(o.enabled, s.enabled), s.updated_at, COALESCE(o.id, 0)").
		Joins("LEFT JOIN tenant_user_skill_overrides o ON o.tenant_id = s.tenant_id AND o.user_id = ? AND o.skill_id = s.id", userID)
}

func (r *GormRepository) with(ctx context.Context) *gorm.DB {
	return r.db.WithContext(ctx)
}

func (r *GormRepository) log(ctx context.Context, action, function, message string, attrs ...any) {
	observability.Info(ctx, r.logger, action, function, message, attrs...)
}

func nullableStringPtr(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func nullableUint64Ptr(value uint64) *uint64 {
	if value == 0 {
		return nil
	}
	return &value
}

func nullableInt64Ptr(value int64) *int64 {
	if value == 0 {
		return nil
	}
	return &value
}

func nullableRawJSONPtr(value json.RawMessage) *string {
	if len(value) == 0 {
		return nil
	}
	raw := string(value)
	return &raw
}

func gormInsertedID(id uint64, err error) (uint64, error) {
	if err != nil {
		return 0, err
	}
	if id == 0 {
		return 0, fmt.Errorf("last insert id is zero")
	}
	return id, nil
}
