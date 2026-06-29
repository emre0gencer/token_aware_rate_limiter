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

local bucket = redis.call('HMGET', KEYS[1], 'tokens', 'ts')
local tokens = tonumber(bucket[1])
local ts     = tonumber(bucket[2])

if tokens == nil then        -- cold start: full bucket
  tokens = capacity
  ts = now
end

-- lazy refill based on elapsed time
local elapsed = math.max(0, now - ts) / 1000.0
tokens = math.min(capacity, tokens + elapsed * refill)

local allowed = 0
if tokens >= cost then
  tokens = tokens - cost
  allowed = 1
end

redis.call('HSET', KEYS[1], 'tokens', tokens, 'ts', now)
redis.call('PEXPIRE', KEYS[1], ttl)

-- approximate time until enough tokens refill for one 'cost' unit (for Retry-After)
local reset_ms = 0
local deficit = cost - tokens
if deficit > 0 and refill > 0 then
  reset_ms = math.ceil((deficit / refill) * 1000)
end

return { allowed, tostring(tokens), reset_ms }
