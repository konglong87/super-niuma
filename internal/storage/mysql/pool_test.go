package mysql

import (
	"database/sql"
	"testing"
	"time"
)

type recordingPool struct {
	maxOpen     int
	maxIdle     int
	maxLifetime time.Duration
	maxIdleTime time.Duration
	calls       []string
}

func (p *recordingPool) SetMaxOpenConns(n int) {
	p.maxOpen = n
	p.calls = append(p.calls, "SetMaxOpenConns")
}

func (p *recordingPool) SetMaxIdleConns(n int) {
	p.maxIdle = n
	p.calls = append(p.calls, "SetMaxIdleConns")
}

func (p *recordingPool) SetConnMaxLifetime(d time.Duration) {
	p.maxLifetime = d
	p.calls = append(p.calls, "SetConnMaxLifetime")
}

func (p *recordingPool) SetConnMaxIdleTime(d time.Duration) {
	p.maxIdleTime = d
	p.calls = append(p.calls, "SetConnMaxIdleTime")
}

// 四个旋钮一个都不能漏：只设 MaxOpenConns 仍然会拿到被 wait_timeout 踢掉的死连接。
func TestApplyPoolConfigSetsAllFourKnobs(t *testing.T) {
	pool := &recordingPool{}
	cfg := applyPoolConfig(pool, PoolConfig{
		MaxOpenConns:    40,
		MaxIdleConns:    20,
		ConnMaxLifetime: time.Hour,
		ConnMaxIdleTime: 90 * time.Second,
	})

	want := []string{"SetMaxOpenConns", "SetMaxIdleConns", "SetConnMaxLifetime", "SetConnMaxIdleTime"}
	if len(pool.calls) != len(want) {
		t.Fatalf("calls = %v, want %v", pool.calls, want)
	}
	for i, name := range want {
		if pool.calls[i] != name {
			t.Fatalf("calls = %v, want %v", pool.calls, want)
		}
	}
	if pool.maxOpen != 40 || pool.maxIdle != 20 || pool.maxLifetime != time.Hour || pool.maxIdleTime != 90*time.Second {
		t.Fatalf("pool = %+v", pool)
	}
	if cfg.MaxOpenConns != 40 {
		t.Fatalf("returned config = %+v", cfg)
	}
}

// 零值配置不能退化成 database/sql 的默认值，那正是要修的 bug。
func TestApplyPoolConfigBoundsTheZeroValue(t *testing.T) {
	pool := &recordingPool{}
	applyPoolConfig(pool, PoolConfig{})

	if pool.maxOpen != defaultMaxOpenConns {
		t.Fatalf("MaxOpenConns = %d, want a bounded default (0 means unlimited to database/sql)", pool.maxOpen)
	}
	if pool.maxIdle != defaultMaxIdleConns {
		t.Fatalf("MaxIdleConns = %d, want %d", pool.maxIdle, defaultMaxIdleConns)
	}
	if pool.maxLifetime != defaultConnMaxLifetime {
		t.Fatalf("ConnMaxLifetime = %s, want %s", pool.maxLifetime, defaultConnMaxLifetime)
	}
	if pool.maxIdleTime != defaultConnMaxIdleTime {
		t.Fatalf("ConnMaxIdleTime = %s, want %s", pool.maxIdleTime, defaultConnMaxIdleTime)
	}
}

func TestPoolConfigClampsIdleToOpen(t *testing.T) {
	cfg := PoolConfig{MaxOpenConns: 4, MaxIdleConns: 32}.normalize()
	if cfg.MaxIdleConns != 4 {
		t.Fatalf("MaxIdleConns = %d, want it clamped to MaxOpenConns", cfg.MaxIdleConns)
	}
}

func TestPoolConfigFromEnvOverridesDefaults(t *testing.T) {
	t.Setenv(envMaxOpenConns, "64")
	t.Setenv(envMaxIdleConns, "16")
	t.Setenv(envConnMaxLifetime, "10m")
	t.Setenv(envConnMaxIdleTime, "45s")

	cfg := PoolConfigFromEnv()
	if cfg.MaxOpenConns != 64 || cfg.MaxIdleConns != 16 {
		t.Fatalf("conn counts = %+v", cfg)
	}
	if cfg.ConnMaxLifetime != 10*time.Minute || cfg.ConnMaxIdleTime != 45*time.Second {
		t.Fatalf("durations = %+v", cfg)
	}
}

func TestPoolConfigFromEnvFallsBackOnGarbage(t *testing.T) {
	t.Setenv(envMaxOpenConns, "lots")
	t.Setenv(envConnMaxLifetime, "half an hour")

	cfg := PoolConfigFromEnv()
	if cfg.MaxOpenConns != defaultMaxOpenConns {
		t.Fatalf("MaxOpenConns = %d, want the default", cfg.MaxOpenConns)
	}
	if cfg.ConnMaxLifetime != defaultConnMaxLifetime {
		t.Fatalf("ConnMaxLifetime = %s, want the default", cfg.ConnMaxLifetime)
	}
}

// connPool 的方法集必须和 *sql.DB 对得上，否则 OpenGormRepository 那边编译不过 ——
// 这条同时保证上面几个测试测的不是一个和真实类型脱节的假接口。
func TestSQLDBSatisfiesConnPool(t *testing.T) {
	// sql.Open 不会连服务器，这里只借它拿一个真的 *sql.DB。
	db, err := sql.Open("mysql", "user:pass@tcp(127.0.0.1:3306)/db")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	cfg := applyPoolConfig(db, PoolConfig{MaxOpenConns: 7, MaxIdleConns: 3})
	if got := db.Stats().MaxOpenConnections; got != 7 {
		t.Fatalf("MaxOpenConnections = %d, want 7", got)
	}
	if cfg.MaxIdleConns != 3 {
		t.Fatalf("config = %+v", cfg)
	}
}
