package benchmark

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadPricingConfigWithEnvOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pricing.json")
	require.NoError(t, os.WriteFile(path, []byte(`{
		"default":{"input_per_1m_tokens":0,"output_per_1m_tokens":0},
		"judge":{"input_per_1m_tokens":0,"output_per_1m_tokens":0},
		"embedding":{"per_1m_tokens":0}
	}`), 0o644))

	got, source, err := LoadPricingConfigWithEnv(path, testEnvLookup(map[string]string{
		"MODEL_API_DEFAULT_INPUT_PER_1M_TOKENS":  "1.25",
		"MODEL_API_DEFAULT_OUTPUT_PER_1M_TOKENS": "2.5",
		"MODEL_API_JUDGE_INPUT_PER_1M_TOKENS":    "3.75",
		"MODEL_API_JUDGE_OUTPUT_PER_1M_TOKENS":   "4.25",
		"MODEL_API_EMBEDDING_PER_1M_TOKENS":      "0.5",
	}))

	require.NoError(t, err)
	require.Equal(t, pricingSourceFileEnv, source.Source)
	require.ElementsMatch(t, []string{
		"MODEL_API_DEFAULT_INPUT_PER_1M_TOKENS",
		"MODEL_API_DEFAULT_OUTPUT_PER_1M_TOKENS",
		"MODEL_API_JUDGE_INPUT_PER_1M_TOKENS",
		"MODEL_API_JUDGE_OUTPUT_PER_1M_TOKENS",
		"MODEL_API_EMBEDDING_PER_1M_TOKENS",
	}, source.EnvOverrides)
	require.Equal(t, 1.25, got.Default.InputPer1MTokens)
	require.Equal(t, 2.5, got.Default.OutputPer1MTokens)
	require.Equal(t, 3.75, got.Judge.InputPer1MTokens)
	require.Equal(t, 4.25, got.Judge.OutputPer1MTokens)
	require.Equal(t, 0.5, got.Embedding.Per1MTokens)
}

func TestPricingMissingWhenPricesAreZero(t *testing.T) {
	records := []DecisionRecord{testCostDecision("exact", 100, 50)}
	got := buildPricedCostMetrics(Metrics{Total: 1}, records, PricingConfig{}, PricingSourceInfo{Source: pricingSourceMissing})

	require.True(t, got.PricingMissing)
	require.False(t, got.PricingComplete)
	require.False(t, got.EarlyPositiveSignal)
	require.Equal(t, 0.0, got.GrossCostUSD)
	require.Contains(t, got.PricingNote, "not business-actionable")
}

func TestRecomputeCostsDoesNotRequireProviders(t *testing.T) {
	dir := t.TempDir()
	metricsPath := filepath.Join(dir, "metrics.json")
	decisionsPath := filepath.Join(dir, "decisions.jsonl")
	outputPath := filepath.Join(dir, "priced.json")
	reportPath := filepath.Join(dir, "report.md")
	require.NoError(t, writeJSONFile(metricsPath, Metrics{Total: 1}))
	require.NoError(t, WriteDecisionLog(decisionsPath, []DecisionRecord{testCostDecision("exact", 100, 50)}))

	got, markdown, err := RecomputeCosts(RecomputeCostsInput{
		MetricsPath:     metricsPath,
		DecisionLogPath: decisionsPath,
		OutputPath:      outputPath,
		ReportPath:      reportPath,
		Getenv: testEnvLookup(map[string]string{
			"MODEL_API_DEFAULT_INPUT_PER_1M_TOKENS":  "10",
			"MODEL_API_DEFAULT_OUTPUT_PER_1M_TOKENS": "20",
			"MODEL_API_JUDGE_INPUT_PER_1M_TOKENS":    "1",
			"MODEL_API_JUDGE_OUTPUT_PER_1M_TOKENS":   "1",
			"MODEL_API_EMBEDDING_PER_1M_TOKENS":      "0.1",
		}),
	})

	require.NoError(t, err)
	require.FileExists(t, outputPath)
	require.FileExists(t, reportPath)
	require.Equal(t, pricingSourceEnv, got.PricingSource)
	require.False(t, got.PricingMissing)
	require.Contains(t, markdown, "offline_decision_log")
}

