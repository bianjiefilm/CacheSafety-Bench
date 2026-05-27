package benchmark

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	pricingSourceMissing = "missing"
	pricingSourceFile    = "file"
	pricingSourceEnv     = "env"
	pricingSourceFileEnv = "file+env"
)

type RecomputeCostsInput struct {
	MetricsPath     string
	DecisionLogPath string
	RequestLogDB    string
	PricingPath     string
	OutputPath      string
	ReportPath      string
	Getenv          func(string) string
}

type PricingSourceInfo struct {
	Source       string   `json:"source"`
	EnvOverrides []string `json:"env_overrides,omitempty"`
}

type PricedCostMetrics struct {
	Metrics
	RequestHitRate            float64 `json:"request_hit_rate"`
	TokenSavingRate           float64 `json:"token_saving_rate"`
	BadHitCount               int     `json:"bad_hit_count"`
	ExpectedRefusalLeakCount  int     `json:"expected_refusal_leak_count"`
	HardNegativeSafeHitCount  int     `json:"hard_negative_safe_hit_count"`
	ExactCacheContributionUSD float64 `json:"exact_cache_contribution_usd"`
	JudgeCostShare            float64 `json:"judge_cost_share"`
	EmbeddingCostShare        float64 `json:"embedding_cost_share"`
	PricingSource             string  `json:"pricing_source"`
	PricingMissing            bool    `json:"pricing_missing"`
	PricingComplete           bool    `json:"pricing_complete"`
	EarlyPositiveSignal       bool    `json:"early_positive_signal"`
	CostRecomputeMode         string  `json:"cost_recompute_mode"`
	PricingNote               string  `json:"pricing_note,omitempty"`
}

func RecomputeCosts(input RecomputeCostsInput) (PricedCostMetrics, string, error) {
	if strings.TrimSpace(input.MetricsPath) == "" {
		return PricedCostMetrics{}, "", fmt.Errorf("metrics path is required")
	}
	if strings.TrimSpace(input.DecisionLogPath) == "" {
		return PricedCostMetrics{}, "", fmt.Errorf("decision log path is required")
	}
	metrics, err := loadMetricsFile(input.MetricsPath)
	if err != nil {
		return PricedCostMetrics{}, "", err
	}
	records, err := LoadDecisionLog(input.DecisionLogPath)
	if err != nil {
		return PricedCostMetrics{}, "", err
	}
	if metrics.Total > 0 && metrics.Total != len(records) {
		return PricedCostMetrics{}, "", fmt.Errorf("metrics total %d does not match decision log total %d", metrics.Total, len(records))
	}
	pricing, sourceInfo, err := LoadPricingConfigWithEnv(input.PricingPath, input.Getenv)
	if err != nil {
		return PricedCostMetrics{}, "", err
	}
	result := buildPricedCostMetrics(metrics, records, pricing, sourceInfo)
	markdown := RenderPricedCostReport(result)
	if strings.TrimSpace(input.OutputPath) != "" {
		if err := writeJSONFile(input.OutputPath, result); err != nil {
			return result, markdown, err
		}
	}
	if strings.TrimSpace(input.ReportPath) != "" {
		if err := os.MkdirAll(filepath.Dir(input.ReportPath), 0o755); err != nil {
			return result, markdown, err
		}
		if err := os.WriteFile(input.ReportPath, []byte(markdown), 0o644); err != nil {
			return result, markdown, err
		}
	}
	return result, markdown, nil
}

func LoadPricingConfigWithEnv(path string, getenv func(string) string) (PricingConfig, PricingSourceInfo, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	cfg, err := LoadPricingConfig(path)
	if err != nil {
		return PricingConfig{}, PricingSourceInfo{}, err
	}
	filePresent := strings.TrimSpace(path) != "" && fileExists(path)
	envOverrides, err := applyPricingEnvOverrides(&cfg, getenv)
	if err != nil {
		return PricingConfig{}, PricingSourceInfo{}, err
	}
	source := pricingSourceMissing
	switch {
	case filePresent && len(envOverrides) > 0:
		source = pricingSourceFileEnv
	case filePresent:
		source = pricingSourceFile
	case len(envOverrides) > 0:
		source = pricingSourceEnv
	}
	return cfg, PricingSourceInfo{Source: source, EnvOverrides: envOverrides}, nil
}

