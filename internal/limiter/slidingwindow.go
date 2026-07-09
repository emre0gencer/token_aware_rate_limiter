package limiter

import (
	"context"
	_ "embed"
	"time"

	"github.com/redis/go-redis/v9"
)

//go:embed scripts/sliding_window.lua
var slidingWindowSrc string

var slidingWindowScript = redis.NewScript(slidingWindowSrc)

// SlidingWindow is the comparison algorithm to A/B against TokenBucket: a
// current+previous weighted counter that enforces a strict average without the
// 2x boundary burst of the fixed window (step 3). Like the token bucket it runs
// its whole check inside one Lua script (scripts/sliding_window.lua), so the
// state stays shared and correct across replicas.
type SlidingWindow struct {
	rdb    redis.Cmdable
	limit  float64
	window time.Duration
	ttl    time.Duration
}

func NewSlidingWindow(rdb redis.Cmdable, limit float64, window, ttl time.Duration) *SlidingWindow {
	if ttl <= 0 {
		ttl = 2 * window
	}
	return &SlidingWindow{rdb: rdb, limit: limit, window: window, ttl: ttl}
}

func (s *SlidingWindow) Allow(ctx context.Context, key string, cost float64) (Decision, error) {
	now := time.Now()
	_ = now

	// HINT 1 — one atomic round-trip, same shape as TokenBucket.Allow. The ARGV
	// order MUST match the header in scripts/sliding_window.lua:
	//     res, err := slidingWindowScript.Run(ctx, s.rdb,
	//         []string{key},
	//         s.limit, s.window.Milliseconds(), cost, now.UnixMilli(), s.ttl.Milliseconds(),
	//     ).Result()
	// HINT 2 — on err return (Decision{}, err); let the gateway decide policy.
	// HINT 3 — parse { allowed, remaining, reset_ms } with the shared helper:
	//     return parseDecision(res, s.limit, now)

	return Decision{}, nil // TODO
}

// Reconcile nudges the current-window counter by delta. Windows are coarser than
// buckets, so settlement here is best-effort and heals on window rollover.
func (s *SlidingWindow) Reconcile(ctx context.Context, key string, delta float64) error {
	// TODO: delta == 0 -> return nil. Otherwise bump the hash's "cur" field by
	// delta. HINCRBYFLOAT can drive it negative on a large refund; keep it simple
	// here (exactness lives in TokenBucket):
	//     return s.rdb.HIncrByFloat(ctx, key, "cur", delta).Err()
	return nil // TODO
}