func TestRecomputeCostsNetSavingRate(t *testing.T) {
	records := []DecisionRecord{testCostDecision("exact", 100, 50)}
	pricing := PricingConfig{
		Default:   ModelTokenPricing{InputPer1MTokens: 10, OutputPer1MTokens: 20},
		Judge:     ModelTokenPricing{InputPer1MTokens: 1, OutputPer1MTokens: 1},
		Embedding: EmbeddingPricing{Per1MTokens: 1},
	}

	got := buildPricedCostMetrics(Metrics{Total: 1}, records, pricing, PricingSourceInfo{Source: pricingSourceFile})

	require.InDelta(t, 0.002, got.GrossCostUSD, 1e-12)
	require.InDelta(t, 0.002, got.CachedCostUSDSaved, 1e-12)
	require.InDelta(t, 0.002, got.NetCostSavedUSD, 1e-12)
	require.Equal(t, 1.0, got.GrossSavingRate)
	require.Equal(t, 1.0, got.NetSavingRate)
	require.False(t, got.PricingMissing)
}

func TestJudgeAndEmbeddingCostsLowerRecomputedNetSaving(t *testing.T) {
	safe := true
	record := testCostDecision("semantic", 100, 100)
	record.EmbeddingMode = "hash"
	record.SemanticCandidateFound = true
	record.JudgeCacheSafe = &safe
	record.JudgeReason = "safe for reuse"
	record.RequestPreview = "Explain cache safety."
	record.CandidatePromptPreview = "What is cache safety?"
	record.CandidateAnswerPreview = "Cache safety avoids bad hits."
	record.FreshAnswerPreview = "Cache safety avoids bad hits."
	records := []DecisionRecord{record}
	withoutOverhead := buildPricedCostMetrics(Metrics{Total: 1}, records, PricingConfig{
		Default: ModelTokenPricing{InputPer1MTokens: 10, OutputPer1MTokens: 20},
	}, PricingSourceInfo{Source: pricingSourceFile})
	withOverhead := buildPricedCostMetrics(Metrics{Total: 1}, records, PricingConfig{
		Default:   ModelTokenPricing{InputPer1MTokens: 10, OutputPer1MTokens: 20},
		Judge:     ModelTokenPricing{InputPer1MTokens: 100, OutputPer1MTokens: 100},
		Embedding: EmbeddingPricing{Per1MTokens: 100},
	}, PricingSourceInfo{Source: pricingSourceFile})

	require.Greater(t, withOverhead.JudgeCostUSD, 0.0)
	require.Greater(t, withOverhead.EmbeddingCostUSD, 0.0)
	require.Less(t, withOverhead.NetCostSavedUSD, withoutOverhead.NetCostSavedUSD)
}

func TestZeroPricingReportIsNotActionable(t *testing.T) {
	got := buildPricedCostMetrics(Metrics{Total: 1}, []DecisionRecord{testCostDecision("exact", 100, 50)}, PricingConfig{}, PricingSourceInfo{Source: pricingSourceFile})

	markdown := RenderPricedCostReport(got)

	require.True(t, got.PricingMissing)
	require.False(t, got.EarlyPositiveSignal)
	require.Contains(t, markdown, "pricing_missing: `true`")
	require.True(t, strings.Contains(markdown, "not business-actionable"))
}

func testCostDecision(layer string, promptTokens int, completionTokens int) DecisionRecord {
	return DecisionRecord{
		SampleID:                    "sample-1",
		Category:                    "general_explanation",
		ExpectedRisk:                "low",
		ExpectedCacheBehavior:       "exact_duplicate",
		ModelAlias:                  "model_test",
		CacheLayer:                  layer,
		EmbeddingMode:               "fake",
		PromptTokens:                promptTokens,
		CompletionTokens:            completionTokens,
		TotalTokens:                 promptTokens + completionTokens,
		CachedPromptTokensSaved:     promptTokens,
		CachedCompletionTokensSaved: completionTokens,
		TokenEstimationMode:         "provider_usage",
	}
}

func testEnvLookup(values map[string]string) func(string) string {
	return func(key string) string {
		return values[key]
	}
}
