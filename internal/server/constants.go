package server

// HTTP header 名。
const (
	headerTenantSkillKeys = "X-Tenant-Skill-Keys"
)

// 兼容多种客户端写法的 user id header 别名（见 resolveUserID）。
var userIDHeaderAliases = []string{"X-User-Id", "X-User-ID", "X-Claude-User-Id"}

// 查询参数名。
const (
	paramLimit   = "limit"
	paramVersion = "version"
	paramEnabled = "enabled"
)

// 复用的错误消息。
const (
	errMsgTenantStorageNotConfig   = "tenant storage is not configured"
	errMsgGoalIDRequired           = "goal id is required"
	errMsgAgentTaskIDRequired      = "agent task id is required"
	errMsgAgentRunnerNotConfig     = "agent runner is not configured"
	errMsgGoalQueryRunnerNotConfig = "goal query runner is not configured"
	errMsgContentRequired          = "content is required"
	errMsgUserKeyRequired          = "user_key is required"
	errMsgTenantKeyRequired        = "tenant_key is required"
	errMsgQueryHandlerNotConfig    = "query handler is not configured"
)
