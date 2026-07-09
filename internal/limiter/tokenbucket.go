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

// The Lua sources are loaded once and run by SHA (EVALSHA, with an EVAL
// fallback) on each call, so the atomic read-refill-debit-write happens
// server-side in one round-trip — the whole point of STEP 4.
var (
	tokenBucketScript    = redis.NewScript(tokenBucketSrc)
	tokenBucketReconcile = redis.NewScript(tokenBucketReconcileSrc)
)

// TokenBucket is the default algorithm (build-order STEP 4). It natively
// supports weighted cost, which is what makes it the right fit for
// variable-cost LLM requests. Unlike the in-memory Bucket (step 2) or the
// INCRBY FixedWindow (step 3), read -> lazy-refill -> conditional debit ->
// write all run inside ONE Lua script, so N stateless replicas share a single
// correct budget with no read-modify-write race.
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
	_ = now

	// HINT 1 — run the script atomically. redis.Script.Run does EVALSHA (falling
	// back to EVAL on NOSCRIPT), so the entire bucket update is one race-free
	// round-trip. KEYS[1] is the bucket key; the ARGV order MUST match the header
	// contract in scripts/token_bucket.lua exactly:
	//     res, err := tokenBucketScript.Run(ctx, t.rdb,
	//         []string{key},
	//         t.capacity, t.refill, cost, now.UnixMilli(), t.ttl.Milliseconds(),
	//     ).Result()

	// HINT 2 — errors bubble up. On err return (Decision{}, err) and let the
	// gateway apply the fail-open/closed policy (ADR-008); don't invent a
	// decision here.

	// HINT 3 — parse. The script returns { allowed(0|1), remaining(string),
	// reset_ms }. parseDecision (limiter.go) turns that array into a Decision and
	// converts reset_ms into ResetAt relative to `now`:
	//     return parseDecision(res, t.capacity, now)

	return Decision{}, nil // TODO: implement via tokenBucketScript.Run + parseDecision
}

// Reconcile settles the optimistic pre-flight debit: delta = actual - estimate.
func (t *TokenBucket) Reconcile(ctx context.Context, key string, delta float64) error {
	// TODO: run tokenBucketReconcile with KEYS = [key] and ARGV = capacity,
	// delta, ttl_ms (see scripts/token_bucket_reconcile.lua). delta > 0 consumes
	// more, delta < 0 refunds; the script clamps to [0, capacity]. Return .Err().
	//     return tokenBucketReconcile.Run(ctx, t.rdb,
	//         []string{key}, t.capacity, delta, t.ttl.Milliseconds()).Err()
	return nil // TODO
}
