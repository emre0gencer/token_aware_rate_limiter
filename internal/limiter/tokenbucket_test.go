package limiter

import (
	"context"
	"math"
	"testing"
	"time"
)

// bucketTokens reads the raw 'tokens' field of the bucket hash, the counterpart
// of windowKeys for inspecting state the Decision doesn't show.
func bucketTokens(t *testing.T, ctx context.Context, key string) float64 {
	t.Helper()
	rdb := redisForTest(t)
	s, err := rdb.HGet(ctx, key, "tokens").Float64()
	if err != nil {
		t.Fatalf("HGET %q tokens: %v", key, err)
	}
	return s
}

// approx compares within eps. TokenBucket.Allow reads time.Now() internally,
// so tokens refill between any two calls — Remaining can never be compared
// with ==. (Reconcile has no clock, so its effects CAN be asserted exactly.)
func approx(got, want, eps float64) bool {
	return math.Abs(got-want) <= eps
}

// TestTokenBucketAllow pins the core spec: a cold bucket starts FULL (the
// ADR-005 cold-start trade-off), a weighted cost debits in one call, a request
// larger than what's left is denied WITHOUT spending, and the denial's ResetAt
// reflects how long the refill needs to cover the shortfall.
func TestTokenBucketAllow(t *testing.T) {
	rdb := redisForTest(t)
	ctx := context.Background()
	key := newKey(t)

	const capacity, refill = 100, 10 // 10 tokens/sec
	tb := NewTokenBucket(rdb, capacity, refill, time.Minute)

	// Cold start: full bucket admits a weighted cost, Remaining reflects the
	// debit. Refill between here and the assert can only ADD tokens, so the
	// tolerance is one-sided in practice; 0.5 is generous.
	dec, err := tb.Allow(ctx, key, 30)
	if err != nil {
		t.Fatalf("first Allow: unexpected error: %v", err)
	}
	if !dec.Allowed {
		t.Errorf("first Allow: Allowed = false, want true (cold bucket starts full)")
	}
	if dec.Limit != capacity {
		t.Errorf("first Allow: Limit = %v, want %v", dec.Limit, float64(capacity))
	}
	if !approx(dec.Remaining, 70, 0.5) {
		t.Errorf("first Allow: Remaining = %v, want ~70", dec.Remaining)
	}

	// 80 > ~70 left: denied, and the tokens are NOT spent (unlike FixedWindow,
	// which deliberately counts denied attempts — compare TestFixedWindowAllow).
	before := time.Now()
	dec, err = tb.Allow(ctx, key, 80)
	if err != nil {
		t.Fatalf("over-budget Allow: unexpected error: %v", err)
	}
	if dec.Allowed {
		t.Errorf("over-budget Allow: Allowed = true, want false")
	}
	if !approx(dec.Remaining, 70, 0.5) {
		t.Errorf("over-budget Allow: Remaining = %v, want ~70 (denial must not spend)", dec.Remaining)
	}

	// Shortfall is ~10 tokens at 10/sec => ResetAt ~1s out.
	wait := dec.ResetAt.Sub(before)
	if wait < 500*time.Millisecond || wait > 1500*time.Millisecond {
		t.Errorf("ResetAt = %v away, want ~1s (10-token shortfall at 10/sec)", wait)
	}
}

// TestTokenBucketCostOverCapacity: a request bigger than the bucket can EVER
// hold is denied even when the bucket is brimming, and costs nothing. (Nuance:
// reset_ms still reports a finite wait, though no wait can ever satisfy the
// request — the refill caps at capacity. Documented behavior, not a bug.)
func TestTokenBucketCostOverCapacity(t *testing.T) {
	rdb := redisForTest(t)
	ctx := context.Background()
	key := newKey(t)

	tb := NewTokenBucket(rdb, 50, 10, time.Minute)

	dec, err := tb.Allow(ctx, key, 60)
	if err != nil {
		t.Fatalf("Allow(60): unexpected error: %v", err)
	}
	if dec.Allowed {
		t.Errorf("Allow(60) on capacity 50: Allowed = true, want false")
	}
	if !approx(dec.Remaining, 50, 0.5) {
		t.Errorf("Remaining = %v, want ~50 (impossible request must not spend)", dec.Remaining)
	}

	// The full budget is still there: an exactly-capacity request goes through.
	dec, err = tb.Allow(ctx, key, 50)
	if err != nil {
		t.Fatalf("Allow(50): unexpected error: %v", err)
	}
	if !dec.Allowed {
		t.Errorf("Allow(50): Allowed = false, want true (prior denial spent nothing)")
	}
}

// TestTokenBucketRefill exhausts the bucket, waits real time, and sees budget
// come back — capped at capacity. Allow() reads time.Now() internally (no
// injectable clock, same lesson as TestFixedWindowRollover), so we must sleep
// rather than fast-forward.
func TestTokenBucketRefill(t *testing.T) {
	rdb := redisForTest(t)
	ctx := context.Background()
	key := newKey(t)

	const capacity, refill = 5, 10 // refills fully in 500ms
	tb := NewTokenBucket(rdb, capacity, refill, time.Minute)

	// Drain it, then an immediate retry finds ~0 tokens: denied.
	if dec, err := tb.Allow(ctx, key, capacity); err != nil || !dec.Allowed {
		t.Fatalf("drain: dec=%+v err=%v, want Allowed", dec, err)
	}
	if dec, err := tb.Allow(ctx, key, capacity); err != nil || dec.Allowed {
		t.Fatalf("immediate retry: dec=%+v err=%v, want denied", dec, err)
	}

	// 700ms at 10/sec would refill 7 — the cap holds it at 5, so a capacity-sized
	// request is admitted and Remaining lands near 0. (If the cap were broken,
	// tokens would be 7 and Remaining ~2 — that's what the upper bound catches.)
	time.Sleep(700 * time.Millisecond)
	dec, err := tb.Allow(ctx, key, capacity)
	if err != nil {
		t.Fatalf("post-refill Allow: unexpected error: %v", err)
	}
	if !dec.Allowed {
		t.Errorf("post-refill Allow: Allowed = false, want true (bucket refilled)")
	}
	if dec.Remaining > 1 {
		t.Errorf("post-refill Remaining = %v, want <1 (refill must cap at capacity)", dec.Remaining)
	}
}

