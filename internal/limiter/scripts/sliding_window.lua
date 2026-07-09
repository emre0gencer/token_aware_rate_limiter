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
local cur_start = math.floor(now/window)*window
-- HINT 2 — read {cur, prev, start} via HMGET (default cur/prev = 0, start =
--          cur_start). If start advanced exactly one window, prev = old cur; if
--          it jumped further (a gap), prev = 0. Then cur = 0, start = cur_start.
local data = redis.call("HMGET", KEYS[1], "cur", "prev", "start")
local cur = tonumber(data[1]) or 0
local prev = tonumber(data[2]) or 0
local start = tonumber(data[3]) or cur_start

if start ~= cur_start then
	if start == cur_start - window then
		prev = cur
	else
		prev = 0
	end

	cur = 0
	start = cur_start
end
-- HINT 3 — weight the previous window by the fraction still inside the view:
--          local weight   = (window - (now - cur_start)) / window
--          local estimate = cur + prev * weight
local elapsed = now - cur_start
local weight = (window - elapsed) / window

local estimate = cur + prev * weight
-- HINT 4 — admit iff estimate + cost <= limit; if so cur = cur + cost, allowed = 1.
local allowed = 0
local used = estimate

if estimate + cost <= limit then
	cur = cur + cost
	allowed = 1
	used = estimate + cost
end
-- HINT 5 — persist HSET cur/prev/start, then PEXPIRE KEYS[1] ttl.
redis.call("HSET", KEYS[1],
	"cur", cur,
	"prev", prev,
	"start", start
)

redis.call("PEXPIRE", KEYS[1], ttl)
-- HINT 6 — remaining = max(0, limit - used) where used includes cost when
--          admitted; reset_ms = (cur_start + window) - now. Return
--          { allowed, tostring(remaining), reset_ms }.
local remaining = limit - used
if remaining < 0 then
	remaining = 0
end

local reset_ms = (cur_start + window) - now

return { allowed, tostring(remaining), reset_ms }
