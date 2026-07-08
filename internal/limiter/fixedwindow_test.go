package limiter

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// redisForTest connects to a local Redis, or SKIPS the test if none is running,
// so `make test` still passes on a machine without Redis. Start one with:
//
//	docker compose up -d redis      (or: redis-server)
func redisForTest(t *testing.T) *redis.Client {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skipf("no Redis at localhost:6379 (%v) — skipping integration test", err)
	}
	return rdb
}

func TestFixedWindowAllow(t *testing.T) {
	rdb := redisForTest(t)
	ctx := context.Background()

	// Unique key per run so leftover state can't leak between runs.
	key := "test:fw:" + t.Name() + ":" + time.Now().Format("150405.000000")

	// Worked case (the template): limit of 3, so the first three unit requests
	// in one window are admitted. What you assert AFTER that is the real test.
	fw := NewFixedWindow(rdb, 3, time.Minute)
	for i := 1; i <= 3; i++ {
		dec, err := fw.Allow(ctx, key, 1)
		if err != nil {
			t.Fatalf("request %d: unexpected error: %v", i, err)
		}
		if !dec.Allowed {
			t.Errorf("request %d: allowed = false, want true (still under limit)", i)
		}
	}

	// TODO — add the cases that make this a spec, not a smoke test:
	//   - the 4th request in the SAME window is DENIED (over limit)
	//   - Remaining counts down 2 -> 1 -> 0 across the first three
	//   - a weighted cost (e.g. cost = 2) consumes two units in one call
	//   - WINDOW ROLLOVER admits again. Note the snag: Allow calls time.Now()
	//     internally, so you can't fast-forward like Bucket.Allow let you.
	//     Two ways to handle it, and the choice is the lesson:
	//       (a) use a tiny window (e.g. time.Second) and actually sleep past it;
	//       (b) refactor Allow to take `now time.Time` so the window is
	//           injectable and the test stays instant and deterministic.
	//     Which is better — and what does needing (b) reveal about the existing
	//     TokenBucket/SlidingWindow, which also call time.Now() inside?
}
