package product

import "os"

const (
	DisplayName      = "牛马 Niuma"
	Name             = "Niuma"
	BinaryName       = "niuma"
	ProductKey       = "niuma"
	ConfigDirName    = ".niuma"
	GuidanceFilename = "niuma.md"

	TranscriptSchemaV1     = "niuma.transcript.v1"
	TranscriptSchemaV2     = "niuma.transcript.v2"
	TenantQuotaRedisPrefix = "niuma:tenant_quota"
	MobileUsageRedisPrefix = "niuma:mobile_usage"

	EnvPrefix              = "NIUMA_"
	LegacyEnvPrefix        = "GOLANG_CC_"
	LegacyClaudeEnvPrefix  = "GOLANG_CLAUDE_CODE_"
	LegacyShortEnvPrefix   = "GO_CLAUDE_CODE_"
	LegacyCompactEnvPrefix = "GO_CLAUDE_"

	LegacyProductName        = "niuma-legacy"
	LegacyConfigDirName      = ".niuma-legacy"
	LegacyTranscriptSchemaV1 = "niuma-legacy.transcript.v1"
	LegacyTranscriptSchemaV2 = "niuma-legacy.transcript.v2"

	ProductNameEnv      = EnvPrefix + "PRODUCT_NAME"
	ProductKeyEnv       = EnvPrefix + "PRODUCT_KEY"
	ConfigDirEnv        = EnvPrefix + "CONFIG_DIR"
	ConfigDirNameEnv    = EnvPrefix + "CONFIG_DIR_NAME"
	GuidanceFilenameEnv = EnvPrefix + "GUIDANCE_FILE"
)

func Getenv(key string) string            { return os.Getenv(key) }
func LookupEnv(key string) (string, bool) { return os.LookupEnv(key) }
func PromoteEnvironment() error           { return nil }
func compatibilityEnvPrefixes() []string  { return []string{EnvPrefix} }
