package mysql

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// PoolConfig 是 database/sql 连接池的四个旋钮。全走默认值的后果各不相同，四个都得管
// (AUDIT-P0-11)：
//   - MaxOpenConns 默认无上限，并发一冲就把 MySQL 的 max_connections 打满，
//     整个实例对所有客户端不可用。
//   - MaxIdleConns 默认只有 2，高并发下连接建完就丢，反复付 TCP + 握手的钱。
//   - ConnMaxLifetime 默认无限，于是会攥着已经被 wait_timeout 踢掉的死连接，
//     下一次使用直接报错。
//   - ConnMaxIdleTime 默认无限，空闲连接不回收，穿过 LB / proxy 时同样会拿到死连接。
//
// 刻意不提供「不限制」选项：无上限正是要修的那个 bug。
type PoolConfig struct {
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

const (
	defaultMaxOpenConns    = 25
	defaultMaxIdleConns    = 10
	defaultConnMaxLifetime = 30 * time.Minute
	defaultConnMaxIdleTime = 5 * time.Minute

	envMaxOpenConns    = "NIUMA_MYSQL_MAX_OPEN_CONNS"
	envMaxIdleConns    = "NIUMA_MYSQL_MAX_IDLE_CONNS"
	envConnMaxLifetime = "NIUMA_MYSQL_CONN_MAX_LIFETIME"
	envConnMaxIdleTime = "NIUMA_MYSQL_CONN_MAX_IDLE_TIME"
)

// DefaultPoolConfig 是没有任何环境变量时生效的配置。
func DefaultPoolConfig() PoolConfig {
	return PoolConfig{
		MaxOpenConns:    defaultMaxOpenConns,
		MaxIdleConns:    defaultMaxIdleConns,
		ConnMaxLifetime: defaultConnMaxLifetime,
		ConnMaxIdleTime: defaultConnMaxIdleTime,
	}
}

// PoolConfigFromEnv 用环境变量覆盖默认值。值解析不了就退回默认 —— 拿不准的配置
// 不该把进程拦在启动阶段，实际生效的配置会在 OpenGormRepository 里打日志。
func PoolConfigFromEnv() PoolConfig {
	cfg := DefaultPoolConfig()
	cfg.MaxOpenConns = envInt(envMaxOpenConns, cfg.MaxOpenConns)
	cfg.MaxIdleConns = envInt(envMaxIdleConns, cfg.MaxIdleConns)
	cfg.ConnMaxLifetime = envDuration(envConnMaxLifetime, cfg.ConnMaxLifetime)
	cfg.ConnMaxIdleTime = envDuration(envConnMaxIdleTime, cfg.ConnMaxIdleTime)
	return cfg.normalize()
}

// normalize 把非正值补成默认值，并把空闲上限压到不超过打开上限 ——
// database/sql 本来也会静默这么干，写出来省得配置和实际行为对不上。
func (c PoolConfig) normalize() PoolConfig {
	if c.MaxOpenConns <= 0 {
		c.MaxOpenConns = defaultMaxOpenConns
	}
	if c.MaxIdleConns <= 0 {
		c.MaxIdleConns = defaultMaxIdleConns
	}
	if c.MaxIdleConns > c.MaxOpenConns {
		c.MaxIdleConns = c.MaxOpenConns
	}
	if c.ConnMaxLifetime <= 0 {
		c.ConnMaxLifetime = defaultConnMaxLifetime
	}
	if c.ConnMaxIdleTime <= 0 {
		c.ConnMaxIdleTime = defaultConnMaxIdleTime
	}
	return c
}

// connPool 是 *sql.DB 上这四个 setter 的最小面，测试据此断言四个都被设过。
type connPool interface {
	SetMaxOpenConns(int)
	SetMaxIdleConns(int)
	SetConnMaxLifetime(time.Duration)
	SetConnMaxIdleTime(time.Duration)
}

func applyPoolConfig(pool connPool, cfg PoolConfig) PoolConfig {
	cfg = cfg.normalize()
	pool.SetMaxOpenConns(cfg.MaxOpenConns)
	pool.SetMaxIdleConns(cfg.MaxIdleConns)
	pool.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	pool.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)
	return cfg
}

func envInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func envDuration(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return value
}