func applyPricingEnvOverrides(cfg *PricingConfig, getenv func(string) string) ([]string, error) {
	type envTarget struct {
		names []string
		set   func(float64)
	}
	targets := []envTarget{
		{
			names: []string{"MODEL_API_DEFAULT_INPUT_PER_1M_TOKENS", "VOLCENGINE_DEFAULT_INPUT_PER_1M_TOKENS"},
			set:   func(v float64) { cfg.Default.InputPer1MTokens = v },
		},
		{
			names: []string{"MODEL_API_DEFAULT_OUTPUT_PER_1M_TOKENS", "VOLCENGINE_DEFAULT_OUTPUT_PER_1M_TOKENS"},
			set:   func(v float64) { cfg.Default.OutputPer1MTokens = v },
		},
		{
			names: []string{"MODEL_API_JUDGE_INPUT_PER_1M_TOKENS", "VOLCENGINE_JUDGE_INPUT_PER_1M_TOKENS"},
			set:   func(v float64) { cfg.Judge.InputPer1MTokens = v },
		},
		{
			names: []string{"MODEL_API_JUDGE_OUTPUT_PER_1M_TOKENS", "VOLCENGINE_JUDGE_OUTPUT_PER_1M_TOKENS"},
			set:   func(v float64) { cfg.Judge.OutputPer1MTokens = v },
		},
		{
			names: []string{"MODEL_API_EMBEDDING_PER_1M_TOKENS", "VOLCENGINE_EMBEDDING_PER_1M_TOKENS"},
			set:   func(v float64) { cfg.Embedding.Per1MTokens = v },
		},
	}
	var applied []string
	for _, target := range targets {
		name, value := firstEnvValue(getenv, target.names)
		if name == "" {
			continue
		}
		price, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
		target.set(price)
		applied = append(applied, name)
	}
	return applied, nil
}

func firstEnvValue(getenv func(string) string, names []string) (string, string) {
	for _, name := range names {
		if value := strings.TrimSpace(getenv(name)); value != "" {
			return name, value
		}
	}
	return "", ""
}

func fileExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func buildPricedCostMetrics(base Metrics, records []DecisionRecord, pricing PricingConfig, source PricingSourceInfo) PricedCostMetrics {
	metrics := base
	metrics.Total = len(records)
	metrics.LayerContribution = map[string]int{
		"exact":     0,
		"canonical": 0,
		"semantic":  0,
		"miss":      0,
	}
	metrics.TotalPromptTokens = 0
	metrics.TotalCompletionTokens = 0
	metrics.TotalTokens = 0
	metrics.CachedTokensSaved = 0
	metrics.CachedPromptTokensSaved = 0
	metrics.CachedCompletionTokensSaved = 0
	metrics.GrossCostUSD = 0
	metrics.CachedCostUSDSaved = 0
	metrics.JudgeCostUSD = 0
	metrics.EmbeddingCostUSD = 0
	metrics.NetCostSavedUSD = 0
	metrics.GrossSavingRate = 0
	metrics.NetSavingRate = 0
	metrics.CategoryMetrics = map[string]CategoryMetric{}

	badHitCount := 0
	safeHitCount := 0
	exactContribution := 0.0
	tokenMode := ""
	for _, record := range records {
		layer := defaultString(record.CacheLayer, "unknown")
		metrics.LayerContribution[layer]++
		if record.BadHit {
			badHitCount++
		}
		if record.JudgeCacheSafe != nil && *record.JudgeCacheSafe && !record.BadHit {
			safeHitCount++
		}
		cost := recomputeDecisionCost(record, pricing)
		metrics.TotalPromptTokens += cost.PromptTokens
		metrics.TotalCompletionTokens += cost.CompletionTokens
		metrics.TotalTokens += cost.TotalTokens
		metrics.CachedPromptTokensSaved += cost.CachedPromptTokensSaved
		metrics.CachedCompletionTokensSaved += cost.CachedCompletionTokensSaved
		metrics.CachedTokensSaved += cost.CachedPromptTokensSaved + cost.CachedCompletionTokensSaved
		metrics.GrossCostUSD += cost.UpstreamCostUSD
		metrics.CachedCostUSDSaved += cost.CachedCostUSDSaved
		metrics.JudgeCostUSD += cost.JudgeCostUSD
		metrics.EmbeddingCostUSD += cost.EmbeddingCostUSD
		metrics.NetCostSavedUSD += cost.NetCostSavedUSD
		tokenMode = mergeTokenEstimationMode(tokenMode, cost.TokenEstimationMode)
		if layer == "exact" {
			exactContribution += cost.CachedCostUSDSaved
		}
		category := defaultString(record.Category, "unknown")
		categoryMetric := metrics.CategoryMetrics[category]
		categoryMetric.Total++
		if record.SemanticCandidateFound {
			categoryMetric.SemanticCandidates++
		}
		if record.JudgeCacheSafe != nil && *record.JudgeCacheSafe && !record.BadHit {
			categoryMetric.SemanticJudgedSafe++
		}
		if record.RefusedReason != "" {
			categoryMetric.SemanticRejectedBySafety++
		}
		categoryMetric.SafeCostSaved += record.SafeCostSaved
		categoryMetric.CachedTokensSaved += cost.CachedPromptTokensSaved + cost.CachedCompletionTokensSaved
		categoryMetric.CachedCostUSDSaved += cost.CachedCostUSDSaved
		categoryMetric.NetCostSavedUSD += cost.NetCostSavedUSD
		if record.BadHit {
			categoryMetric.badHits++
		}
		metrics.CategoryMetrics[category] = categoryMetric
	}
	if metrics.Total > 0 {
		hits := metrics.Total - metrics.LayerContribution["miss"]
		metrics.HitRate = float64(hits) / float64(metrics.Total)
		metrics.SafeHitRate = float64(safeHitCount) / float64(metrics.Total)
		metrics.BadHitRate = float64(badHitCount) / float64(metrics.Total)
	}
	metrics.BadHitExamplesCount = badHitCount
	if tokenMode == "" {
		metrics.TokenEstimationMode = "decision_log_recompute"
	} else {
		metrics.TokenEstimationMode = "decision_log_recompute_" + tokenMode
	}
	finalizeCostRates(&metrics)
	for category, categoryMetric := range metrics.CategoryMetrics {
		if categoryMetric.Total > 0 {
			categoryMetric.BadHitRate = float64(categoryMetric.badHits) / float64(categoryMetric.Total)
		}
		metrics.CategoryMetrics[category] = categoryMetric
	}
	pricingMissing := pricingMissingForRecords(pricing, records)
	result := PricedCostMetrics{
		Metrics:                   metrics,
		RequestHitRate:            metrics.HitRate,
		TokenSavingRate:           ratioInt(metrics.CachedTokensSaved, metrics.TotalTokens),
		BadHitCount:               badHitCount,
		ExpectedRefusalLeakCount:  countExpectedRefusalLeaks(records),
		HardNegativeSafeHitCount:  semanticHardNegativeSafeHits(records),
		ExactCacheContributionUSD: exactContribution,
		JudgeCostShare:            costShare(metrics.JudgeCostUSD, metrics.GrossCostUSD),
		EmbeddingCostShare:        costShare(metrics.EmbeddingCostUSD, metrics.GrossCostUSD),
		PricingSource:             source.Source,
		PricingMissing:            pricingMissing,
		PricingComplete:           !pricingMissing,
		CostRecomputeMode:         "offline_decision_log",
	}
	if pricingMissing {
		result.PricingNote = "pricing_missing=true; net_saving_rate is not business-actionable"
	}
	result.EarlyPositiveSignal = result.PricingComplete &&
		result.BadHitCount == 0 &&
		metrics.JudgeErrorCount == 0 &&
		metrics.UnknownCount == 0 &&
		metrics.NetSavingRate >= 0.15
	return result
}

func recomputeDecisionCost(record DecisionRecord, pricing PricingConfig) TokenCostAccount {
	promptTokens := record.PromptTokens
	completionTokens := record.CompletionTokens
	totalTokens := record.TotalTokens
	if totalTokens == 0 {
		totalTokens = promptTokens + completionTokens
	}
	price := pricing.PriceForModel(record.ModelAlias)
	upstreamCost := tokenCost(price, promptTokens, completionTokens)
	cachedCostSaved := tokenCost(price, record.CachedPromptTokensSaved, record.CachedCompletionTokensSaved)
	judgeCostValue := estimateDecisionLogJudgeCost(record, pricing.Judge)
	embedCost := estimateDecisionLogEmbeddingCost(record, pricing.Embedding)
	return TokenCostAccount{
		PromptTokens:                promptTokens,
		CompletionTokens:            completionTokens,
		TotalTokens:                 totalTokens,
		CachedPromptTokensSaved:     record.CachedPromptTokensSaved,
		CachedCompletionTokensSaved: record.CachedCompletionTokensSaved,
		UpstreamCostUSD:             upstreamCost,
		CachedCostUSDSaved:          cachedCostSaved,
		JudgeCostUSD:                judgeCostValue,
		EmbeddingCostUSD:            embedCost,
		NetCostSavedUSD:             cachedCostSaved - judgeCostValue - embedCost,
		TokenEstimationMode:         defaultString(record.TokenEstimationMode, "decision_log"),
	}
}

