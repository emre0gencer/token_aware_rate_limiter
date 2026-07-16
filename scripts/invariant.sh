#!/usr/bin/env bash
#
# scripts/invariant.sh — the STEP-6 multi-node invariant driver.
#
# THE INVARIANT: N stateless gateway replicas backed by ONE shared Redis must
# enforce ONE global token budget. We flood the replicas round-robin with
# requests of a known estimated cost and assert:
#
#     total admitted (estimated) tokens  ≈  the configured budget
#
# regardless of how the flood splits across nodes. If admitted spend instead
# scales with replica count, the state isn't truly shared and the design failed
# (run docker-compose.split.yml to see exactly that — spend doubles).
#
# It needs no Redis access: each run uses a fresh client key, so the token
# bucket always starts full and runs are independent.
#
# Usage:
#   docker compose up --build -d          # bring up redis + stubllm + 2 gateways
#   ./scripts/invariant.sh                # PASS when the global budget holds
#
# Tunables (env):
#   ENDPOINTS      space-separated gateway base URLs   (default: :8081 :8082)
#   BUDGET_TOKENS  the per-key token budget under test (default: 10000)
#   PROMPT_CHARS   chars of prompt content per request (default: 400 -> 100 tok)
#   MAX_TOKENS     max_tokens per request              (default: 400)
#   REQUESTS       total requests to fire              (default: 120)
#   CONCURRENCY    parallel in-flight requests         (default: 40)
#   KEY            X-API-Key for this run   (default: unique per run)
set -euo pipefail

ENDPOINTS="${ENDPOINTS:-http://localhost:8081 http://localhost:8082}"
BUDGET_TOKENS="${BUDGET_TOKENS:-10000}"
PROMPT_CHARS="${PROMPT_CHARS:-400}"
MAX_TOKENS="${MAX_TOKENS:-400}"
REQUESTS="${REQUESTS:-120}"
CONCURRENCY="${CONCURRENCY:-40}"
KEY="${KEY:-inv-$(date +%s)-${RANDOM}}"

# Per-request estimated cost must mirror the gateway's estimator exactly:
#   PromptTokens = max(1, chars/4);  Total = PromptTokens + max_tokens.
prompt_tokens=$(( PROMPT_CHARS / 4 ))
(( prompt_tokens < 1 )) && prompt_tokens=1
COST=$(( prompt_tokens + MAX_TOKENS ))

# Fixed body: PROMPT_CHARS copies of 'x' as the prompt content (ASCII => 1 rune
# each), so every request estimates to exactly COST tokens.
content=$(head -c "$PROMPT_CHARS" < /dev/zero | tr '\0' 'x')
BODY=$(printf '{"model":"stub-model","max_tokens":%d,"messages":[{"role":"user","content":"%s"}]}' \
  "$MAX_TOKENS" "$content")

echo "── invariant flood ─────────────────────────────────────────────"
echo "  endpoints     : $ENDPOINTS"
echo "  client key    : $KEY"
echo "  budget        : $BUDGET_TOKENS tokens"
echo "  per-request   : $COST tokens ($prompt_tokens prompt + $MAX_TOKENS max)"
echo "  requests      : $REQUESTS  (concurrency $CONCURRENCY)"
echo "  expected admit: ~$(( BUDGET_TOKENS / COST )) requests  (budget / cost)"
echo "────────────────────────────────────────────────────────────────"

# Worker: fire request #i at the i-th endpoint (round-robin), print "<code> <ep>".
worker() {
  local i="$1"
  local eps=($ENDPOINTS)                       # re-split in the subshell
  local ep="${eps[$(( i % ${#eps[@]} ))]}"
  local code
  code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 15 \
    -X POST "$ep/v1/chat/completions" \
    -H "X-API-Key: $KEY" \
    -H 'Content-Type: application/json' \
    --data-binary "$BODY" || echo 000)
  echo "$code $ep"
}
export -f worker
export ENDPOINTS KEY BODY

results=$(seq 1 "$REQUESTS" | xargs -P "$CONCURRENCY" -I{} bash -c 'worker "$@"' _ {})

# Tally admissions (2xx) vs denials (429) vs errors, plus the per-node split, and
# render the verdict. admitted_tokens = admitted * COST is the number the whole
# project is about: it must track the budget, not the replica count.
echo "$results" | awk -v cost="$COST" -v budget="$BUDGET_TOKENS" '
  {
    total++
    code=$1; ep=$2
    if (code ~ /^2/)      { admitted++; node[ep]++ }
    else if (code=="429") { denied++ }
    else                  { errors++; ecode[code]++ }
  }
  END {
    admitted_tokens = admitted * cost
    ratio = admitted_tokens / budget
    print ""
    printf "  admitted      : %d  (%d tokens)\n", admitted, admitted_tokens
    printf "  denied (429)  : %d\n", denied
    if (errors > 0) {
      msg=""
      for (c in ecode) msg = msg sprintf(" %s×%d", c, ecode[c])
      printf "  errors        : %d (%s )\n", errors, msg
    }
    print  "  per-node admit:"
    for (e in node) printf "                  %-26s %d\n", e, node[e]
    printf "  admitted/budget ratio : %.2fx\n", ratio
    print "────────────────────────────────────────────────────────────────"

    low = budget - cost; high = budget + cost      # "within one request'\''s cost"
    if (admitted_tokens >= low && admitted_tokens <= high) {
      print "  ✅ PASS — global budget held; admitted spend ≈ budget across nodes."
      exit 0
    }
    if (admitted_tokens > high) {
      printf "  ❌ FAIL — admitted spend is %.2f× the budget: state is NOT shared\n", ratio
      print  "           (expected when replicas use separate Redis instances)."
      exit 1
    }
    print "  ❌ FAIL — admitted spend fell short of the budget (under-admitting)."
    exit 1
  }
'
