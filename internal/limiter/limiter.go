// Package limiter defines the rate-limiting contract and Redis-backed
// implementations. The Limiter interface is deliberately storage-agnostic so
// algorithms can be unit-tested against a fake and swapped per-rule by config.
package limiter

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

// Decision is the result of a single Allow check.
type Decision struct {
	Allowed   bool
	Limit     float64   // ceiling for the matched rule
	Remaining float64   // budget left after this request
	ResetAt   time.Time // when budget refills / window rolls over
}

// Limiter checks and settles weighted cost against a per-client budget.
//
//	cost  — weight of THIS request: 1 for request-count rules, estimated token
//	        count for token rules, estimated dollars for dollar rules.
//	delta — actual minus estimate, applied during reconciliation.
type Limiter interface {
	Allow(ctx context.Context, key string, cost float64) (Decision, error)
	Reconcile(ctx context.Context, key string, delta float64) error
}

// parseDecision converts a Lua array result {allowed, remaining, reset_ms}.
func parseDecision(v any, limit float64, now time.Time) (Decision, error) {
	arr, ok := v.([]any)
	if !ok || len(arr) < 3 {
		return Decision{}, fmt.Errorf("limiter: unexpected script result %#v", v)
	}
	resetMs := toInt(arr[2])
	return Decision{
		Allowed:   toInt(arr[0]) == 1,
		Limit:     limit,
		Remaining: toFloat(arr[1]),
		ResetAt:   now.Add(time.Duration(resetMs) * time.Millisecond),
	}, nil
}

// Redis returns Lua numbers as int64 and Lua strings as string; our scripts
// stringify fractional values to preserve precision, so handle both.
func toInt(v any) int64 {
	switch t := v.(type) {
	case int64:
		return t
	case string:
		n, _ := strconv.ParseInt(t, 10, 64)
		return n
	default:
		return 0
	}
}

func toFloat(v any) float64 {
	switch t := v.(type) {
	case int64:
		return float64(t)
	case string:
		f, _ := strconv.ParseFloat(t, 64)
		return f
	default:
		return 0
	}
}
