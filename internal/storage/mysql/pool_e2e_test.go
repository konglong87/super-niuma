package mysql

import (
	"context"
	"os"
	"testing"
	"time"
)

// 单元测试只能证明 applyPoolConfig 会设那四个值；这条 opt-in e2e 证明
// OpenGormRepository 真的调用了它，而不是把配置留在原地(AUDIT-P0-11)。
func TestMySQLE2EOpenGormRepositoryAppliesPoolConfig(t *testing.T) {
	dsn := os.Getenv("NIUMA_MYSQL_E2E_DSN")
	if dsn == "" {
		t.Skip("set NIUMA_MYSQL_E2E_DSN to run the real MySQL pool check")
	}
	t.Setenv(envMaxOpenConns, "7")
	t.Setenv(envMaxIdleConns, "3")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repo, err := OpenGormRepository(ctx, dsn, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repo.Close() }()

	stats, err := repo.PoolStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.MaxOpenConnections != 7 {
		t.Fatalf("MaxOpenConnections = %d, want 7 from %s", stats.MaxOpenConnections, envMaxOpenConns)
	}
}
