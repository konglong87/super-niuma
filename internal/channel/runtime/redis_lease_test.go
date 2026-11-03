package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestRedisLeaseExcludesConcurrentWorkerAndReleases(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	lease := NewRedisLease(client, "test:channel")
	release, err := lease.Acquire(context.Background(), 1, 2, "worker-a", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := lease.Acquire(context.Background(), 1, 2, "worker-b", 3*time.Second); err == nil {
		t.Fatal("second worker acquired active lease")
	}
	release()
	if next, err := lease.Acquire(context.Background(), 1, 2, "worker-b", 3*time.Second); err != nil {
		t.Fatal(err)
	} else {
		next()
	}
}