func estimateDecisionLogJudgeCost(record DecisionRecord, price ModelTokenPricing) float64 {
	if !decisionRecordJudged(record) {
		return 0
	}
	promptTokens := EstimateTokensApprox(record.RequestPreview) +
		EstimateTokensApprox(record.CandidatePromptPreview) +
		EstimateTokensApprox(record.CandidateAnswerPreview) +
		EstimateTokensApprox(record.FreshAnswerPreview) +
		128
	completionTokens := EstimateTokensApprox(record.JudgeReason)
	if record.JudgeError != "" {
		completionTokens += EstimateTokensApprox(record.JudgeError)
	}
	if completionTokens == 0 {
		completionTokens = 16
	}
	return tokenCost(price, promptTokens, completionTokens)
}

func decisionRecordJudged(record DecisionRecord) bool {
	return record.JudgeCacheSafe != nil ||
		strings.TrimSpace(record.JudgeError) != "" ||
		strings.TrimSpace(record.JudgeReason) != "" ||
		record.CachedScore != 0 ||
		record.FreshScore != 0 ||
		record.JudgeDelta != 0
}

func estimateDecisionLogEmbeddingCost(record DecisionRecord, price EmbeddingPricing) float64 {
	mode := strings.TrimSpace(record.EmbeddingMode)
	if mode == "" || mode == "fixture" || mode == "fake" {
		return 0
	}
	layer := strings.TrimSpace(record.CacheLayer)
	if layer == "exact" || layer == "canonical" {
		return 0
	}
	tokens := record.PromptTokens
	if tokens == 0 {
		tokens = EstimateTokensApprox(record.RequestPreview)
	}
	return embeddingCost(price, tokens)
}

func pricingMissingForRecords(pricing PricingConfig, records []DecisionRecord) bool {
	if pricing.Default.InputPer1MTokens <= 0 || pricing.Default.OutputPer1MTokens <= 0 {
		return true
	}
	if pricing.Judge.InputPer1MTokens <= 0 || pricing.Judge.OutputPer1MTokens <= 0 {
		return true
	}
	if pricing.Embedding.Per1MTokens <= 0 {
		return true
	}
	for _, alias := range usedModelAliases(records) {
		price := pricing.PriceForModel(alias)
		if price.InputPer1MTokens <= 0 || price.OutputPer1MTokens <= 0 {
			return true
		}
	}
	return false
}

func usedModelAliases(records []DecisionRecord) []string {
	seen := map[string]bool{}
	for _, record := range records {
		alias := strings.TrimSpace(record.ModelAlias)
		if alias == "" {
			continue
		}
		seen[alias] = true
	}
	out := make([]string, 0, len(seen))
	for alias := range seen {
		out = append(out, alias)
	}
	sort.Strings(out)
	return out
}

