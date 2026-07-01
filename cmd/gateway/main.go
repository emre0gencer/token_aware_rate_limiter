// Command gateway is the entry point for the rate-limiting LLM proxy.
//
// STEP 1 of the build order (README §"Build order"): a *bare* reverse proxy.
// It forwards every request to one upstream and streams the response back —
// no identification, no limiting, no cost accounting yet. We get something
// that runs and is testable first, then layer the (already-built) limiter on
// top in later steps.
package main

import (
	"flag"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
)

func main() {
	// Later we'll replace these with the internal/config loader that already
	// exists — for a "bare" step, two flags keep it honest and dependency-free.
	addr := flag.String("addr", ":8080", "address the gateway listens on")
	upstream := flag.String("upstream", "", "upstream LLM base URL, e.g. https://api.openai.com")
	flag.Parse()

	if *upstream == "" {
		//prints and exits with a non-zero status.
		log.Fatal("missing required -upstream flag")
	}

	// url.Parse validates the target and splits it into scheme/host/path.
	target, err := url.Parse(*upstream)
	if err != nil {
		log.Fatalf("bad -upstream URL %q: %v", *upstream, err)
	}

	// NewSingleHostReverseProxy is the whole reverse proxy in one call: it
	// rewrites each incoming request onto `target` and streams the upstream
	// response back to the client unbuffered — which is exactly what we want
	// for token-by-token LLM responses.
	proxy := httputil.NewSingleHostReverseProxy(target)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s -> %s", r.Method, r.URL.Path, target.Host)
		proxy.ServeHTTP(w, r)
	})

	// Make the upstream see its own host in the Host header, not the client's.
	orig := proxy.Director
	proxy.Director = func(req *http.Request) {
		orig(req) // does the scheme/host/path rewrite
		req.Host = target.Host
	}

	log.Printf("gateway listening on %s, proxying to %s", *addr, target)
	log.Fatal(http.ListenAndServe(*addr, handler))
}
