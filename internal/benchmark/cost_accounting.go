package benchmark

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"unicode"

	cachepkg "github.com/bianjiefilm/CacheSafety-Bench/internal/cache"
	"github.com/bianjiefilm/CacheSafety-Bench/internal/teacher"
)

type ModelTokenPricing struct {
	InputPer1MTokens  float64 `json:"input_per_1m_tokens"`
	OutputPer1MTokens float64 `json:"output_per_1m_tokens"`
}

type EmbeddingPricing struct {
	Per1MTokens float64 `json:"per_1m_tokens"`
}

type PricingConfig struct {
	Default   ModelTokenPricing            `json:"default"`
	Judge     ModelTokenPricing            `json:"judge"`
	Embedding EmbeddingPricing             `json:"embedding"`
	Models    map[string]ModelTokenPricing `json:"-"`
}

type pricingConfigJSON struct {
	Default   ModelTokenPricing `json:"default"`
	Judge     ModelTokenPricing `json:"judge"`
	Embedding EmbeddingPricing  `json:"embedding"`
}

func (p *PricingConfig) UnmarshalJSON(data []byte) error {
	var known pricingConfigJSON
	if err := json.Unmarshal(data, &known); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	p.Default = known.Default
	p.Judge = known.Judge
	p.Embedding = known.Embedding
	p.Models = map[string]ModelTokenPricing{}
	for key, value := range raw {
		switch key {
		case "default", "judge", "embedding":
			continue
		default:
			var price ModelTokenPricing
			if err := json.Unmarshal(value, &price); err != nil {
				return fmt.Errorf("decode pricing for %s: %w", key, err)
			}
			p.Models[key] = price
		}
	}
	return nil
}

func LoadPricingConfig(path string) (PricingConfig, error) {
	if strings.TrimSpace(path) == "" {
		return PricingConfig{}, nil
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return PricingConfig{}, nil
		}
		return PricingConfig{}, err
	}
	if len(strings.TrimSpace(string(bytes))) == 0 {
		return PricingConfig{}, nil
	}
	var cfg PricingConfig
	if err := json.Unmarshal(bytes, &cfg); err != nil {
		return PricingConfig{}, err
	}
	return cfg, nil
}

func (p PricingConfig) PriceForModel(alias string) ModelTokenPricing {
	if p.Models != nil {
		if price, ok := p.Models[alias]; ok {
			return price
		}
	}
	return p.Default
}

func (p *PricingConfig) ApplyOverrides(inputPricePer1M, outputPricePer1M, judgePricePer1M, embeddingPricePer1M float64) {
	if inputPricePer1M >= 0 {
		p.Default.InputPer1MTokens = inputPricePer1M
	}
	if outputPricePer1M >= 0 {
		p.Default.OutputPer1MTokens = outputPricePer1M
	}
	if judgePricePer1M >= 0 {
		p.Judge.InputPer1MTokens = judgePricePer1M
		p.Judge.OutputPer1MTokens = judgePricePer1M
	}
	if embeddingPricePer1M >= 0 {
		p.Embedding.Per1MTokens = embeddingPricePer1M
	}
}

type TokenCostAccount struct {
	PromptTokens                int     `json:"prompt_tokens,omitempty"`
	CompletionTokens            int     `json:"completion_tokens,omitempty"`
	TotalTokens                 int     `json:"total_tokens,omitempty"`
	CachedPromptTokensSaved     int     `json:"cached_prompt_tokens_saved,omitempty"`
	CachedCompletionTokensSaved int     `json:"cached_completion_tokens_saved,omitempty"`
	UpstreamCostUSD             float64 `json:"upstream_cost_usd,omitempty"`
	CachedCostUSDSaved          float64 `json:"cached_cost_usd_saved,omitempty"`
	EmbeddingCostUSD            float64 `json:"embedding_cost_usd,omitempty"`
	JudgeCostUSD                float64 `json:"judge_cost_usd,omitempty"`
	NetCostSavedUSD             float64 `json:"net_cost_saved_usd,omitempty"`
	TokenEstimationMode         string  `json:"token_estimation_mode,omitempty"`
}

func EstimateTokensApprox(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	var cjk, other int
	for _, r := range value {
		if unicode.IsSpace(r) {
			continue
		}
		if isCJKRune(r) {
			cjk++
			continue
		}
		other++
	}
	tokens := int(math.Ceil(float64(cjk)/1.5 + float64(other)/4.0))
	if tokens == 0 {
		return 1
	}
	return tokens
}

func isCJKRune(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) ||
		(r >= 0x3400 && r <= 0x4DBF) ||
		(r >= 0x20000 && r <= 0x2A6DF) ||
		(r >= 0x2A700 && r <= 0x2B73F) ||
		(r >= 0x2B740 && r <= 0x2B81F) ||
		(r >= 0x2B820 && r <= 0x2CEAF) ||
		(r >= 0xF900 && r <= 0xFAFF)
}

func tokenCost(price ModelTokenPricing, promptTokens int, completionTokens int) float64 {
	input := float64(promptTokens) * price.InputPer1MTokens / 1_000_000
	output := float64(completionTokens) * price.OutputPer1MTokens / 1_000_000
	return input + output
}

func embeddingCost(price EmbeddingPricing, tokens int) float64 {
	return float64(tokens) * price.Per1MTokens / 1_000_000
}

