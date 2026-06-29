package limiter

import (
	"context"
	_ "embed"
	"time"

	"github.com/redis/go-redis/v9"
)

//go:embed scripts/token_bucket.lua
var tokenBucketSrc string

//go:embed scripts/token_bucket_reconcile.lua
var tokenBucketReconcileSrc string

var (
	tokenBucketScript    = redis.NewScript(tokenBucketSrc)
	tokenBucketReconcile = redis.NewScript(tokenBucketReconcileSrc)
)

// TokenBucket is the default algorithm. It natively supports weighted cost,
// which is what makes it the right fit for variable-cost LLM requests.
type TokenBucket struct {
	rdb      redis.Cmdable // *redis.Client or *redis.ClusterClient
	capacity float64       // burst ceiling
	refill   float64       // tokens per second (the sustained rate)
	ttl      time.Duration // idle buckets auto-expire
}

func NewTokenBucket(rdb redis.Cmdable, capacity, refillPerSec float64, ttl time.Duration) *TokenBucket {
	if ttl <= 0 {
		ttl = time.Hour
	}
	return &TokenBucket{rdb: rdb, capacity: capacity, refill: refillPerSec, ttl: ttl}
}

func (t *TokenBucket) Allow(ctx context.Context, key string, cost float64) (Decision, error) {
	now := time.Now()
	res, err := tokenBucketScript.Run(ctx, t.rdb,
		[]string{key},
		t.capacity, t.refill, cost, now.UnixMilli(), t.ttl.Milliseconds(),
	).Result()
	if err != nil {
		return Decision{}, err
	}
	return parseDecision(res, t.capacity, now)
}

// Reconcile settles the optimistic pre-flight debit: delta = actual - estimate.
func (t *TokenBucket) Reconcile(ctx context.Context, key string, delta float64) error {
	return tokenBucketReconcile.Run(ctx, t.rdb,
		[]string{key},
		t.capacity, delta, t.ttl.Milliseconds(),
	).Err()
}
