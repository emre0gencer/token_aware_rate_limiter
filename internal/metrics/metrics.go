// Package metrics exposes lightweight counters via expvar (std lib, zero deps).
// Swapping to Prometheus is a stretch task; the call sites stay the same.
// The novel bit for this niche: we count *tokens* and *dollars*, not just hits.
package metrics

import (
	"expvar"
	"net/http"
)

var (
	Allowed        = expvar.NewInt("allowed")
	Denied         = expvar.NewInt("denied")
	StoreErrors    = expvar.NewInt("store_errors")
	TokensMetered  = expvar.NewInt("tokens_metered")
	DollarsMetered = expvar.NewFloat("dollars_metered")
	Reconciles     = expvar.NewInt("reconciles")
)

// Handler serves the metrics at /debug/vars-style JSON.
func Handler() http.Handler { return expvar.Handler() }