func RenderPricedCostReport(result PricedCostMetrics) string {
	metrics := result.Metrics
	var b strings.Builder
	b.WriteString("# Shadow Replay v2 Priced Cost Report\n\n")
	b.WriteString("## Summary\n")
	b.WriteString(fmt.Sprintf("- total replay requests: `%d`\n", metrics.Total))
	b.WriteString(fmt.Sprintf("- request_hit_rate: `%.4f`\n", result.RequestHitRate))
	b.WriteString(fmt.Sprintf("- exact: `%d`\n", metrics.LayerContribution["exact"]))
	b.WriteString(fmt.Sprintf("- semantic: `%d`\n", metrics.LayerContribution["semantic"]))
	b.WriteString(fmt.Sprintf("- miss: `%d`\n", metrics.LayerContribution["miss"]))
	b.WriteString(fmt.Sprintf("- bad_hit_count: `%d`\n", result.BadHitCount))
	b.WriteString(fmt.Sprintf("- judge_error_count: `%d`\n", metrics.JudgeErrorCount))
	b.WriteString(fmt.Sprintf("- unknown_count: `%d`\n", metrics.UnknownCount))
	b.WriteString(fmt.Sprintf("- expected_refusal_leak_count: `%d`\n", result.ExpectedRefusalLeakCount))
	b.WriteString(fmt.Sprintf("- hard_negative_safe_hit_count: `%d`\n", result.HardNegativeSafeHitCount))
	b.WriteString(fmt.Sprintf("- pricing_source: `%s`\n", defaultString(result.PricingSource, pricingSourceMissing)))
	b.WriteString(fmt.Sprintf("- pricing_missing: `%t`\n", result.PricingMissing))
	b.WriteString(fmt.Sprintf("- cost_recompute_mode: `%s`\n", result.CostRecomputeMode))
	if result.PricingMissing {
		b.WriteString("- pricing_note: `net_saving_rate is not business-actionable until real non-zero default, judge, and embedding prices are configured`\n")
	}

	b.WriteString("\n## Token Savings\n")
	b.WriteString(fmt.Sprintf("- total_prompt_tokens: `%d`\n", metrics.TotalPromptTokens))
	b.WriteString(fmt.Sprintf("- total_completion_tokens: `%d`\n", metrics.TotalCompletionTokens))
	b.WriteString(fmt.Sprintf("- total_tokens: `%d`\n", metrics.TotalTokens))
	b.WriteString(fmt.Sprintf("- cached_tokens_saved: `%d`\n", metrics.CachedTokensSaved))
	b.WriteString(fmt.Sprintf("- cached_prompt_tokens_saved: `%d`\n", metrics.CachedPromptTokensSaved))
	b.WriteString(fmt.Sprintf("- cached_completion_tokens_saved: `%d`\n", metrics.CachedCompletionTokensSaved))
	b.WriteString(fmt.Sprintf("- token_saving_rate: `%.4f`\n", result.TokenSavingRate))

	b.WriteString("\n## Cost Savings\n")
	b.WriteString(fmt.Sprintf("- gross_cost_usd: `%.8f`\n", metrics.GrossCostUSD))
	b.WriteString(fmt.Sprintf("- cached_cost_usd_saved: `%.8f`\n", metrics.CachedCostUSDSaved))
	b.WriteString(fmt.Sprintf("- judge_cost_usd: `%.8f`\n", metrics.JudgeCostUSD))
	b.WriteString(fmt.Sprintf("- embedding_cost_usd: `%.8f`\n", metrics.EmbeddingCostUSD))
	b.WriteString(fmt.Sprintf("- net_cost_saved_usd: `%.8f`\n", metrics.NetCostSavedUSD))
	b.WriteString(fmt.Sprintf("- gross_saving_rate: `%.4f`\n", metrics.GrossSavingRate))
	b.WriteString(fmt.Sprintf("- net_saving_rate: `%.4f`\n", metrics.NetSavingRate))
	b.WriteString(fmt.Sprintf("- judge_cost_share: `%.4f`\n", result.JudgeCostShare))
	b.WriteString(fmt.Sprintf("- embedding_cost_share: `%.4f`\n", result.EmbeddingCostShare))
	b.WriteString(fmt.Sprintf("- exact_cache_contribution_usd: `%.8f`\n", result.ExactCacheContributionUSD))

	b.WriteString("\n## Exact Cache Contribution\n")
	b.WriteString(fmt.Sprintf("- exact requests: `%d`\n", metrics.LayerContribution["exact"]))
	b.WriteString(fmt.Sprintf("- exact cached cost saved: `%.8f`\n", result.ExactCacheContributionUSD))
	b.WriteString(fmt.Sprintf("- exact contribution share: `%.4f`\n", safeDiv(result.ExactCacheContributionUSD, metrics.CachedCostUSDSaved)))

	b.WriteString("\n## Early Positive Signal\n")
	b.WriteString(fmt.Sprintf("- early_positive_signal: `%t`\n", result.EarlyPositiveSignal))
	b.WriteString("- criteria: `bad_hit_count=0`, `judge_error_count=0`, `unknown_count=0`, `net_saving_rate>=0.15`, `pricing_missing=false`\n")
	if result.EarlyPositiveSignal {
		b.WriteString("- expand_collection_allowed: `YES`\n")
	} else {
		b.WriteString("- expand_collection_allowed: `NO`\n")
	}
	return b.String()
}

func safeDiv(numerator float64, denominator float64) float64 {
	if math.Abs(denominator) < 1e-12 {
		return 0
	}
	return numerator / denominator
}
