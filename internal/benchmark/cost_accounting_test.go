package benchmark

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	cachepkg "github.com/bianjiefilm/CacheSafety-Bench/internal/cache"
	"github.com/bianjiefilm/CacheSafety-Bench/internal/teacher"

	"github.com/stretchr/testify/require"
)

func TestLoadPricingConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pricing.json")
	require.NoError(t, os.WriteFile(path, []byte(`{
		"default":{"input_per_1m_tokens":1.5,"output_per_1m_tokens":2.5},
		"volcengine_model_alias":{"input_per_1m_tokens":3,"output_per_1m_tokens":4},
		"judge":{"input_per_1m_tokens":5,"output_per_1m_tokens":6},
		"embedding":{"per_1m_tokens":7}
	}`), 0o644))

	got, err := LoadPricingConfig(path)

	require.NoError(t, err)
	require.Equal(t, 1.5, got.Default.InputPer1MTokens)
	require.Equal(t, 4.0, got.Models["volcengine_model_alias"].OutputPer1MTokens)
	require.Equal(t, 6.0, got.Judge.OutputPer1MTokens)
	require.Equal(t, 7.0, got.Embedding.Per1MTokens)
}

func TestEstimateTokensApproxEnglish(t *testing.T) {
	require.Equal(t, 4, EstimateTokensApprox("abcdefghijklmnop"))
}

func TestEstimateTokensApproxChinese(t *testing.T) {
	require.Equal(t, 4, EstimateTokensApprox("缓存系统安全"))
}

func TestProviderUsageBeatsApproximation(t *testing.T) {
	result := cachepkg.LookupResult{
		Layer: cachepkg.LayerMiss,
		Response: cachepkg.Response{
			Text:             "provider answer",
			PromptTokens:     11,
			CompletionTokens: 7,
			TotalTokens:      18,
		},
	}

	got := BuildTokenCostAccount(Case{Request: testBenchRequest("short")}, result, hitEvaluation{}, PricingConfig{}, false, "fake")

	require.Equal(t, 11, got.PromptTokens)
	require.Equal(t, 7, got.CompletionTokens)
	require.Equal(t, 18, got.TotalTokens)
	require.Equal(t, "provider_usage", got.TokenEstimationMode)
}

func TestMissDoesNotCountSavedTokensOrCost(t *testing.T) {
	cases := []Case{{
		ID:           "miss",
		Category:     "general_explanation",
		Request:      testBenchRequest("What is REST?"),
		FreshAnswer:  "REST is an API style.",
		TeacherScore: 10,
		FreshCost:    1,
		CachedCost:   0.1,
	}}
	got, err := RunWithFreshAnswer(context.Background(), cases, Config{
		Seed:              1,
		SemanticThreshold: 0.9,
		Pricing:           PricingConfig{Default: ModelTokenPricing{InputPer1MTokens: 1, OutputPer1MTokens: 1}},
	}, func(ctx context.Context, item Case) (cachepkg.Response, error) {
		return cachepkg.Response{Text: item.FreshAnswer, PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}, nil
	})

	require.NoError(t, err)
	require.Equal(t, 15, got.TotalTokens)
	require.Equal(t, 0, got.CachedTokensSaved)
	require.Equal(t, 0.0, got.CachedCostUSDSaved)
	require.Equal(t, "provider_usage", got.TokenEstimationMode)
}

func TestSemanticHitCountsSavedTokensAndCategoryCost(t *testing.T) {
	cases := []Case{
		{
			ID:                 "seed",
			Category:           "general_explanation",
			Request:            testBenchRequest("What is REST API?"),
			FreshAnswer:        "REST is a web API style.",
			TeacherScore:       10,
			FreshCost:          1,
			CachedCost:         0.1,
			SemanticGroup:      "rest",
			SemanticSimilarity: 0.97,
		},
		{
			ID:                 "hit",
			Category:           "general_explanation",
			Request:            testBenchRequest("Explain REST API simply."),
			FreshAnswer:        "REST is a simple web API style.",
			TeacherScore:       10,
			FreshCost:          1,
			CachedCost:         0.1,
			SemanticGroup:      "rest",
			SemanticSimilarity: 0.97,
		},
	}
	judge := teacher.FakeJudge{Results: map[string]teacher.JudgeResult{
		"general_explanation": {CacheSafe: true, CachedScore: 8, FreshScore: 9, Delta: -1, BadHit: false, Reason: "safe"},
	}}

	got, err := RunWithOptions(context.Background(), cases, Config{
		Seed:              1,
		SemanticThreshold: 0.95,
		JudgeMode:         "fake",
		Pricing:           PricingConfig{Default: ModelTokenPricing{InputPer1MTokens: 1, OutputPer1MTokens: 1}},
	}, nil, judge)

	require.NoError(t, err)
	require.Greater(t, got.CachedTokensSaved, 0)
	require.Greater(t, got.CachedCostUSDSaved, 0.0)
	require.Greater(t, got.NetCostSavedUSD, 0.0)
	category := got.CategoryMetrics["general_explanation"]
	require.Equal(t, got.CachedTokensSaved, category.CachedTokensSaved)
	require.Equal(t, got.CachedCostUSDSaved, category.CachedCostUSDSaved)
}

func TestJudgeAndEmbeddingCostReduceNetSaving(t *testing.T) {
	item := Case{Request: testBenchRequest("Explain cache safety."), FreshAnswer: "Cache safety avoids bad hits."}
	result := cachepkg.LookupResult{
		Layer:        cachepkg.LayerSemantic,
		Response:     cachepkg.Response{Text: "Cache safety avoids bad hits."},
		CachedPrompt: "What is cache safety?",
	}
	evaluation := hitEvaluation{
		judged: true,
		record: teacher.Record{
			Input: teacher.JudgeInput{
				OldRequest:   "What is cache safety?",
				CachedAnswer: "Cache safety avoids bad hits.",
				NewRequest:   "Explain cache safety.",
				FreshAnswer:  "Cache safety avoids bad hits.",
				Category:     "general_explanation",
			},
			Result: teacher.JudgeResult{CacheSafe: true, CachedScore: 9, FreshScore: 9, Delta: 0, BadHit: false},
		},
	}
	pricing := PricingConfig{
		Default:   ModelTokenPricing{InputPer1MTokens: 1, OutputPer1MTokens: 1},
		Judge:     ModelTokenPricing{InputPer1MTokens: 1, OutputPer1MTokens: 1},
		Embedding: EmbeddingPricing{Per1MTokens: 1},
	}

	got := BuildTokenCostAccount(item, result, evaluation, pricing, true, "hash")

	require.Greater(t, got.CachedCostUSDSaved, got.NetCostSavedUSD)
	require.Greater(t, got.JudgeCostUSD, 0.0)
	require.Greater(t, got.EmbeddingCostUSD, 0.0)
}

func TestMissingPricingFileDoesNotPanic(t *testing.T) {
	got, err := LoadPricingConfig(filepath.Join(t.TempDir(), "missing.json"))
	require.NoError(t, err)
	require.Equal(t, PricingConfig{}, got)
}
