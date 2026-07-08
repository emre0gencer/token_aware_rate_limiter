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

// newKey returns a Redis key unique to this test run. Window keys self-expire
// (TTL = 2*window), so unique prefixes keep runs and tests from colliding
// without any explicit cleanup.
func newKey(t *testing.T) string {
	t.Helper()
	return "test:fw:" + t.Name() + ":" + time.Now().Format("150405.000000000")
}

// windowKeys returns the per-window keys Allow created under `key`
// ("<key>:<windowIndex>"). Used to inspect the raw counter and its TTL.
func windowKeys(t *testing.T, rdb *redis.Client, ctx context.Context, key string) []string {
	t.Helper()
	keys, err := rdb.Keys(ctx, key+":*").Result()
	if err != nil {
		t.Fatalf("Keys(%q): %v", key+":*", err)
	}
	return keys
}

// TestFixedWindowAllow pins the core admission spec: units under the limit are
// admitted with Remaining counting down to 0, the first over-limit request is
// denied (Remaining floored, not negative), ResetAt points at the next window,
// and — the deliberate design choice — a denied request still consumed budget.
func TestFixedWindowAllow(t *testing.T) {
	rdb := redisForTest(t)
	ctx := context.Background()
	key := newKey(t)

	const limit = 3
	fw := NewFixedWindow(rdb, limit, time.Minute)

	// The first `limit` unit requests are admitted, Remaining counts 2 -> 1 -> 0,
	// and every Decision reports the rule's Limit.
	for i, wantRemain := range []float64{2, 1, 0} {
		dec, err := fw.Allow(ctx, key, 1)
		if err != nil {
			t.Fatalf("request %d: unexpected error: %v", i+1, err)
		}
		if !dec.Allowed {
			t.Errorf("request %d: Allowed = false, want true (still under limit)", i+1)
		}
		if dec.Remaining != wantRemain {
			t.Errorf("request %d: Remaining = %v, want %v", i+1, dec.Remaining, wantRemain)
		}
		if dec.Limit != limit {
			t.Errorf("request %d: Limit = %v, want %v", i+1, dec.Limit, float64(limit))
		}
	}

	// The next request in the SAME window is over the limit: denied, and Remaining
	// floors at 0 rather than going negative.
	before := time.Now()
	dec, err := fw.Allow(ctx, key, 1)
	if err != nil {
		t.Fatalf("over-limit request: unexpected error: %v", err)
	}
	if dec.Allowed {
		t.Errorf("over-limit request: Allowed = true, want false")
	}
	if dec.Remaining != 0 {
		t.Errorf("over-limit request: Remaining = %v, want 0", dec.Remaining)
	}

	// ResetAt is the start of the NEXT window: strictly in the future and aligned
	// to a window boundary.
	if !dec.ResetAt.After(before) {
		t.Errorf("ResetAt = %v, want a time after %v", dec.ResetAt, before)
	}
	if off := dec.ResetAt.UnixNano() % time.Minute.Nanoseconds(); off != 0 {
		t.Errorf("ResetAt = %v is not aligned to a %v boundary (off by %dns)", dec.ResetAt, time.Minute, off)
	}

	// The denial still incremented the counter: we limit attempts, not admissions
	// (no DECRBY-back). Three admits + one denial => raw counter of 4.
	keys := windowKeys(t, rdb, ctx, key)
	if len(keys) != 1 {
		t.Fatalf("found %d window keys (%v), want exactly 1", len(keys), keys)
	}
	if n, err := rdb.Get(ctx, keys[0]).Int64(); err != nil || n != 4 {
		t.Errorf("raw counter = %d (err %v), want 4 (denials stay counted)", n, err)
	}
}

// TestFixedWindowWeightedCost checks that a single call with cost > 1 debits
// that many units at once, and that overshooting the limit is denied.
func TestFixedWindowWeightedCost(t *testing.T) {
	rdb := redisForTest(t)
	ctx := context.Background()
	key := newKey(t)

	const limit = 5
	fw := NewFixedWindow(rdb, limit, time.Minute)

	steps := []struct {
		cost        float64
		wantAllowed bool
		wantRemain  float64
	}{
		{2, true, 3},  // total 2
		{2, true, 1},  // total 4
		{2, false, 0}, // total would be 6 > 5: denied, Remaining floored
	}
	for i, s := range steps {
		dec, err := fw.Allow(ctx, key, s.cost)
		if err != nil {
			t.Fatalf("step %d (cost %v): unexpected error: %v", i+1, s.cost, err)
		}
		if dec.Allowed != s.wantAllowed {
			t.Errorf("step %d (cost %v): Allowed = %v, want %v", i+1, s.cost, dec.Allowed, s.wantAllowed)
		}
		if dec.Remaining != s.wantRemain {
			t.Errorf("step %d (cost %v): Remaining = %v, want %v", i+1, s.cost, dec.Remaining, s.wantRemain)
		}
	}
}

