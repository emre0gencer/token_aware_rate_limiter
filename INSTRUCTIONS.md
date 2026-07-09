# INSTRUCTIONS.md — The Build Guidebook (Steps 1–10)

This is the **single source of truth for how this project gets built**, step by
step. It exists for two readers:

1. **The learner (Emre)** — each step tells you the goal, *why* it matters, the
   exact files and functions to complete, and how to prove it works before
   moving on.
2. **Claude (or any AI assistant)** — when prompted about *anything* related to
   building, continuing, reviewing, or explaining a step of this project, refer
   to this file **first** and follow its contract below.

> Companion docs: [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) (the design,
> §-references below point here) and [`docs/DECISIONS.md`](docs/DECISIONS.md)
> (ADR-001 … ADR-011, the "why" for every choice). This file is the *how* and
> *in what order*.

---

## The working contract (read me first)

**This is a stepwise learning build.** The skeleton files already contain
`HINT`/`TODO` comments that carry the design. The rules:

- **Complete within the hints — don't redesign.** The learner fills in
  function bodies and Lua scripts guided by the inline HINTs. Assistants
  *review, explain, and scaffold*; they do not pre-write the algorithm bodies
  unless explicitly asked.
- **One step at a time.** Do not start step N+1 until step N's "Done when"
  checklist is green. Resist wiring everything early — each step is
  independently runnable.
- **Every step ends verified.** `go build ./...`, `go vet ./...`, and the
  step's tests must pass before a step counts as done.
- **Tests live next to the code** (Go convention): `foo.go` and `foo_test.go`
  in the same directory, same package.
- **Commit per step** with the established message style:
  `step N (incomplete): ...` while in progress, `step N: ...` when done.

### Test-harness conventions (established in steps 2–3; reuse everywhere)

- **Pure-algorithm tests** (no Redis) are table-driven with an injectable
  `now time.Time` — advance time by adding `Duration`s, never `time.Sleep`.
  See `internal/limiter/bucket_test.go`.
