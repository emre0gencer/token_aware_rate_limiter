// Package gateway wires the middleware chain into a single http.Handler:
//
//	identify → estimate → limit (most-restrictive) → proxy → reconcile → observe
//
// It is the place where the cost-aware niche and the distributed-limit
// decisions meet. See ARCHITECTURE.md §3.4.
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"time"

	"github.com/egencer/distributed-rate-limiter-gateway/internal/cost"
	"github.com/egencer/distributed-rate-limiter-gateway/internal/identify"
	"github.com/egencer/distributed-rate-limiter-gateway/internal/limiter"
	"github.com/egencer/distributed-rate-limiter-gateway/internal/metrics"
	"github.com/egencer/distributed-rate-limiter-gateway/internal/rules"
)

// Gateway is the assembled request handler.
type Gateway struct {
	engine           *rules.Engine
	limiters         map[string]limiter.Limiter // keyed by rule ID
	prices           cost.PriceTable
	proxy            *httputil.ReverseProxy
	defaultMaxTokens int
}

// New builds a Gateway. limiters must contain one entry per rule ID.
func New(engine *rules.Engine, limiters map[string]limiter.Limiter, prices cost.PriceTable, upstreamBase string, defaultMaxTokens int) (*Gateway, error) {
	target, err := url.Parse(upstreamBase)
	if err != nil {
		return nil, err
	}
	g := &Gateway{
		engine:           engine,
		limiters:         limiters,
		prices:           prices,
		defaultMaxTokens: defaultMaxTokens,
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	// preserve upstream host (TLS SNI / vhost routing)
	origDirector := proxy.Director
	proxy.Director = func(r *http.Request) {
		origDirector(r)
		r.Host = target.Host
	}
	// reconcile estimate vs. actual once the provider has answered
	proxy.ModifyResponse = g.reconcile
	g.proxy = proxy
	return g, nil
}

// planKey is the context key for the per-request reconciliation plan.
type planKey struct{}

type reconcileItem struct {
	lim   limiter.Limiter
	key   string
	unit  rules.Unit
	model string
}

type reconcilePlan struct {
	estimate cost.Estimate
	items    []reconcileItem
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Buffer the body so we can both estimate cost and forward it.
	body, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(body))

	id := identify.Identify(r)
	est := cost.EstimateFromBody(body, g.defaultMaxTokens)
	matches := g.engine.Match(id, est.Model)

	var best *limiter.Decision // most restrictive, for response headers
	plan := &reconcilePlan{estimate: est}

	for _, m := range matches {
		lim, ok := g.limiters[m.Rule.ID]
		if !ok {
			continue // rule without a built limiter; skip defensively
		}
		weight := weightFor(m.Rule.Unit, est, g.prices)

		dec, err := lim.Allow(r.Context(), m.Key, weight)
		if err != nil {
			metrics.StoreErrors.Add(1)
			if m.Rule.FailOpen {
				continue // ADR-008: this rule favors availability
			}
			// fail-closed: refuse rather than risk unbounded spend
			writeJSON(w, http.StatusServiceUnavailable, errBody{
				Error:   "rate_limiter_unavailable",
				Message: "limiter backend unavailable and rule is fail-closed",
			})
			return
		}
		if !dec.Allowed {
			metrics.Denied.Add(1)
			writeRateLimited(w, dec)
			return
		}
		if best == nil || dec.Remaining < best.Remaining {
			d := dec
			best = &d
		}
		if m.Rule.Unit == rules.UnitTokens || m.Rule.Unit == rules.UnitDollars {
			plan.items = append(plan.items, reconcileItem{lim: lim, key: m.Key, unit: m.Rule.Unit, model: est.Model})
		}
	}

	metrics.Allowed.Add(1)
	metrics.TokensMetered.Add(int64(est.Total()))
	if d, ok := dollarEstimate(g.prices, est); ok {
		metrics.DollarsMetered.Add(d)
	}
	if best != nil {
		setRateHeaders(w.Header(), *best)
	}

	r = r.WithContext(context.WithValue(r.Context(), planKey{}, plan))
	g.proxy.ServeHTTP(w, r)
}

// reconcile reads actual usage from the upstream response and settles each
// token/dollar rule's optimistic debit (ADR-006).
func (g *Gateway) reconcile(resp *http.Response) error {
	plan, ok := resp.Request.Context().Value(planKey{}).(*reconcilePlan)
	if !ok || len(plan.items) == 0 {
		return nil
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(raw)) // restore for the client
	resp.ContentLength = int64(len(raw))
	resp.Header.Set("Content-Length", strconv.Itoa(len(raw)))

	usage, ok := cost.UsageFromResponse(raw)
	if !ok {
		return nil // streaming / no usage object: nothing to reconcile
	}

	ctx := resp.Request.Context()
	for _, it := range plan.items {
		var delta float64
		switch it.unit {
		case rules.UnitTokens:
			delta = cost.TokenDelta(plan.estimate, usage)
		case rules.UnitDollars:
			actual := g.prices.Dollars(it.model, usage.PromptTokens, usage.CompletionTokens)
			delta = actual - g.prices.EstimateDollars(plan.estimate)
		}
		if err := it.lim.Reconcile(ctx, it.key, delta); err == nil {
			metrics.Reconciles.Add(1)
		}
	}
	resp.Header.Set("X-Cost-Tokens-Actual", strconv.Itoa(usage.Total()))
	return nil
}

func weightFor(unit rules.Unit, est cost.Estimate, prices cost.PriceTable) float64 {
	switch unit {
	case rules.UnitTokens:
		return float64(est.Total())
	case rules.UnitDollars:
		return prices.EstimateDollars(est)
	default: // UnitRequests
		return 1
	}
}

func dollarEstimate(prices cost.PriceTable, est cost.Estimate) (float64, bool) {
	d := prices.EstimateDollars(est)
	return d, d > 0
}

// --- response helpers ---

type errBody struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

func setRateHeaders(h http.Header, d limiter.Decision) {
	h.Set("X-RateLimit-Limit", strconv.FormatFloat(d.Limit, 'f', -1, 64))
	h.Set("X-RateLimit-Remaining", strconv.FormatFloat(math.Max(0, d.Remaining), 'f', -1, 64))
	h.Set("X-RateLimit-Reset", strconv.FormatInt(d.ResetAt.Unix(), 10))
}

func writeRateLimited(w http.ResponseWriter, d limiter.Decision) {
	retry := int(math.Ceil(time.Until(d.ResetAt).Seconds()))
	if retry < 1 {
		retry = 1
	}
	setRateHeaders(w.Header(), d)
	w.Header().Set("Retry-After", strconv.Itoa(retry))
	writeJSON(w, http.StatusTooManyRequests, errBody{
		Error:   "rate_limit_exceeded",
		Message: "budget exhausted; retry after " + strconv.Itoa(retry) + "s",
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
