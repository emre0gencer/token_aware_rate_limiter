// Package cost turns an LLM request/response into a meterable weight:
// a pre-flight token *estimate* (estimator), a tokens->dollars conversion
// (pricing), and a post-response *reconciliation* of estimate vs. actual.
package cost

import (
	"encoding/json"
	"unicode/utf8"
)

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
// NOTE: this uses a cheap ~4-chars-per-token heuristic. ADR-006 / the open
// questions in DECISIONS.md call out swapping in a real model-specific
// tokenizer (e.g. tiktoken) as a follow-up — the interface here stays the same.
func EstimateFromBody(body []byte, defaultMaxTokens int) Estimate {
	var req chatRequest
	_ = json.Unmarshal(body, &req) // best-effort; unknown bodies estimate from raw length

	var chars int
	for _, m := range req.Messages {
		chars += utf8.RuneCountInString(m.Content)
	}
	chars += utf8.RuneCountInString(req.Prompt)
	if chars == 0 {
		chars = utf8.RuneCount(body) // fallback: whole body
	}

	maxTok := req.MaxTokens
	if maxTok == 0 {
		maxTok = defaultMaxTokens
	}
	return Estimate{
		Model:        req.Model,
		PromptTokens: approxTokens(chars),
		MaxTokens:    maxTok,
	}
}

// approxTokens converts characters to tokens (~4 chars/token, min 1).
func approxTokens(chars int) int {
	t := chars / 4
	if t < 1 {
		t = 1
	}
	return t
}
