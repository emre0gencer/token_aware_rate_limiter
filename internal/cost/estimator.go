// Package cost turns an LLM request/response into a meterable weight:
// a pre-flight token *estimate* (estimator), a tokens->dollars conversion
// (pricing), and a post-response *reconciliation* of estimate vs. actual.
//
// STEP 5 of the build order — the niche. Fill these in after the Lua token
// bucket (step 4) works; the gateway already calls into them, and cost_test.go
// already asserts the behavior (it will stay red until this is implemented).
package cost

// Estimate is the pre-flight worst-case token cost of a request, used as the
// optimistic debit before we proxy. We intentionally over-estimate (prompt +
// max_tokens) so we never under-protect under concurrency; reconciliation
// later refunds the difference.
type Estimate struct {
	Model        string
	PromptTokens int
	MaxTokens    int
}

func (e Estimate) Total() int { return e.PromptTokens + e.MaxTokens }

// chatRequest is the subset of the OpenAI/Anthropic-style body we care about.
// Unmarshal the request body into this to pull model + max_tokens + messages.
type chatRequest struct {
	Model     string `json:"model"`
	MaxTokens int    `json:"max_tokens"`
	Messages  []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
	Prompt string `json:"prompt"` // legacy completions
}

// EstimateFromBody parses a request body and approximates token usage.
//
// NOTE: use a cheap ~4-chars-per-token heuristic here. ADR-006 / the open
// questions in DECISIONS.md call out swapping in a real model-specific
// tokenizer (e.g. tiktoken) as a follow-up — keep this signature stable.
func EstimateFromBody(body []byte, defaultMaxTokens int) Estimate {
	// TODO (STEP 5):
	// HINT 1 — json.Unmarshal the body into a chatRequest, best-effort (ignore
	//          the error; unknown bodies still get estimated from raw length).
	// HINT 2 — count characters across every message Content plus the legacy
	//          Prompt (utf8.RuneCountInString). If that total is 0, fall back to
	//          utf8.RuneCount(body) so we never estimate zero cost.
	// HINT 3 — maxTok = req.MaxTokens, or defaultMaxTokens when the request omits
	//          it. Return Estimate{Model: req.Model, PromptTokens:
	//          approxTokens(chars), MaxTokens: maxTok}.
	// (Remember to re-add the "encoding/json" and "unicode/utf8" imports.)
	return Estimate{} // TODO
}

// approxTokens converts characters to tokens (~4 chars/token, min 1).
func approxTokens(chars int) int {
	// TODO: return max(1, chars/4).
	return 0 // TODO
}
