// Command gateway is the entry point for the rate-limiting LLM proxy.
//
// STEP 5 of the build order: the bare step-1 reverse proxy is replaced by the
// full middleware chain. main.go's job is now pure wiring — load config,
// connect to Redis, build one limiter per rule, and hand everything to
// gateway.New (which owns identify → estimate → limit → proxy → reconcile →
// observe). All the interesting logic lives in the internal packages; this file
// just assembles them.
package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/egencer/distributed-rate-limiter-gateway/internal/config"
	"github.com/egencer/distributed-rate-limiter-gateway/internal/gateway"
	"github.com/egencer/distributed-rate-limiter-gateway/internal/limiter"
	"github.com/egencer/distributed-rate-limiter-gateway/internal/metrics"
	"github.com/egencer/distributed-rate-limiter-gateway/internal/rules"
	"github.com/egencer/distributed-rate-limiter-gateway/internal/store"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to the YAML config file")
	flag.Parse()

	// Config carries server/redis/upstream settings, the price table, and the
	// rule set. Load validates it (upstream.base_url is required) and fills in
	// defaults for anything omitted.
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// One shared Redis client is the single budget truth for every stateless
	// replica. Per-op timeouts come from config so a slow shard trips the same
	// degradation path as a down one (ADR-008).
	rdb, err := store.NewRedis(cfg.Redis.Addr, cfg.Redis.Timeout)
	if err != nil {
		log.Fatalf("redis %s: %v", cfg.Redis.Addr, err)
	}

	// Build one Limiter per rule ID, switching on the algorithm the rule asks
	// for. gateway.New requires exactly one limiter per rule, so an unknown
	// algorithm is a config error we fail fast on rather than silently skip.
	limiters := make(map[string]limiter.Limiter, len(cfg.Rules))
	for _, r := range cfg.Rules {
		switch r.Algorithm {
		case rules.AlgoTokenBucket:
			// ttl 0 → NewTokenBucket defaults to 1h; idle buckets auto-expire.
			limiters[r.ID] = limiter.NewTokenBucket(rdb, r.Capacity(), r.RefillPerSec(), 0)
		case rules.AlgoSlidingWindow:
			// ttl 0 → NewSlidingWindow defaults to 2×window.
			limiters[r.ID] = limiter.NewSlidingWindow(rdb, r.Limit, r.Window, 0)
		default:
			log.Fatalf("rule %q: unknown algorithm %q", r.ID, r.Algorithm)
		}
	}

	// The engine matches each request to its applicable rules in memory; only
	// the counters those rules debit live in Redis (ADR-011).
	engine := rules.NewEngine(cfg.Rules)

	gw, err := gateway.New(engine, limiters, cfg.Pricing, cfg.Upstream.BaseURL, cfg.Upstream.DefaultMaxTokens)
	if err != nil {
		log.Fatalf("gateway: %v", err)
	}

	// The gateway handles everything at /; metrics ride alongside at /debug/vars.
	mux := http.NewServeMux()
	mux.Handle("/debug/vars", metrics.Handler())
	mux.Handle("/", gw)

	log.Printf("gateway listening on %s, proxying to %s (%d rules)",
		cfg.Server.Addr, cfg.Upstream.BaseURL, len(cfg.Rules))
	log.Fatal(http.ListenAndServe(cfg.Server.Addr, mux))
}
