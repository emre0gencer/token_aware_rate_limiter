// Package identify extracts the identifiers a request can be limited by.
// We capture all of API key, authenticated user, and source IP so that
// *layered* rules (per-key AND per-IP AND global) can each match the right one
// — the most-restrictive rule then governs (see ARCHITECTURE.md §3.4).
package identify

import (
	"net"
	"net/http"
	"strings"
)

// Identity holds every identifier we could pull from the request. Any field
// may be empty (e.g. an anonymous request has no APIKey/User).
type Identity struct {
	APIKey string
	User   string
	IP     string
}

// Identify pulls identifiers in priority order: API key, then JWT subject,
// then source IP. JWT parsing here is intentionally trivial (no signature
// verification) — auth is a separate concern; we only need a stable key.
func Identify(r *http.Request) Identity {
	id := Identity{
		APIKey: r.Header.Get("X-API-Key"),
		IP:     clientIP(r),
	}
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		id.User = subjectFromBearer(strings.TrimPrefix(auth, "Bearer "))
	}
	return id
}

// For returns the identifier matching a rule scope, and whether it's present.
// "global" always resolves to a shared key so a global rule applies to everyone.
func (i Identity) For(scope string) (string, bool) {
	switch scope {
	case "global":
		return "global", true
	case "api_key":
		return i.APIKey, i.APIKey != ""
	case "user":
		return i.User, i.User != ""
	case "ip":
		return i.IP, i.IP != ""
	default:
		return "", false
	}
}

// clientIP prefers the left-most X-Forwarded-For hop, falling back to RemoteAddr.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// subjectFromBearer extracts a stable client id from a token. Placeholder:
// returns the raw token. Swap for real JWT claim parsing when auth lands.
func subjectFromBearer(tok string) string {
	return tok
}