// TestFixedWindowRollover verifies that budget resets when the window rolls over.
// Allow() reads time.Now() internally — there is no injectable clock like
// Bucket.Allow's `now` parameter — so we can't fast-forward; we wait out a real
// window. (That we must sleep is the lesson the TODO posed: FixedWindow,
// SlidingWindow, and TokenBucket all call time.Now() inside, so all three are
// awkward to unit-test; threading a `now time.Time` through Allow, as Bucket
// does, would make this instant and deterministic instead.)
func TestFixedWindowRollover(t *testing.T) {
	rdb := redisForTest(t)
	ctx := context.Background()
	key := newKey(t)

	const window = 500 * time.Millisecond
	fw := NewFixedWindow(rdb, 1, window)

	// Sleep to just past a window boundary first, so the two quick requests below
	// are guaranteed to land in the SAME window (no accidental mid-test rollover).
	nextBoundary := time.Unix(0, (time.Now().UnixMilli()/window.Milliseconds()+1)*window.Nanoseconds())
	time.Sleep(time.Until(nextBoundary) + 30*time.Millisecond)

	// Exhaust the window (limit 1): first admitted, second denied.
	if dec, err := fw.Allow(ctx, key, 1); err != nil || !dec.Allowed {
		t.Fatalf("first request: dec=%+v err=%v, want Allowed", dec, err)
	}
	if dec, err := fw.Allow(ctx, key, 1); err != nil || dec.Allowed {
		t.Fatalf("second request (same window): dec=%+v err=%v, want denied", dec, err)
	}

	// Cross into the next window (we're ~30ms in, so one window of sleep lands us
	// ~30ms into the next). New window index => new key => fresh count.
	time.Sleep(window)

	dec, err := fw.Allow(ctx, key, 1)
	if err != nil {
		t.Fatalf("post-rollover request: unexpected error: %v", err)
	}
	if !dec.Allowed {
		t.Errorf("post-rollover request: Allowed = false, want true (fresh window)")
	}
	if dec.Remaining != 0 {
		t.Errorf("post-rollover request: Remaining = %v, want 0 (limit 1, one used)", dec.Remaining)
	}
}

// TestFixedWindowArmsTTL confirms Allow arms an expiry on the window key so it
// self-cleans after rollover (the PEXPIRE half of the pipeline).
func TestFixedWindowArmsTTL(t *testing.T) {
	rdb := redisForTest(t)
	ctx := context.Background()
	key := newKey(t)

	const window = time.Minute
	fw := NewFixedWindow(rdb, 3, window)
	if _, err := fw.Allow(ctx, key, 1); err != nil {
		t.Fatalf("Allow: unexpected error: %v", err)
	}

	keys := windowKeys(t, rdb, ctx, key)
	if len(keys) != 1 {
		t.Fatalf("found %d window keys (%v), want exactly 1", len(keys), keys)
	}
	pttl, err := rdb.PTTL(ctx, keys[0]).Result()
	if err != nil {
		t.Fatalf("PTTL: %v", err)
	}
	// NewFixedWindow sets ttl = 2*window. PTTL must be positive (a value of -1
	// would mean "no expiry" — the key would leak) and no larger than that ttl.
	if pttl <= 0 || pttl > 2*window {
		t.Errorf("PTTL = %v, want in (0, %v]", pttl, 2*window)
	}
}

// TestFixedWindowReconcile covers the implemented Reconcile: delta == 0 is a
// no-op, and a nonzero delta nudges the CURRENT window's counter up (actual >
// estimate) or down (a refund), visible to the next Allow. Uses a minute-long
// window so Allow and Reconcile share one window index throughout.
func TestFixedWindowReconcile(t *testing.T) {
	rdb := redisForTest(t)
	ctx := context.Background()
	key := newKey(t)

	const limit = 10
	fw := NewFixedWindow(rdb, limit, time.Minute)

	// Seed the window with one unit (the up-front estimate). counter = 1.
	if dec, err := fw.Allow(ctx, key, 1); err != nil || dec.Remaining != 9 {
		t.Fatalf("seed Allow: dec=%+v err=%v, want Remaining 9", dec, err)
	}

	// delta == 0 must be a no-op: no error, counter untouched.
	if err := fw.Reconcile(ctx, key, 0); err != nil {
		t.Fatalf("Reconcile(0): unexpected error: %v", err)
	}

	// Positive delta (actual exceeded estimate) debits more. counter 1 +4 = 5,
	// then the next Allow(1) => 6 => Remaining 4.
	if err := fw.Reconcile(ctx, key, 4); err != nil {
		t.Fatalf("Reconcile(+4): unexpected error: %v", err)
	}
	if dec, err := fw.Allow(ctx, key, 1); err != nil || dec.Remaining != 4 {
		t.Fatalf("after +4: dec=%+v err=%v, want Remaining 4", dec, err)
	}

	// Negative delta (a refund) hands budget back. counter 6 -3 = 3, then
	// Allow(1) => 4 => Remaining 6.
	if err := fw.Reconcile(ctx, key, -3); err != nil {
		t.Fatalf("Reconcile(-3): unexpected error: %v", err)
	}
	if dec, err := fw.Allow(ctx, key, 1); err != nil || dec.Remaining != 6 {
		t.Fatalf("after -3: dec=%+v err=%v, want Remaining 6", dec, err)
	}
}
