// Command stubllm is a zero-cost fake LLM upstream for local end-to-end runs and
// the step-6 multi-node invariant test. It answers any request with an
// OpenAI-style chat-completion JSON body carrying a `usage` object, so the
// gateway's reconcile path (estimate → actual → settle) has real numbers to
// work with — without ever calling (or billing) a real provider.
package main

import (
	"encoding/json"
	"flag"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"
)

// response mirrors the subset of an OpenAI chat-completion the gateway reads.
// Only `usage` is load-bearing (cost.UsageFromResponse); the rest is realism.
type response struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Model   string   `json:"model"`
	Choices []choice `json:"choices"`
	Usage   usage    `json:"usage"`
}

type choice struct {
	Index        int     `json:"index"`
	Message      message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// request is the subset we read back to derive a realistic prompt-token count
// and to echo the caller's model.
type request struct {
	Model    string `json:"model"`
	Messages []struct {
		Content string `json:"content"`
	} `json:"messages"`
	Prompt string `json:"prompt"`
}

func main() {
	addr := flag.String("addr", ":9090", "listen address")
	completion := flag.Int("completion-tokens", 25, "fixed completion tokens reported per response")
	latency := flag.Duration("latency", 0, "artificial per-request latency, e.g. 20ms")
	flag.Parse()

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()

		var req request
		_ = json.Unmarshal(body, &req) // best-effort; unknown bodies still answer

		// Derive prompt tokens with the same ~4-chars/token heuristic the gateway
		// estimates with; fall back to the whole body so usage is never zero (a
		// zero-usage response would make the gateway skip reconciliation).
		chars := 0
		for _, m := range req.Messages {
			chars += utf8.RuneCountInString(m.Content)
		}
		chars += utf8.RuneCountInString(req.Prompt)
		if chars == 0 {
			chars = utf8.RuneCount(body)
		}
		prompt := chars / 4
		if prompt < 1 {
			prompt = 1
		}

		if *latency > 0 {
			time.Sleep(*latency)
		}

		model := req.Model
		if model == "" {
			model = "stub-model"
		}
		resp := response{
			ID:     "stub-" + strconv.FormatInt(time.Now().UnixNano(), 36),
			Object: "chat.completion",
			Model:  model,
			Choices: []choice{{
				Message:      message{Role: "assistant", Content: "stub response"},
				FinishReason: "stop",
			}},
			Usage: usage{
				PromptTokens:     prompt,
				CompletionTokens: *completion,
				TotalTokens:      prompt + *completion,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	log.Printf("stubllm listening on %s (completion_tokens=%d, latency=%s)", *addr, *completion, *latency)
	log.Fatal(http.ListenAndServe(*addr, nil))
}
