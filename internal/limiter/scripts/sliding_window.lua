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

-- TODO: implement the weighted sliding window — the precise alternative to the
-- step-3 fixed window (which allows a 2x burst across the boundary).
--
-- HINT 1 — locate the current window: cur_start = math.floor(now/window)*window.
-- HINT 2 — read {cur, prev, start} via HMGET (default cur/prev = 0, start =
--          cur_start). If start advanced exactly one window, prev = old cur; if
--          it jumped further (a gap), prev = 0. Then cur = 0, start = cur_start.
-- HINT 3 — weight the previous window by the fraction still inside the view:
--          local weight   = (window - (now - cur_start)) / window
--          local estimate = cur + prev * weight
-- HINT 4 — admit iff estimate + cost <= limit; if so cur = cur + cost, allowed = 1.
-- HINT 5 — persist HSET cur/prev/start, then PEXPIRE KEYS[1] ttl.
-- HINT 6 — remaining = max(0, limit - used) where used includes cost when
--          admitted; reset_ms = (cur_start + window) - now. Return
--          { allowed, tostring(remaining), reset_ms }.

return { 0, "0", 0 } -- TODO
