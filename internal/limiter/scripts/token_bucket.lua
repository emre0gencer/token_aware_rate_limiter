-- Token bucket, executed atomically by Redis (no read-modify-write race across gateways).
-- KEYS[1] = bucket key (hash with fields: tokens, ts)
-- ARGV[1] = capacity        (max tokens / burst)
-- ARGV[2] = refill          (tokens per second)
-- ARGV[3] = cost            (weight of THIS request: 1, or estimated LLM tokens, or dollars)
-- ARGV[4] = now_ms          (caller clock, ms)
-- ARGV[5] = ttl_ms          (auto-expire idle buckets)
-- returns { allowed(0|1), remaining(string), reset_ms }

local capacity = tonumber(ARGV[1])
local refill   = tonumber(ARGV[2])
local cost     = tonumber(ARGV[3])
local now      = tonumber(ARGV[4])
local ttl      = tonumber(ARGV[5])

-- TODO (STEP 4): implement the atomic read -> lazy-refill -> conditional debit
-- -> write. This is THE distributed-correctness win: all of it runs server-side
-- so concurrent gateways can't interleave a stale read with a write.
--
-- HINT 1 — read current state with HMGET KEYS[1] 'tokens' 'ts'. On a cold bucket
--          (tokens == nil) start FULL: tokens = capacity, ts = now (the
--          cold-start burst trade-off, ADR-005).
local state = redis.call("HMGET", KEYS[1], 'tokens', 'ts')
local tokens = tonumber(state[1])
local ts = tonumber(state[2])
if tokens == nil then
    tokens = capacity
    ts = now
end
-- HINT 2 — lazy refill by elapsed time, capped at capacity:
local elapsed = math.max(0, now - ts) / 1000.0
tokens = math.min(capacity, tokens + elapsed * refill)
-- HINT 3 — conditional debit: only when tokens >= cost do you set allowed = 1
--          and tokens = tokens - cost; otherwise allowed = 0 and don't spend.
local allowed = 0
local reset_ms = 0
if tokens >= cost then
    allowed = 1
    tokens = tokens - cost
else
    if refill > 0 then
        reset_ms = math.ceil((cost - tokens) / refill * 1000)
    else
        reset_ms = 0
    end
end
-- HINT 4 — persist: HSET KEYS[1] 'tokens' tokens 'ts' now, then
--          PEXPIRE KEYS[1] ttl so idle buckets disappear.
redis.call("HSET", KEYS[1], "tokens", tokens, "ts", now)
redis.call("PEXPIRE", KEYS[1], ttl)
-- HINT 5 — reset_ms for Retry-After: when short, ceil((cost - tokens)/refill*1000)
--          (guard refill > 0); otherwise 0.
-- HINT 6 — return { allowed, tostring(tokens), reset_ms }. tokens is stringified
--          so fractional precision survives (parseDecision reads it back).
return { allowed, tostring(tokens), reset_ms }
