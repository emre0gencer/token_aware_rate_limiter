package cost

// Price is the per-1K-token cost of a model, split by input/output.
type Price struct {
	InputPer1K  float64 `yaml:"input_per_1k"`
	OutputPer1K float64 `yaml:"output_per_1k"`
}

// PriceTable maps model name -> Price. Loaded from config so prices change
// without a redeploy (ADR-011). A missing model falls back to Default.
type PriceTable struct {
	Models  map[string]Price `yaml:"models"`
	Default Price            `yaml:"default"`
}

// priceFor returns the model's Price, falling back to Default when unknown.
func (p PriceTable) priceFor(model string) Price {
	// TODO: return p.Models[model] when present, else p.Default.
	if price, ok := p.Models[model]; ok {
		return price
	}
	return p.Default
}

// Dollars converts token counts to a dollar cost for the given model.
func (p PriceTable) Dollars(model string, inputTokens, outputTokens int) float64 {
	// TODO (STEP 5): pr := priceFor(model); return
	//   float64(inputTokens)/1000.0*pr.InputPer1K +
	//   float64(outputTokens)/1000.0*pr.OutputPer1K
	pr := p.priceFor(model)

	return float64(inputTokens)/1000.0*pr.InputPer1K +
		float64(outputTokens)/1000.0*pr.OutputPer1K
}

// EstimateDollars prices a pre-flight Estimate (treats MaxTokens as output).
func (p PriceTable) EstimateDollars(e Estimate) float64 {
	// TODO: return p.Dollars(e.Model, e.PromptTokens, e.MaxTokens).
	return p.Dollars(e.Model, e.PromptTokens, e.MaxTokens)
}
