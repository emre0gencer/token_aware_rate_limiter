package cost

import "testing"

func TestEstimateFromBody(t *testing.T) {
	body := []byte(`{"model":"gpt-4o","max_tokens":256,"messages":[{"role":"user","content":"abcdefgh"}]}`)
	est := EstimateFromBody(body, 512)
	if est.Model != "gpt-4o" {
		t.Fatalf("model = %q, want gpt-4o", est.Model)
	}
	if est.MaxTokens != 256 {
		t.Fatalf("max tokens = %d, want 256", est.MaxTokens)
	}
	// "abcdefgh" = 8 chars / 4 = 2 prompt tokens
	if est.PromptTokens != 2 {
		t.Fatalf("prompt tokens = %d, want 2", est.PromptTokens)
	}
	if est.Total() != 258 {
		t.Fatalf("total = %d, want 258", est.Total())
	}
}

func TestEstimateDefaultMaxTokens(t *testing.T) {
	body := []byte(`{"model":"x","messages":[{"role":"user","content":"hi there"}]}`)
	est := EstimateFromBody(body, 100)
	if est.MaxTokens != 100 {
		t.Fatalf("max tokens = %d, want default 100", est.MaxTokens)
	}
}

func TestPricingDollars(t *testing.T) {
	pt := PriceTable{
		Default: Price{InputPer1K: 0.5, OutputPer1K: 1.5},
		Models:  map[string]Price{"gpt-4o": {InputPer1K: 2.5, OutputPer1K: 10}},
	}
	// 2000 in @2.5/1k = 5.0 ; 1000 out @10/1k = 10.0 => 15.0
	if got := pt.Dollars("gpt-4o", 2000, 1000); got != 15.0 {
		t.Fatalf("dollars = %v, want 15.0", got)
	}
	// unknown model uses default: 1000 in @0.5 = 0.5
	if got := pt.Dollars("unknown", 1000, 0); got != 0.5 {
		t.Fatalf("default dollars = %v, want 0.5", got)
	}
}

func TestUsageAndDelta(t *testing.T) {
	resp := []byte(`{"id":"x","usage":{"prompt_tokens":10,"completion_tokens":40,"total_tokens":50}}`)
	u, ok := UsageFromResponse(resp)
	if !ok || u.Total() != 50 {
		t.Fatalf("usage = %+v ok=%v, want total 50", u, ok)
	}
	est := Estimate{PromptTokens: 10, MaxTokens: 512} // estimated 522
	if d := TokenDelta(est, u); d != float64(50-522) {
		t.Fatalf("delta = %v, want %v", d, 50-522)
	}

	if _, ok := UsageFromResponse([]byte(`data: streaming chunk`)); ok {
		t.Fatal("streaming body should not parse as usage")
	}
}
