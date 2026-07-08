package limiter

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// FixedWindow is the simplest Redis-backed limiter (build-order step 3): a
// per-window counter incremented with INCRBY and auto-reset by a TTL. It moves
// budget state OFF the process (unlike the in-memory Bucket) and leans on INCR
// being atomic server-side, so the read-modify-write race is gone without any
// Lua. Intended for unit:requests rules, where cost is a whole number.
//
// Trade-off vs. SlidingWindow: a fixed window allows a 2x burst across the
// boundary (full limit at the end of one window + full limit at the start of
// the next). That imprecision is exactly what SlidingWindow removes — build the
// blunt version first to feel the problem.
type FixedWindow struct {
	rdb    redis.Cmdable
	limit  float64
	window time.Duration
	ttl    time.Duration
}

func NewFixedWindow(rdb redis.Cmdable, limit float64, window time.Duration) *FixedWindow {
	// TODO: also set ttl a bit longer than one window (e.g. 2*window) so a
	// just-written counter survives until its window is definitely over.
	return &FixedWindow{rdb: rdb, limit: limit, window: window}
}

// Allow increments this client's counter for the CURRENT window and admits the
// request iff the new total stays within limit.
func (f *FixedWindow) Allow(ctx context.Context, key string, cost float64) (Decision, error) {
	now := time.Now()
	_ = now

	// HINT 1 — window-in-the-key. Turn `now` into an integer window index using
	// the window length, and append it to `key`. Because each window gets its
	// own key, the previous window's counter just TTLs away — no manual reset,
	// and you sidestep the "only EXPIRE on the first increment" bug (see bottom).
	//     idx  := now.UnixMilli() / f.window.Milliseconds()
	//     wkey := fmt.Sprintf("%s:%d", key, idx)

	// HINT 2 — the atomic increment. IncrBy returns the NEW total after adding.
	// That one value is both your read and your write — no GET first, no race.
	//     total, err := f.rdb.IncrBy(ctx, wkey, int64(cost)).Result()
	// IncrBy is integer; cost is float64 only for interface uniformity. Whole
	// costs only here. (Fractional token/dollar rules use the token bucket, or
	// you'd reach for IncrByFloat / INCRBYFLOAT.)

	// HINT 3 — arm the TTL so the window auto-expires. With window-in-key it is
	// safe to (re)set on every call; the key is unique to this window.
	//     f.rdb.PExpire(ctx, wkey, f.ttl)

	// HINT 4 — decide and fill the Decision:
	//     Allowed   = total <= limit
	//     Limit     = f.limit
	//     Remaining = max(0, f.limit - float64(total))
	//     ResetAt   = start of the NEXT window (now truncated to window + window)
	// Design question: on a DENY you've still incremented. Do you DECRBY it back
	// so denials don't consume budget? For a pure counter that's the difference
	// between limiting "attempts" and "admissions" — decide deliberately.

	// HINT 5 — errors. If a Redis call errors, return (Decision{}, err) and let
	// the gateway apply the fail-open/closed policy (ADR-008); don't decide here.

	// HINT 6 — latency (optional). INCRBY then PEXPIRE is two round-trips. A
	// pipeline (f.rdb.Pipeline()) sends both in one, matching §5.3's "one
	// round-trip per check." Correctness doesn't need it; latency likes it.

	return Decision{}, nil // TODO
}

// Reconcile nudges the current window's counter by delta (actual - estimate).
func (f *FixedWindow) Reconcile(ctx context.Context, key string, delta float64) error {
	// TODO: derive the same window key, then a single IncrBy of delta. Return
	// nil for delta == 0. Best-effort — the counter heals on window rollover.
	return nil
}

// --- The "EXPIRE only on first increment" bug (why window-in-key avoids it) ---
// The textbook single-key fixed window uses ONE key reset by EXPIRE. The trap:
// calling EXPIRE on every INCR keeps pushing the TTL out, so the window never
// rolls over. The fix there is to EXPIRE only when INCR returns exactly `cost`
// (the key was just created). Window-in-key dodges it entirely: a new window is
// a new key, so there is nothing to reset.
