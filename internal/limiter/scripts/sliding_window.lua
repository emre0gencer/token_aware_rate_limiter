-- Sliding-window counter (current + weighted previous window) in a single hash
-- so all state for one client stays on one Redis slot.
-- KEYS[1] = key (hash with fields: cur, prev, start)
-- ARGV[1] = limit
-- ARGV[2] = window_ms
-- ARGV[3] = cost
-- ARGV[4] = now_ms
-- ARGV[5] = ttl_ms
-- returns { allowed(0|1), remaining(string), reset_ms }

local limit  = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local cost   = tonumber(ARGV[3])
local now    = tonumber(ARGV[4])
local ttl    = tonumber(ARGV[5])

local cur_start = math.floor(now / window) * window

local h = redis.call('HMGET', KEYS[1], 'cur', 'prev', 'start')
local cur   = tonumber(h[1]) or 0
local prev  = tonumber(h[2]) or 0
local start = tonumber(h[3]) or cur_start

if start ~= cur_start then
  if (cur_start - start) == window then
    prev = cur            -- we advanced exactly one window
  else
    prev = 0              -- gap: previous window is stale
  end
  cur = 0
  start = cur_start
end

-- weight the previous window by the fraction of it still inside the sliding view
local elapsed_into = now - cur_start
local weight = (window - elapsed_into) / window
local estimate = cur + prev * weight

local allowed = 0
if (estimate + cost) <= limit then
  cur = cur + cost
  allowed = 1
end

redis.call('HSET', KEYS[1], 'cur', cur, 'prev', prev, 'start', start)
redis.call('PEXPIRE', KEYS[1], ttl)

local used = estimate
if allowed == 1 then used = used + cost end
local remaining = math.max(0, limit - used)
local reset_ms = (cur_start + window) - now
return { allowed, tostring(remaining), reset_ms }
