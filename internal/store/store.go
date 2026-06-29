// Package store constructs the Redis client used as the shared budget store.
// Tight per-call timeouts come from config; the fail-open/closed decision is
// made by the gateway when a call errors (ADR-008), not here.
package store

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// NewRedis returns a connected client with pooled connections and per-op
// timeouts. timeout bounds each read/write so a slow shard trips the same
// degradation path as a down shard.
func NewRedis(addr string, timeout time.Duration) (*redis.Client, error) {
	if timeout <= 0 {
		timeout = 50 * time.Millisecond
	}
	rdb := redis.NewClient(&redis.Options{
		Addr:         addr,
		ReadTimeout:  timeout,
		WriteTimeout: timeout,
		PoolSize:     64, // reuse connections; avoid per-request TCP handshakes
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, err
	}
	return rdb, nil
}
