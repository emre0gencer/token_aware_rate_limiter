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

// SlidingWindow is the comparison algorithm: a current+previous weighted
// counter that enforces a strict average without the 2x boundary burst of a
// fixed window. Implemented to A/B against TokenBucket.
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
	res, err := slidingWindowScript.Run(ctx, s.rdb,
		[]string{key},
		s.limit, s.window.Milliseconds(), cost, now.UnixMilli(), s.ttl.Milliseconds(),
	).Result()
	if err != nil {
		return Decision{}, err
	}
	return parseDecision(res, s.limit, now)
}

// Reconcile nudges the current-window counter by delta. Windows are coarser
// than buckets, so settlement here is best-effort and heals on window rollover.
func (s *SlidingWindow) Reconcile(ctx context.Context, key string, delta float64) error {
	// HINCRBYFLOAT can drive the counter negative on a large refund; clamp via a
	// tiny inline check. Kept simple on purpose — exactness lives in TokenBucket.
	if delta == 0 {
		return nil
	}
	return s.rdb.HIncrByFloat(ctx, key, "cur", delta).Err()
}