- **Redis-backed tests** hit a real Redis at `localhost:6379` and **skip — not
  fail — when none is running**, via these helpers in
  `internal/limiter/fixedwindow_test.go` (reuse them; don't duplicate):
  - `redisForTest(t)` — connect or `t.Skip`
  - `newKey(t)` — unique per-test key, collision-free across runs
- **The `time.Now()` caveat:** Redis-backed limiters read `time.Now()`
  *internally* (no injectable clock), so tests that need time to pass must use
  short real windows + `time.Sleep`. This is called out in
  `fixedwindow_test.go` and applies to steps 3, 4, and beyond.
- Start Redis with `docker compose up -d redis` (or `make redis`).

### Standard verification commands

```bash
make build    # go build -o bin/gateway ./cmd/gateway
make vet      # go vet ./...
make test     # go test ./...   (Redis-backed tests self-skip without Redis)
make redis    # docker compose up -d redis
make run      # go run ./cmd/gateway -config config.yaml
```

### Status at a glance (update this table as steps complete)

| Step | What | Status |
|---|---|---|
| 1 | Bare reverse proxy | ✅ done |
| 2 | In-memory weighted token bucket | ✅ done |
| 3 | Redis fixed window (INCRBY+EXPIRE) | ✅ done |
| 4 | Lua token bucket (atomic read-refill-debit) | ✅ done |
| 5 | Cost estimator + reconcile, then full gateway wiring | ✅ done |
| 6 | Multi-node invariant test | ⬜ not started |
| 7 | Dollar rules + most-restrictive multi-rule eval | ⬜ not started |
| 8 | Degradation: fail-closed, timeouts, in-memory fallback | ⬜ not started |
| 9 | Redis Cluster sharding + dynamic rule reload | ⬜ not started |
| 10 | Stretch: blocklist, admin/usage, Prometheus | ⬜ not started |

---

## Step 1 — Bare reverse proxy ✅

**Goal / the "why":** get a running, testable artifact on day one. A gateway is
a reverse proxy before it is anything else; everything later (identify,
estimate, limit, reconcile) is middleware layered in front of this proxy.
`httputil.ReverseProxy` streams responses unbuffered, which is exactly what
token-by-token LLM responses need.

**Files & functions (complete):**
- `cmd/gateway/main.go` — flag parsing (`-addr`, `-upstream`),
  `httputil.NewSingleHostReverseProxy`, and the `Director` override that
  rewrites `req.Host` to the upstream host (TLS SNI / vhost routing breaks
  without it).

**How to verify:**
```bash
go run ./cmd/gateway -upstream https://httpbin.org
curl -s localhost:8080/get -i     # response comes from the upstream, streamed
```
**Done when:** requests round-trip through the proxy; `make build` green.

**Background:** ARCHITECTURE.md §3.2 (placement), ADR-003.

---

## Step 2 — In-memory weighted token bucket ✅

**Goal / the "why":** prove the *algorithm* in isolation before adding any
distribution. Token bucket is the project default (ADR-005) because it natively
subtracts a weighted cost `w` — perfect for "this request is worth 4,200
tokens" — while capacity `C` bounds bursts and refill `R` bounds the average.
One client, one node, no locking, no Redis: just the math, table-tested.

**Files & functions (complete):**
- `internal/limiter/bucket.go` — `NewBucket` (starts **full**: the cold-start
  burst trade-off, ADR-005), `Bucket.Allow(now, cost)` (lazy refill
  `tokens = min(C, tokens + elapsed·R)`, then conditional debit). `now` is a
  parameter *on purpose* — deterministic tests, no sleeps.
- `internal/limiter/bucket_test.go` — table-driven `TestBucketAllow`.

**Done when:** `go test ./internal/limiter/ -run TestBucket` green with no
Redis running.

**Background:** ARCHITECTURE.md §4 "Token bucket", ADR-005.

---

## Step 3 — Redis fixed window (`INCRBY`+`EXPIRE`) ✅

**Goal / the "why":** move budget state **off-process** by the simplest atomic
path. `INCRBY` is atomic server-side, so the read-modify-write race disappears
without any Lua — the returned new total is both your read and your write.
Deliberately blunt: a fixed window allows a 2× burst across the boundary;
building it first makes you *feel* the problem the sliding window (step 7 area)
and token bucket (step 4) each solve.

**Files & functions (complete):**
- `internal/limiter/fixedwindow.go` — `NewFixedWindow`,
  `FixedWindow.Allow` (window-index-in-the-key + pipelined
  `INCRBY`/`PEXPIRE`; decision only after `Exec`), `Reconcile` (best-effort
  `IncrBy` of the delta on the current window).
- `internal/limiter/fixedwindow_test.go` — plus the shared helpers
  `redisForTest` / `newKey` used by every Redis-backed test after this.

**Key lesson encoded in the file:** the "EXPIRE only on first increment" bug,
and how window-in-the-key sidesteps it (see comment at the bottom of
`fixedwindow.go`).

**Done when:** `docker compose up -d redis && go test ./internal/limiter/`
green including `TestFixedWindow*`.

**Background:** ARCHITECTURE.md §4 "Why correctness is hard", ADR-009.

---

## Step 4 — Lua token bucket ✅

**Goal / the "why":** move the step-2 bucket algorithm into a **single Redis
Lua script**, so read → lazy-refill → conditional-debit → write executes
atomically *server-side*. This is THE distributed-correctness win: N stateless
gateways can hammer one key and the budget still holds, because no two of them
can interleave a stale read with a write (ADR-009). Redis runs a Lua script as
one atomic unit; the whole decision is one `EVALSHA` round-trip (§5.3).

**Scope:** just the `TokenBucket` limiter unit + a test — the standalone rhythm
of steps 2–3. Do **not** wire `main.go`/`gateway.go` yet (that's step 5).
`slidingwindow.go` + `sliding_window.lua` are the optional A/B comparison
algorithm with the same call shape — skip unless you want the comparison now;
they're required by config rules at step 5, so they must exist before then.

**Files & functions to complete (in this order):**

1. `internal/limiter/scripts/token_bucket.lua` — the actual algorithm. ARGV
   parsing is already there; implement the body per the 6 inline HINTs:
   - `HMGET` tokens, ts → cold start (nil) = **full** bucket
     (tokens=capacity, ts=now) — ADR-005's burst trade-off
   - lazy refill: `elapsed = max(0, now-ts)/1000`;
     `tokens = min(capacity, tokens + elapsed*refill)`
   - conditional debit: only if `tokens >= cost` set `allowed=1` and subtract
   - persist: `HSET` tokens+ts, then `PEXPIRE` ttl
   - `reset_ms` for Retry-After when short:
     `ceil((cost - tokens)/refill*1000)`, guard `refill > 0`
   - return `{ allowed, tostring(tokens), reset_ms }` (stringify keeps
     fractional precision; `parseDecision` reads it back)

2. `internal/limiter/tokenbucket.go` — two thin Go wrappers over the scripts:
   - `Allow(ctx, key, cost)` →
     `tokenBucketScript.Run(ctx, t.rdb, []string{key}, t.capacity, t.refill,
     cost, now.UnixMilli(), t.ttl.Milliseconds())`, error-check (return
     `(Decision{}, err)` — the *gateway* owns fail-open/closed policy,
     ADR-008), then `return parseDecision(res, t.capacity, now)`. The ARGV
     order MUST match the `.lua` header exactly.
   - `Reconcile(ctx, key, delta)` →
     `tokenBucketReconcile.Run(ctx, t.rdb, []string{key}, t.capacity, delta,
     t.ttl.Milliseconds()).Err()`

3. `internal/limiter/scripts/token_bucket_reconcile.lua` — the settlement
   (`delta = actual − estimate`): `HGET` tokens → nil means expired, return
   `tostring(0)`; else `tokens = max(0, min(capacity, tokens - delta))`;
   `HSET` + `PEXPIRE`; return `tostring(tokens)`.

4. `internal/limiter/tokenbucket_test.go` — **new file**, mirroring
   `fixedwindow_test.go`'s helpers (`redisForTest`, `newKey`). Cover at least:
   - full bucket admits a weighted `cost > 1`; `Remaining` reflects the debit
   - `cost > capacity` → denied, tokens unchanged, `ResetAt` in the future
   - refill: exhaust, real `time.Sleep`, then a request is admitted again
     (the no-injectable-clock caveat — sleep, don't fast-forward)
   - fractional precision survives (e.g. refill 2.5/s)
   - `Reconcile`: delta=0 no-op; +delta debits more; −delta refunds and
     clamps at capacity; reconcile on an expired key is a no-op

**How to verify:**
1. `make redis` — without it the new tests self-skip.
2. `go build ./... && go vet ./...` clean.
3. `go test ./internal/limiter/` passes including the new token-bucket cases.

**Done when:** all of the above, plus you can explain *why* `MULTI/EXEC`
wouldn't have worked here (the read that informs the decision happens before
the transaction — ADR-009).

**Background:** ARCHITECTURE.md §4 "Why correctness is hard, and the fix",
ADR-005, ADR-009.

---

## Step 5 — Cost estimator + reconcile, then wire the gateway ⬜

**Goal / the "why":** this is **the niche** (ADR-001, ADR-006). The true token
cost is unknown until the provider responds, so: (a) pre-flight, estimate
worst-case cost (prompt tokens + `max_tokens`) and **optimistically debit** it
atomically before proxying — we never under-protect under concurrency; (b)
post-response, read the provider's `usage` and settle
`delta = actual − estimate` (refund or extra debit). Small drift from a failed
reconcile heals via TTL.

**Part A — the cost package.** `internal/cost/cost_test.go` already asserts
all of this behavior and **stays red until you implement it** — this step is
test-driven by design.

1. `internal/cost/estimator.go`:
   - `EstimateFromBody(body, defaultMaxTokens)` — best-effort
     `json.Unmarshal` into `chatRequest`; count runes across every message
     `Content` plus legacy `Prompt` (`utf8.RuneCountInString`); if 0, fall
     back to `utf8.RuneCount(body)` so we never estimate zero;
     `maxTok = req.MaxTokens` or the default. (Re-add the `encoding/json` and
     `unicode/utf8` imports.)
   - `approxTokens(chars)` — `max(1, chars/4)` (~4 chars/token heuristic; a
     real tokenizer is a logged follow-up — keep the signature stable).
2. `internal/cost/reconciler.go`:
   - `UsageFromResponse(body)` — unmarshal `usageEnvelope`; `(Usage{}, false)`
     on error or `Total() == 0` (streaming/SSE bodies skip reconciliation);
     else `(env.Usage, true)`.
   - `TokenDelta(est, actual)` — `float64(actual.Total() - est.Total())`.
3. `internal/cost/pricing.go`:
   - `priceFor(model)` — `p.Models[model]` when present, else `p.Default`.
   - `Dollars(model, in, out)` —
     `in/1000·InputPer1K + out/1000·OutputPer1K`.
   - `EstimateDollars(e)` — `p.Dollars(e.Model, e.PromptTokens, e.MaxTokens)`
     (treats `MaxTokens` as output — worst case).

**Part B — wire the full chain.** `internal/gateway/gateway.go` (the whole
middleware chain: identify → estimate → limit → proxy → reconcile → observe),
`internal/identify`, `internal/rules`, `internal/config`, `internal/store`,
and `internal/metrics` are **already implemented** — read them now; this is
where every piece you built meets. What remains:

4. `cmd/gateway/main.go` — replace the step-1 bare proxy with real wiring:
   - `config.Load(path)` from a `-config` flag (keep `config.example.yaml` →
     `config.yaml` copy flow)
   - `store.NewRedis(cfg.Redis.Addr, cfg.Redis.Timeout)`
   - build one `limiter.Limiter` per rule ID, switching on
     `rule.Algorithm`: `AlgoTokenBucket` →
     `limiter.NewTokenBucket(rdb, r.Capacity(), r.RefillPerSec(), ttl)`;
     `AlgoSlidingWindow` →
     `limiter.NewSlidingWindow(rdb, r.Limit, r.Window, 0)`
     (so finish `slidingwindow.go` + `sliding_window.lua` now if you skipped
     them in step 4 — the example config uses both algorithms)
   - `gateway.New(engine, limiters, cfg.Pricing, cfg.Upstream.BaseURL,
     cfg.Upstream.DefaultMaxTokens)`
   - mount the gateway at `/` and `metrics.Handler()` at `/debug/vars`

**How to verify:**
1. `go test ./internal/cost/` — the pre-written tests go green (no Redis).
2. `cp config.example.yaml config.yaml`, point `upstream.base_url` somewhere
   real (or a local stub), `make redis && make run`, then the README curl:
   look for `X-RateLimit-Remaining` on the way in and `X-Cost-Tokens-Actual`
   after the response; hammer until you get a `429` with `Retry-After`.
3. `curl localhost:8080/debug/vars` — `tokens_metered`, `allowed`, `denied`
   move.

**Done when:** cost tests green; a real request is estimated, debited,
proxied, reconciled, and metered end-to-end.

**Background:** ARCHITECTURE.md §4 "The cost twist", ADR-006; §3.4 lifecycle.

---

## Step 6 — Multi-node invariant test ⬜

**Goal / the "why":** the defining test of the whole project (ARCHITECTURE.md
§9, README "The invariant"). N stateless replicas + one Redis must enforce ONE
global budget. **If admitted spend scales with replica count, the state isn't
truly shared and the design has failed.** Everything before this was building
toward the moment you can prove it.

**Files & functions to complete:**
1. A **stub upstream** so the flood doesn't hit a paid provider — e.g.
   `cmd/stubllm/main.go`: an HTTP server that answers any POST with a small
   JSON body containing a `usage` object (fixed or derived
   `prompt_tokens`/`completion_tokens`). Add it as a `stubllm` service in
   `docker-compose.yml` and point `config.yaml`'s `upstream.base_url` at it.
2. `internal/config/config.go` — honor the `REDIS_ADDR` env var as an
   override of `redis.addr` (docker-compose.yml already sets
   `REDIS_ADDR=redis:6379` for the gateway replicas; nothing reads it yet).
3. The compose stack — `docker-compose.yml` already defines `redis`,
   `gateway1` (:8081), `gateway2` (:8082) built from the `Dockerfile`. Get
   `docker compose up --build` running clean.
4. The invariant driver — either a shell script (`scripts/invariant.sh`) or a
   Go test tagged `//go:build invariant`: set a small token budget for one
   key in `config.yaml` (e.g. 10,000 tokens/min), fire M concurrent requests
   of known estimated cost **alternating/round-robin across :8081 and
   :8082**, count admissions (2xx) vs denials (429), and assert
   `total admitted estimated tokens ≈ budget` (within one request's cost),
   regardless of the split across nodes.

**How to verify:**
```bash
docker compose up --build -d
./scripts/invariant.sh        # or: go test -tags invariant ./...
```
**Done when:** the invariant holds with 2 replicas; then prove the negative —
point the two gateways at *separate* Redis instances (or an in-memory limiter)
and watch admitted spend double. Write down both numbers in the README.

**Background:** ARCHITECTURE.md §9, §3.3 topology.

---

## Step 7 — Dollar rules + most-restrictive multi-rule eval ⬜

**Goal / the "why":** a request can match several rules at once (per-key
tokens/min, per-key $/day, global req/s, per-IP) and the **most restrictive
wins — deny if any denies** (ADR-007). Dollars are just tokens × price table
(ADR-001): same limiter, different weight. The *mechanism* already exists in
code — this step is about exercising, testing, and understanding it, not
writing new plumbing.

**Files & functions to complete:**
1. **Read + verify existing code** (built in earlier steps, so far untested):
   - `internal/rules/rules.go` — `Engine.Match` (scope → identifier via
     `Identity.For`, model filter, hash-tagged key `rl:{<client>}:<ruleID>`),
     `Rule.Capacity`, `Rule.RefillPerSec`
   - `internal/gateway/gateway.go` — the multi-rule loop in `ServeHTTP`
     (deny-if-any-denies; `best` = lowest `Remaining` drives the
     `X-RateLimit-*` headers), `weightFor` (unit → weight)
2. `internal/rules/rules_test.go` — **new file**, pure Go (no Redis):
   layered matching (api_key + ip + global all match one request; anonymous
   request skips api_key rules), model filtering, `Capacity`/`RefillPerSec`
   defaults (Burst=0 → Limit; Window=0 → Limit/s).
3. `internal/gateway/gateway_test.go` — **new file**, `httptest` +
   `httptest.NewServer` stub upstream + a **fake in-memory `Limiter`** (the
   interface makes this trivial — no Redis): one rule allows, another
   denies → response is 429; headers report the *binding* rule's numbers;
   fail-open vs fail-closed on a limiter that returns an error.
4. `config.yaml` — enable the `key-dollars-per-day` rule (already in the
   example config) and verify end-to-end: a cheap model passes where an
   expensive one (higher price/1k) trips the dollar rule first.

**Done when:** rules + gateway tests green without Redis; you can demonstrate
a request blocked by its dollar rule while its token rule still had room, and
explain why the headers show the values they do.

**Background:** ARCHITECTURE.md §3.4, ADR-004, ADR-007.

---

## Step 8 — Degradation: timeouts, fail-closed, in-memory fallback ⬜

**Goal / the "why":** Redis is on the critical path; decide what happens when
it's slow or down. For a *cost* gateway, failing open means you stop counting
spend at the exact moment you can't see it — an unbounded-bill risk — so the
default is **fail-closed, per-rule configurable** (ADR-008: this deliberately
flips the usual rate-limiter default). Tight per-op deadlines make a *slow*
shard trip the same path as a *down* shard.

**Already in place (verify, don't rebuild):** `internal/store/store.go` sets
per-op read/write timeouts; `gateway.ServeHTTP` branches on `m.Rule.FailOpen`
(continue) vs fail-closed (503 `rate_limiter_unavailable`) and counts
`store_errors`.

**Files & functions to complete:**
1. `internal/limiter/fallback.go` — **new file**: a `Fallback` limiter that
   wraps a primary `Limiter` and, when the primary errors, consults a coarse
   **per-node in-memory** limiter instead of failing. Reuse step 2's
   `Bucket` guarded by a `sync.Mutex` in a `map[string]*Bucket` keyed by
   client key. It's approximate (each node has its own budget slice — bound
   the blast radius by dividing capacity by an assumed replica count), and
   that approximation is the documented trade-off.
2. `internal/limiter/fallback_test.go` — fake primary that errors → fallback
   decides; primary healthy → fallback untouched.
3. Wiring: in `main.go`, wrap rules that opt in (e.g. a `fallback: true`
   rule field through `config.go` + `rules.go`) — or keep it global; your
   call, but record the choice in `docs/DECISIONS.md` as a new ADR note.

**How to verify:**
1. Full stack up, traffic flowing → `docker compose stop redis`.
2. Fail-closed rule (api-key tokens) → immediate 503s, `store_errors`
   climbing; fail-open rule (global req/s) → traffic still flows.
3. `docker compose start redis` → recovery with no restart.
4. Slow-shard path: set `redis.timeout_ms: 1` and watch timeouts behave
   exactly like an outage.

**Done when:** you can narrate the full degradation story: deadline → error →
per-rule policy → fallback → recovery, and the fallback tests are green.

**Background:** ARCHITECTURE.md §5.2, §5.3, ADR-008.

---

## Step 9 — Redis Cluster sharding + dynamic rule reload ⬜

**Goal / the "why":** two production-scale concerns. (a) One Redis caps at
~100–200k ops/s; past that, **shard by client key** with Redis Cluster —
16,384 hash slots, automatic routing/failover — instead of hand-rolled
consistent hashing (ADR-010). The **hash tag** in `rl:{<client>}:<ruleID>`
(already there since step 5!) is what co-locates one client's token-rule and
dollar-rule keys on one slot. (b) Limits must change without a redeploy: rules
hot-reload into memory; only *counters* live in Redis (ADR-011).

**Files & functions to complete:**
1. `internal/store/store.go` — add a cluster path, e.g.
   `NewRedisCluster(addrs []string, timeout) (*redis.ClusterClient, error)`.
   Everything downstream already takes `redis.Cmdable`, so **no limiter code
   changes** — that interface choice pays off here.
2. `internal/config/config.go` — `redis.cluster_addrs: []` (presence selects
   cluster mode); plumb through `main.go`.
3. Rule reload:
   - `internal/rules/rules.go` — make `Engine` swappable:
     `Engine.Replace(rs []Rule)` guarding the rule slice with
     `sync.RWMutex` (or `atomic.Pointer`); `Match` takes the read lock.
   - `cmd/gateway/main.go` — a goroutine that polls the config file's
     mtime every few seconds (stdlib-only; fsnotify optional), re-runs
     `config.Load`, and calls `engine.Replace` on change. Log reloads;
     malformed config keeps the old rules (never crash the hot path).
4. `docker-compose.yml` — optional `redis-cluster` profile (3 masters) for a
   real cluster run; otherwise verify cluster mode against a single-node
   cluster or note it as exercised-by-interface.

**How to verify:**
1. `go test -race ./internal/rules/` with a test that hammers `Match` while
   `Replace` swaps rules — no race.
2. Live demo: gateway running, edit `config.yaml` to halve a limit, see 429s
   begin within the reload interval — no restart.
3. Cluster mode: keys for one client land on one slot (`CLUSTER KEYSLOT
   rl:{acme}:key-tokens-per-min` equals the dollar rule's slot) — the hash
   tag doing its job; a multi-key Lua call across *different* clients would
   be refused (cross-slot), which is why keys are per-client.

**Done when:** rule edits propagate live, race detector clean, and you can
explain hash tags + why cross-slot scripts are forbidden.

**Background:** ARCHITECTURE.md §5.1, §5.5, ADR-010, ADR-011.

---

## Step 10 — Stretch: hot-key blocklist, admin/usage, Prometheus ⬜

**Goal / the "why":** the operability layer (ARCHITECTURE.md §5.4, §8). Pick
any subset, in any order — the core project is complete after step 9; these
are portfolio polish with real content behind each.

**a) Hot-key blocklist.** A single abusive key can overwhelm its shard;
after repeated denials, block it cheaply at the edge before any budget math.
- `internal/limiter/blocklist.go` — on deny, `INCR bl:deny:{client}` with a
  TTL; past a threshold, `SET bl:block:{client} 1 EX <cooldown>`. In
  `gateway.ServeHTTP`, check the block key first (one `EXISTS` — or fold it
  into the bucket Lua script for zero extra round-trips) and reject with 429
  + a long `Retry-After`.

**b) Admin / usage endpoint.** `GET /admin/usage?client=acme` → remaining
budget per rule for that client (read the bucket hashes; never mutate),
plus current rule definitions. Read-only, no auth beyond a static admin
token — it's a portfolio project, but say so in the code.

**c) Prometheus + Grafana.** Swap `internal/metrics` from expvar to
`prometheus/client_golang` — **keep the call sites identical** (the package
comment promises this): counters for allow/deny/store_errors/reconciles,
counters for tokens & **dollars** metered (the niche metric nobody else has),
a histogram for limiter-check latency (prove the <10ms p99 claim,
ARCHITECTURE.md §1). Add `prometheus` + `grafana` services to compose; one
dashboard: spend per client, deny rate, check latency.

**Done when:** whichever you pick works end-to-end in the compose stack and
gets a short section in the README with a screenshot or curl transcript.

**Background:** ARCHITECTURE.md §5.4, §8; README "Dev".

---

## After step 10 — the wrap-up

- Re-run the step-6 invariant with the final system; update the README
  numbers.
- Sweep `docs/DECISIONS.md`'s "Open questions" — answer or explicitly punt.
- Update the resume framing (ARCHITECTURE.md §10) with real measured numbers.
