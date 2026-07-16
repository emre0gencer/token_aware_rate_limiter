# Token-Aware LLM Rate-Limiting Gateway

A horizontally-scalable reverse-proxy gateway, written in Go, that meters traffic to LLM providers by **token spend and dollar budget** — not request count — and enforces those limits *globally* across stateless replicas using Redis + atomic Lua scripts.

It's a learning project built around the [HelloInterview "Rate Limiter"](https://www.hellointerview.com/learn/system-design/problem-breakdowns/rate-limiter) breakdown: every design decision is deliberate and documented so the *why* is visible throughout.

- **What makes it different:** variable per-request cost, cost-unknown-until-response (estimate → reconcile), and a fail-**closed** default because failing open on an LLM proxy means an unbounded bill. See [`ARCHITECTURE.md`](ARCHITECTURE.md).
- **Why each choice was made:** [`docs/DECISIONS.md`](docs/DECISIONS.md) (ADR-001 … ADR-011).

## How it works (one request)

```
request
  → identify   API key → JWT user → IP
  → estimate   prompt tokens + max_tokens  (optimistic, worst-case)
  → limit      Allow(rule, client, weight) for every matched rule; most-restrictive wins
  → proxy      stream to the upstream LLM provider                     │ 429 if over budget
  → reconcile  read usage from the response, settle (actual − estimate)
  → observe    tokens & dollars metered
response  + X-RateLimit-* / X-Cost-* headers
```

The race-free core is a single Redis **Lua script** per check (`internal/limiter/scripts/`): read bucket → lazy-refill → conditionally debit → write, all atomic.

## Quickstart

```bash
cp config.example.yaml config.yaml      # point upstream.base_url at your provider
docker compose up -d redis              # or: redis-server
make run                                # go run ./cmd/gateway -config config.yaml
```

Send a request (it gets metered, then proxied):

```bash
curl -s localhost:8080/v1/chat/completions \
  -H 'X-API-Key: acme' -H 'Content-Type: application/json' \
  -d '{"model":"gpt-4o","max_tokens":256,"messages":[{"role":"user","content":"hello"}]}' -i
# look for X-RateLimit-Remaining and (after the response) X-Cost-Tokens-Actual
```

Metrics (tokens & dollars metered, allow/deny, store errors): `curl localhost:8080/debug/vars`.

## Layout

```
cmd/gateway            entry point + wiring
internal/identify      client identification (api key / user / ip)
internal/rules         rule model + most-restrictive matching
internal/limiter       Limiter interface + token bucket & sliding window (Lua)
internal/cost          estimator, pricing (tokens→$), reconciler
internal/store         Redis client (pooling, timeouts)
internal/gateway       the middleware chain (estimate→limit→proxy→reconcile)
internal/metrics       expvar counters (tokens, dollars, allow/deny)
docs/DECISIONS.md      ADRs — the "why" for every choice
```

## Build order (incremental — build the next thing only once the last works)

Full per-step instructions — goals, files, functions, verification — live in [`INSTRUCTIONS.md`](INSTRUCTIONS.md), the build guidebook.

1. Bare reverse proxy → one upstream.
2. In-memory weighted token bucket (single node).
3. Redis sliding window (`INCRBY`+`EXPIRE`).
4. **Lua token bucket** — atomic read-refill-debit (the distributed-correctness win).
5. **Cost estimator + reconcile** — the niche.
6. **Multi-node invariant test** — N replicas, one key, assert the *global* budget holds.
7. Dollar rules + most-restrictive multi-rule eval.
8. Degradation — timeouts, fail-closed default, in-memory fallback.
9. Redis Cluster sharding + dynamic rule reload.
10. Stretch — hot-key blocklist, admin/usage dashboard, Prometheus/Grafana.

## The invariant that proves it works

Spin up N replicas, set a token budget for one key, flood the cluster through the LB, and verify total admitted tokens ≈ the budget **regardless of how requests spread across nodes**, and that post-reconcile spend matches the providers' reported `usage`. If admitted spend scales with replica count, the state isn't truly shared — the design has failed.

The step-6 harness proves it end-to-end: `redis` + a zero-cost fake upstream (`cmd/stubllm`) + two stateless gateway replicas (`:8081`, `:8082`), driven by `scripts/invariant.sh`, which floods both nodes round-robin with requests of a known 500-token estimate against a **10,000-token/min** budget.

```bash
make compose         # redis + stubllm + gateway1(:8081) + gateway2(:8082)
make invariant       # flood both nodes; assert admitted spend ≈ budget

make invariant-split # negative control: gateway2 on its OWN redis
make compose-down    # tear everything down
```

**Measured** (120 requests, 500 tokens each, budget 10,000):

| Topology | Admitted | Admitted tokens | Ratio to budget | Verdict |
|---|---|---|---|---|
| **Shared Redis** (both replicas → one Redis) | 20 (10 + 10 across nodes) | 10,000 | **1.00×** | ✅ budget holds globally |
| **Split Redis** (each replica → its own Redis) | 40 (20 + 20 across nodes) | 20,000 | **2.00×** | ❌ spend doubles — state not shared |

The 2.00× is the failure mode the shared-state design exists to prevent: with per-node Redis, each replica enforces its own private budget, so admitted spend scales with replica count. The invariant is isolated from reconcile timing by running the stub with `-echo-max-tokens` (actual usage == estimate ⇒ settlement delta = 0), so admission is governed purely by the atomic optimistic debit.

## Dev

```bash
make test     # pure-Go unit tests (cost logic) — no Redis needed
make build    # compile
make vet      # go vet
```

Integration tests that exercise the Lua scripts need a real Redis (`docker compose up -d redis`).
