-- Settle the optimistic pre-flight debit against the provider's reported usage.
-- delta = actual - estimate.  delta > 0 => consume more; delta < 0 => refund.
-- Paired with token_bucket.lua (the token-bucket family, STEP 4).
-- KEYS[1] = bucket key
-- ARGV[1] = capacity
-- ARGV[2] = delta
-- ARGV[3] = ttl_ms
-- returns tokens(string) after adjustment

local capacity = tonumber(ARGV[1])
local delta    = tonumber(ARGV[2])
local ttl      = tonumber(ARGV[3])

-- TODO: settle the bucket after the response.
-- HINT 1 — HGET KEYS[1] 'tokens'. If nil the bucket already expired; there is
--          nothing to settle, so return tostring(0).
-- HINT 2 — apply the settlement and clamp into range (subtract, so a positive
--          delta consumes more and a negative one refunds):
--          tokens = math.max(0, math.min(capacity, tokens - delta))
-- HINT 3 — HSET the new tokens, PEXPIRE KEYS[1] ttl, return tostring(tokens).

return tostring(0) -- TODO
