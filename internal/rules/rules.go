// Package rules models limiting policies and matches them to a request.
// Rules live in memory and are evaluated on the hot path; only the per-client
// budget counters touch Redis (ADR-011).
package rules

import (
	"fmt"
	"time"

	"github.com/egencer/distributed-rate-limiter-gateway/internal/identify"
)

type Unit string

const (
	UnitRequests Unit = "requests" // weight 1 per request
	UnitTokens   Unit = "tokens"   // weight = estimated token count
	UnitDollars  Unit = "dollars"  // weight = estimated dollar cost
)

type Algorithm string

const (
	AlgoTokenBucket   Algorithm = "token_bucket"
	AlgoSlidingWindow Algorithm = "sliding_window"
)

// Rule is one limiting policy. Many rules can apply to a single request.
type Rule struct {
	ID        string
	Scope     string // api_key | user | ip | global
	Algorithm Algorithm
	Unit      Unit
	Limit     float64       // budget per window (tokens, dollars, or requests)
	Burst     float64       // token-bucket capacity; defaults to Limit if 0
	Window    time.Duration // refill period / window length
	FailOpen  bool          // ADR-008: default false (fail-closed)
	Models    []string      // optional model filter; empty => all
}

// Capacity returns the token-bucket burst ceiling.
func (r Rule) Capacity() float64 {
	if r.Burst > 0 {
		return r.Burst
	}
	return r.Limit
}

// RefillPerSec is the sustained token-bucket rate.
func (r Rule) RefillPerSec() float64 {
	if r.Window <= 0 {
		return r.Limit
	}
	return r.Limit / r.Window.Seconds()
}

func (r Rule) appliesToModel(model string) bool {
	if len(r.Models) == 0 {
		return true
	}
	for _, m := range r.Models {
		if m == model {
			return true
		}
	}
	return false
}

// Match is a rule paired with the concrete Redis key for this client.
type Match struct {
	Rule Rule
	Key  string // rl:{<client>}:<ruleID>  — hash tag co-locates a client's keys
}

// Engine holds the active rule set and matches requests against it.
type Engine struct {
	rules []Rule
}

func NewEngine(rs []Rule) *Engine { return &Engine{rules: rs} }

// Match returns every rule that applies to this identity + model, each with the
// Redis key built from the identifier the rule's scope selects.
func (e *Engine) Match(id identify.Identity, model string) []Match {
	var out []Match
	for _, r := range e.rules {
		if !r.appliesToModel(model) {
			continue
		}
		ident, ok := id.For(r.Scope)
		if !ok {
			continue // this request has no identifier for the rule's scope
		}
		out = append(out, Match{
			Rule: r,
			Key:  fmt.Sprintf("rl:{%s}:%s", ident, r.ID),
		})
	}
	return out
}