// TestTokenBucketFractional: fractional costs debit exactly and survive the
// Redis round-trip (the script stringifies tokens so precision isn't truncated
// to an integer on the way back — HINT 6's whole reason to exist). A negligible
// refill rate keeps clock drift out of the tolerance.
func TestTokenBucketFractional(t *testing.T) {
	rdb := redisForTest(t)
	ctx := context.Background()
	key := newKey(t)

	tb := NewTokenBucket(rdb, 10, 0.001, time.Minute)

	steps := []struct{ cost, wantRemain float64 }{
		{2.5, 7.5},
		{2.5, 5.0},
	}
	for i, s := range steps {
		dec, err := tb.Allow(ctx, key, s.cost)
		if err != nil {
			t.Fatalf("step %d (cost %v): unexpected error: %v", i+1, s.cost, err)
		}
		if !dec.Allowed {
			t.Errorf("step %d (cost %v): Allowed = false, want true", i+1, s.cost)
		}
		if !approx(dec.Remaining, s.wantRemain, 0.01) {
			t.Errorf("step %d (cost %v): Remaining = %v, want ~%v", i+1, s.cost, dec.Remaining, s.wantRemain)
		}
	}
}

// TestTokenBucketReconcile settles the optimistic debit (ADR-006): delta = 0 is
// a no-op, a positive delta (model ran longer than estimated) debits more, a
// negative delta refunds, and both clamp into [0, capacity]. The reconcile
// script never touches the clock, so — unlike Allow — every value here is
// asserted EXACTLY, via the raw hash field.
func TestTokenBucketReconcile(t *testing.T) {
	rdb := redisForTest(t)
	ctx := context.Background()
	key := newKey(t)

	// Negligible refill: the seed Allow computes elapsed=0 on a cold bucket, so
	// the stored value is exactly 70 and stays put between reconciles.
	tb := NewTokenBucket(rdb, 100, 0.001, time.Minute)
	if dec, err := tb.Allow(ctx, key, 30); err != nil || !dec.Allowed {
		t.Fatalf("seed Allow: dec=%+v err=%v, want Allowed", dec, err)
	}

	steps := []struct {
		name       string
		delta      float64
		wantTokens float64
	}{
		{"zero delta is a no-op", 0, 70},
		{"positive delta debits more", 20, 50},
		{"refund clamps at capacity", -500, 100},
		{"fractional delta keeps precision", 7.5, 92.5},
		{"over-debit clamps at zero", 500, 0},
	}
	for _, s := range steps {
		if err := tb.Reconcile(ctx, key, s.delta); err != nil {
			t.Fatalf("Reconcile(%v) [%s]: unexpected error: %v", s.delta, s.name, err)
		}
		if got := bucketTokens(t, ctx, key); got != s.wantTokens {
			t.Errorf("%s: tokens = %v, want %v", s.name, got, s.wantTokens)
		}
	}
}

// TestTokenBucketReconcileExpiredKey: settling against a bucket that TTL'd away
// must be a quiet no-op — and must NOT resurrect the key. (A dead bucket that
// reappeared with a partial balance would corrupt the next cold start.)
func TestTokenBucketReconcileExpiredKey(t *testing.T) {
	rdb := redisForTest(t)
	ctx := context.Background()
	key := newKey(t) // never written: stands in for an expired bucket

	tb := NewTokenBucket(rdb, 100, 10, time.Minute)
	if err := tb.Reconcile(ctx, key, 20); err != nil {
		t.Fatalf("Reconcile on missing key: unexpected error: %v", err)
	}
	if n, err := rdb.Exists(ctx, key).Result(); err != nil || n != 0 {
		t.Errorf("EXISTS = %d (err %v), want 0 — reconcile must not create the bucket", n, err)
	}
}

// TestTokenBucketArmsTTL confirms Allow arms an expiry so idle buckets
// self-clean (the PEXPIRE half of the script), mirroring TestFixedWindowArmsTTL.
func TestTokenBucketArmsTTL(t *testing.T) {
	rdb := redisForTest(t)
	ctx := context.Background()
	key := newKey(t)

	const ttl = time.Minute
	tb := NewTokenBucket(rdb, 100, 10, ttl)
	if _, err := tb.Allow(ctx, key, 1); err != nil {
		t.Fatalf("Allow: unexpected error: %v", err)
	}

	pttl, err := rdb.PTTL(ctx, key).Result()
	if err != nil {
		t.Fatalf("PTTL: %v", err)
	}
	if pttl <= 0 || pttl > ttl {
		t.Errorf("PTTL = %v, want in (0, %v] (a -1 here means the bucket never expires)", pttl, ttl)
	}
}
