package cost

import "encoding/json"

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
	var env usageEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return Usage{}, false
	}
	if env.Usage.Total() == 0 {
		return Usage{}, false
	}
	return env.Usage, true
}

// TokenDelta is actual minus estimate, the amount to settle on the bucket.
func TokenDelta(est Estimate, actual Usage) float64 {
	return float64(actual.Total() - est.Total())
}
