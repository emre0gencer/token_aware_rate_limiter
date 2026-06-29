-- Settle the optimistic pre-flight debit against the provider's reported usage.
-- delta = actual - estimate.  delta > 0 => consume more; delta < 0 => refund.
-- KEYS[1] = bucket key
-- ARGV[1] = capacity
-- ARGV[2] = delta
-- ARGV[3] = ttl_ms
-- returns tokens(string) after adjustment

local capacity = tonumber(ARGV[1])
local delta    = tonumber(ARGV[2])
local ttl      = tonumber(ARGV[3])

local tokens = tonumber(redis.call('HGET', KEYS[1], 'tokens'))
if tokens == nil then
  return tostring(0)        -- bucket already expired; nothing to settle
end

tokens = math.max(0, math.min(capacity, tokens - delta))
redis.call('HSET', KEYS[1], 'tokens', tokens)
redis.call('PEXPIRE', KEYS[1], ttl)
return tostring(tokens)
