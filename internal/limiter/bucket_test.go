package limiter

import (
	"testing"
	"time"
)

// TestBucketAllow is a table-driven test. Each case sets up a bucket, performs
// one Allow at a given time, and asserts the outcome. Because Allow takes `now`
// as a parameter, we advance time by adding Durations to a fixed base — no
// sleeps, fully deterministic.
func TestBucketAllow(t *testing.T) {
	base := time.Unix(0, 0) // a fixed, arbitrary "t = 0"

	tests := []struct {
		name        string
		capacity    float64
		refill      float64       // tokens/sec
		cost        float64       // weight of the request under test
		elapsed     time.Duration // time between NewBucket and the Allow call
		wantAllowed bool
		wantRemain  float64 // tokens expected AFTER the call
	}{
		{
			// TODO: add cases that pin down the rest of the behavior, e.g.
			//   - a cost larger than capacity is rejected, tokens unchanged
			//   - a request that exactly empties the bucket (cost == tokens) is allowed
			//   - after draining, waiting long enough refills enough to allow again
			//   - refill is capped at capacity (waiting a very long time doesn't overflow)
			//   - a fractional/float cost debits correctly

			name:        "full bucket admits weighted cost",
			capacity:    100,
			refill:      10,
			cost:        30,
			elapsed:     0,
			wantAllowed: true,
			wantRemain:  70,
		},
		{
			name:        "cost over capacity is rejected, tokens unchanged",
			capacity:    100,
			refill:      10,
			cost:        150,
			elapsed:     0,
			wantAllowed: false,
			wantRemain:  100,
		},
		{
			name:        "cost equal to tokens exactly empties the bucket",
			capacity:    100,
			refill:      10,
			cost:        100,
			elapsed:     0,
			wantAllowed: true,
			wantRemain:  0,
		},
		{
			name:        "refill is capped at capacity",
			capacity:    100,
			refill:      10,
			cost:        30,
			elapsed:     time.Hour,
			wantAllowed: true,
			wantRemain:  70,
		},
		{
			name:        "fractional cost debits correctly",
			capacity:    100,
			refill:      10,
			cost:        12.5,
			elapsed:     0,
			wantAllowed: true,
			wantRemain:  87.5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := NewBucket(tt.capacity, tt.refill, base)
			allowed, remaining := b.Allow(base.Add(tt.elapsed), tt.cost)
			if allowed != tt.wantAllowed {
				t.Errorf("allowed = %v, want %v", allowed, tt.wantAllowed)
			}
			if remaining != tt.wantRemain {
				t.Errorf("remaining = %v, want %v", remaining, tt.wantRemain)
			}
		})
	}
}
