package cost

// Usage is the provider's reported actual token consumption.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func (u Usage) Total() int {
	if u.TotalTokens > 0 {
		return u.TotalTokens
	}
	return u.PromptTokens + u.CompletionTokens
}

// usageEnvelope matches the `usage` object in OpenAI/Anthropic-style responses.
type usageEnvelope struct {
	Usage Usage `json:"usage"`
}

// UsageFromResponse extracts actual usage from a (buffered) response body.
// Returns ok=false for streaming/SSE bodies where usage isn't a single JSON
// object — the caller then skips reconciliation for that request.
func UsageFromResponse(body []byte) (Usage, bool) {
	// TODO (STEP 5):
	// HINT 1 — json.Unmarshal(body, &env) where env is a usageEnvelope; on error
	//          return (Usage{}, false).
	// HINT 2 — if env.Usage.Total() == 0 there's nothing usable (a streaming
	//          chunk, or no usage object) — return (Usage{}, false).
	// HINT 3 — otherwise return (env.Usage, true).
	// (Remember to re-add the "encoding/json" import.)
	return Usage{}, false // TODO
}

// TokenDelta is actual minus estimate, the amount to settle on the bucket.
func TokenDelta(est Estimate, actual Usage) float64 {
	// TODO: return float64(actual.Total() - est.Total()).
	return 0 // TODO
}
