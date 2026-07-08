package limiter

import "time"

// Bucket is a single, in-memory, weighted token bucket — STEP 2 of the build
// order. Exactly one client, one node, no Redis. We isolate the algorithm here
// so it can be reasoned about and table-tested; locking (sync.Mutex), keying by
// client (a map), and the Redis/Lua version all come in later steps. The
// Redis-backed TokenBucket in tokenbucket.go is the eventual "real" one — this
// is the teaching model that proves the math.
//
// NOTE: not safe for concurrent use. That's deliberate for now.
type Bucket struct {
	capacity float64   // C: burst ceiling
	refill   float64   // R: tokens per second (sustained rate)
	tokens   float64   // tokens currently available
	last     time.Time // when we last refilled
}

// NewBucket returns a bucket that starts FULL (the cold-start burst trade-off
// from ADR-005: an idle client can immediately spend up to C).
func NewBucket(capacity, refillPerSec float64, now time.Time) *Bucket {
	// TODO: return &Bucket{...} with tokens starting at capacity and last = now.
	return &Bucket{capacity: capacity, refill: refillPerSec, tokens: capacity, last: now}
}

// Allow lazily refills the bucket to time `now`, then tries to debit `cost`.
// It returns whether the request is allowed and the tokens remaining after.
//
// `now` is a parameter (not time.Now()) on purpose: it makes tests deterministic
// — you advance the clock by exact amounts instead of sleeping.
func (b *Bucket) Allow(now time.Time, cost float64) (allowed bool, remaining float64) {
	elapsed := now.Sub(b.last).Seconds()
	b.tokens = min(b.capacity, b.tokens+elapsed*b.refill)
	b.last = now
	if b.tokens >= cost {
		b.tokens -= cost
		allowed = true
	}
	return allowed, b.tokens
}