func responseUsage(resp cachepkg.Response) (int, int, int, bool) {
	total := resp.TotalTokens
	if total == 0 {
		total = resp.PromptTokens + resp.CompletionTokens
	}
	if total <= 0 {
		return 0, 0, 0, false
	}
	return resp.PromptTokens, resp.CompletionTokens, total, true
}

func BuildTokenCostAccount(item Case, result cachepkg.LookupResult, evaluation hitEvaluation, pricing PricingConfig, safeCacheHit bool, embeddingMode string) TokenCostAccount {
	prompt := cachepkg.PromptText(item.Request)
	answerForNoCache := result.Response.Text
	if evaluation.record.Input.FreshAnswer != "" {
		answerForNoCache = evaluation.record.Input.FreshAnswer
	} else if item.FreshAnswer != "" {
		answerForNoCache = item.FreshAnswer
	}

	promptTokens, completionTokens, totalTokens, providerUsage := responseUsage(result.Response)
	mode := "approximate"
	if providerUsage {
		mode = "provider_usage"
	}
	if !providerUsage {
		promptTokens = EstimateTokensApprox(prompt)
		completionTokens = EstimateTokensApprox(answerForNoCache)
		totalTokens = promptTokens + completionTokens
	}
	modelAlias := ModelAlias(item.Request.Model)
	upstreamCost := tokenCost(pricing.PriceForModel(modelAlias), promptTokens, completionTokens)

	var savedPromptTokens, savedCompletionTokens int
	var cachedCostSaved float64
	if safeCacheHit && result.Layer != cachepkg.LayerMiss {
		savedPromptTokens = promptTokens
		savedCompletionTokens = completionTokens
		cachedCostSaved = upstreamCost
	}

	var embedCost float64
	if shouldChargeEmbeddingCost(result, embeddingMode) {
		embedCost = embeddingCost(pricing.Embedding, EstimateTokensApprox(prompt))
	}

	var judgeCostValue float64
	if evaluation.judged {
		judgeCostValue = estimateJudgeCost(evaluation.record.Input, evaluation.record.Result, pricing.Judge)
	}
	netSaved := cachedCostSaved - embedCost - judgeCostValue
	return TokenCostAccount{
		PromptTokens:                promptTokens,
		CompletionTokens:            completionTokens,
		TotalTokens:                 totalTokens,
		CachedPromptTokensSaved:     savedPromptTokens,
		CachedCompletionTokensSaved: savedCompletionTokens,
		UpstreamCostUSD:             upstreamCost,
		CachedCostUSDSaved:          cachedCostSaved,
		EmbeddingCostUSD:            embedCost,
		JudgeCostUSD:                judgeCostValue,
		NetCostSavedUSD:             netSaved,
		TokenEstimationMode:         mode,
	}
}

func shouldChargeEmbeddingCost(result cachepkg.LookupResult, embeddingMode string) bool {
	if strings.TrimSpace(embeddingMode) == "" || embeddingMode == "fixture" || embeddingMode == "fake" {
		return false
	}
	if result.Layer == cachepkg.LayerExact || result.Layer == cachepkg.LayerCanonical {
		return false
	}
	return true
}

func estimateJudgeCost(input teacher.JudgeInput, result teacher.JudgeResult, price ModelTokenPricing) float64 {
	promptTokens := EstimateTokensApprox(teacher.JudgePrompt(input))
	payload, err := json.Marshal(result)
	if err != nil {
		payload = []byte(result.Reason)
	}
	completionTokens := EstimateTokensApprox(string(payload))
	return tokenCost(price, promptTokens, completionTokens)
}

func mergeTokenEstimationMode(current string, next string) string {
	current = strings.TrimSpace(current)
	next = strings.TrimSpace(next)
	if next == "" {
		return current
	}
	if current == "" {
		return next
	}
	if current == next {
		return current
	}
	return "mixed_provider_usage_approximate"
}

func applyTokenCost(metrics *Metrics, categoryMetric *CategoryMetric, cost TokenCostAccount) {
	metrics.TotalPromptTokens += cost.PromptTokens
	metrics.TotalCompletionTokens += cost.CompletionTokens
	metrics.TotalTokens += cost.TotalTokens
	metrics.CachedPromptTokensSaved += cost.CachedPromptTokensSaved
	metrics.CachedCompletionTokensSaved += cost.CachedCompletionTokensSaved
	metrics.CachedTokensSaved += cost.CachedPromptTokensSaved + cost.CachedCompletionTokensSaved
	metrics.GrossCostUSD += cost.UpstreamCostUSD
	metrics.CachedCostUSDSaved += cost.CachedCostUSDSaved
	metrics.EmbeddingCostUSD += cost.EmbeddingCostUSD
	metrics.JudgeCostUSD += cost.JudgeCostUSD
	metrics.NetCostSavedUSD += cost.NetCostSavedUSD
	metrics.TokenEstimationMode = mergeTokenEstimationMode(metrics.TokenEstimationMode, cost.TokenEstimationMode)

	categoryMetric.CachedTokensSaved += cost.CachedPromptTokensSaved + cost.CachedCompletionTokensSaved
	categoryMetric.CachedCostUSDSaved += cost.CachedCostUSDSaved
	categoryMetric.NetCostSavedUSD += cost.NetCostSavedUSD
}

func finalizeCostRates(metrics *Metrics) {
	if metrics.GrossCostUSD <= 0 {
		return
	}
	metrics.GrossSavingRate = metrics.CachedCostUSDSaved / metrics.GrossCostUSD
	metrics.NetSavingRate = metrics.NetCostSavedUSD / metrics.GrossCostUSD
}
