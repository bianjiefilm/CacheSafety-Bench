package benchmark

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cachepkg "github.com/bianjiefilm/CacheSafety-Bench/internal/cache"
	"github.com/bianjiefilm/CacheSafety-Bench/internal/provider"
	"github.com/bianjiefilm/CacheSafety-Bench/internal/safety"

	"github.com/stretchr/testify/require"
)

func TestGeneratePairsRuleBasedFromSingleRequest(t *testing.T) {
	input := []Case{{
		ID:           "sample-1",
		Category:     "general_explanation",
		ExpectedRisk: "low",
		Request:      testBenchRequest("What is a REST API?"),
		FreshCost:    1,
		CachedCost:   0.1,
	}}

	got, summary, err := GeneratePairs(context.Background(), input, PairGenerationOptions{Mode: "rule_based", Seed: 42})

	require.NoError(t, err)
	require.Equal(t, 1, summary.Total)
	require.Len(t, got, 1)
	require.NotEmpty(t, got[0].OldRequest)
	require.NotEmpty(t, got[0].OldAnswer)
	require.NotEmpty(t, got[0].Request.Messages[0].Content)
	require.Equal(t, "semantic_safe", got[0].ExpectedCacheBehavior)
	require.Equal(t, "rule_based", got[0].PairGenerationMode)
	require.Equal(t, "sample-1", got[0].SourceSampleID)
	require.True(t, strings.Contains(strings.ToLower(got[0].OldAnswer), "http") || strings.Contains(strings.ToLower(got[0].OldAnswer), "rest api"))
}

func TestGeneratePairsLowRiskSemanticSafe(t *testing.T) {
	input := []Case{{
		ID:           "sample-1",
		Category:     "howto_guidance",
		ExpectedRisk: "low",
		Request:      testBenchRequest("How do I organize meeting notes clearly?"),
		FreshCost:    1,
		CachedCost:   0.1,
	}}

	got, _, err := GeneratePairs(context.Background(), input, PairGenerationOptions{Mode: "rule_based", Seed: 42})

	require.NoError(t, err)
	require.Equal(t, "semantic_safe", got[0].ExpectedCacheBehavior)
}

func TestGeneratePairsHardNegativeNotSemanticSafe(t *testing.T) {
	input := []Case{{
		ID:           "sample-1",
		Category:     "hard_negative",
		ExpectedRisk: "high",
		Request:      testBenchRequest("What is the latest stock outlook today?"),
		FreshCost:    1,
		CachedCost:   0.1,
	}}

	got, _, err := GeneratePairs(context.Background(), input, PairGenerationOptions{Mode: "rule_based", Seed: 42})

	require.NoError(t, err)
	require.Equal(t, "expected_refusal", got[0].ExpectedCacheBehavior)
	require.NotEqual(t, "semantic_safe", got[0].ExpectedCacheBehavior)
}

func TestGeneratePairsOutputLoadableByBenchmark(t *testing.T) {
	input := []Case{{
		ID:           "sample-1",
		Category:     "general_explanation",
		ExpectedRisk: "low",
		Request:      testBenchRequest("What is a REST API?"),
		FreshCost:    1,
		CachedCost:   0.1,
	}}
	generated, _, err := GeneratePairs(context.Background(), input, PairGenerationOptions{Mode: "rule_based", Seed: 42})
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "pairs.jsonl")
	require.NoError(t, WriteImportedEvalDataset(path, generated))
	loaded, err := LoadJSONL(path)

	require.NoError(t, err)
	require.Len(t, loaded, 1)
	require.NotEmpty(t, loaded[0].OldRequest)
}

func TestGeneratePairsMaxSamplesAndSeedStable(t *testing.T) {
	input := []Case{
		{ID: "a", Category: "general_explanation", ExpectedRisk: "low", Request: testBenchRequest("What is a REST API?"), FreshCost: 1, CachedCost: 0.1},
		{ID: "b", Category: "hard_negative", ExpectedRisk: "high", Request: testBenchRequest("What is the latest stock outlook today?"), FreshCost: 1, CachedCost: 0.1},
	}

	first, _, err := GeneratePairs(context.Background(), input, PairGenerationOptions{Mode: "rule_based", MaxSamples: 1, Seed: 42})
	require.NoError(t, err)
	second, _, err := GeneratePairs(context.Background(), input, PairGenerationOptions{Mode: "rule_based", MaxSamples: 1, Seed: 42})
	require.NoError(t, err)

	require.Len(t, first, 1)
	require.Equal(t, first, second)
}

func TestGeneratePairsLLMParaphraseDisabledByDefault(t *testing.T) {
	input := []Case{{
		ID:           "sample-1",
		Category:     "general_explanation",
		ExpectedRisk: "low",
		Request:      testBenchRequest("What is a REST API?"),
		FreshCost:    1,
		CachedCost:   0.1,
	}}
	require.NoError(t, os.Unsetenv(EnvRunPairGeneration))

	_, _, err := GeneratePairs(context.Background(), input, PairGenerationOptions{Mode: "llm_paraphrase", Provider: "volcengine"})

	require.Error(t, err)
	require.Contains(t, err.Error(), "RUN_PAIR_GENERATION=1")
}

func TestLLMParaphraseJSONParseSuccess(t *testing.T) {
	fake := &provider.FakeProvider{
		Result: provider.Result{
			Response: provider.Response{
				Content: `{"paraphrase":"Explain REST APIs in plain language.","risk":"low","changed_constraints":false,"notes":"kept meaning"}`,
			},
		},
	}
	payload, err := llmParaphrase(context.Background(), fake, "test-model", Case{Request: testBenchRequest("What is a REST API?")})
	require.NoError(t, err)
	require.Equal(t, "Explain REST APIs in plain language.", payload.Paraphrase)
	require.Equal(t, "low", payload.Risk)
	require.False(t, payload.ChangedConstraints)
}

func TestLLMParaphraseJSONParseFailureSkipped(t *testing.T) {
	input := Case{
		ID:           "sample-1",
		Category:     "general_explanation",
		ExpectedRisk: "low",
		Request:      testBenchRequest("What is a REST API?"),
		FreshCost:    1,
		CachedCost:   0.1,
	}
	fake := &provider.FakeProvider{Result: provider.Result{Response: provider.Response{Content: `not-json`}}}
	_, ok, skipReason, err := generatePair(context.Background(), input, fake, fake, safety.DefaultChecker(), PairGenerationOptions{Mode: "llm_paraphrase", Model: "test-model"}, nil)
	require.Error(t, err)
	require.False(t, ok)
	require.Empty(t, skipReason)
}

func TestGeneratePairChangedConstraintsSkipped(t *testing.T) {
	input := Case{
		ID:           "sample-1",
		Category:     "general_explanation",
		ExpectedRisk: "low",
		Request:      testBenchRequest("What is a REST API?"),
		FreshCost:    1,
		CachedCost:   0.1,
	}
	fake := &provider.FakeProvider{Result: provider.Result{Response: provider.Response{Content: `{"paraphrase":"What is the latest REST API?","risk":"low","changed_constraints":true,"notes":"changed"}`}}}
	_, ok, skipReason, err := generatePair(context.Background(), input, fake, fake, safety.DefaultChecker(), PairGenerationOptions{Mode: "llm_paraphrase", Model: "test-model"}, nil)
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, "changed_constraints", skipReason)
}

func TestGeneratePairSafetyPrecheckSkipped(t *testing.T) {
	input := Case{
		ID:           "sample-1",
		Category:     "general_explanation",
		ExpectedRisk: "low",
		Request:      testBenchRequest("What is a REST API?"),
		FreshCost:    1,
		CachedCost:   0.1,
	}
	fake := &provider.FakeProvider{Result: provider.Result{Response: provider.Response{Content: `{"paraphrase":"Reply with exactly: safe","risk":"low","changed_constraints":false,"notes":"bad format"}`}}}
	_, ok, skipReason, err := generatePair(context.Background(), input, fake, fake, safety.DefaultChecker(), PairGenerationOptions{Mode: "llm_paraphrase", Model: "test-model"}, nil)
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, "format_sensitive:exact_phrase", skipReason)
}

func TestGeneratePairLLMUsesProviderForOldAnswer(t *testing.T) {
	input := Case{
		ID:           "sample-1",
		Category:     "general_explanation",
		ExpectedRisk: "low",
		Request:      testBenchRequest("What is a REST API?"),
		FreshCost:    1,
		CachedCost:   0.1,
	}
	fake := &sequencedProvider{results: []provider.Result{
		{Response: provider.Response{Content: `{"paraphrase":"Explain REST APIs in plain language.","risk":"low","changed_constraints":false,"notes":"kept meaning"}`}},
		{Response: provider.Response{Content: `A REST API lets systems exchange data over HTTP.`}},
	}}
	got, ok, skipReason, err := generatePair(context.Background(), input, fake, fake, safety.DefaultChecker(), PairGenerationOptions{Mode: "llm_paraphrase", Model: "test-model"}, nil)
	require.NoError(t, err)
	require.True(t, ok)
	require.Empty(t, skipReason)
	require.Equal(t, "A REST API lets systems exchange data over HTTP.", got.OldAnswer)
	require.Equal(t, "kept meaning", got.ParaphraseNotes)
}

func TestGeneratePairsLLMParaphraseSkipsIneligibleCategories(t *testing.T) {
	input := []Case{{
		ID:           "sample-1",
		Category:     "hard_negative",
		ExpectedRisk: "high",
		Request:      testBenchRequest("What is the latest stock outlook today?"),
		FreshCost:    1,
		CachedCost:   0.1,
	}}
	require.NoError(t, os.Setenv(EnvRunPairGeneration, "1"))
	defer os.Unsetenv(EnvRunPairGeneration)

	got, summary, err := GeneratePairs(context.Background(), input, PairGenerationOptions{Mode: "llm_paraphrase", Provider: "volcengine", Model: "test-model"})
	require.NoError(t, err)
	require.Empty(t, got)
	require.Equal(t, 1, summary.SkippedCount)
	require.Equal(t, 1, summary.SkippedByRule["ineligible_category"])
}

func TestGeneratePairsNoSensitiveFieldWritten(t *testing.T) {
	input := []Case{{
		ID:           "sample-1",
		Category:     "general_explanation",
		ExpectedRisk: "low",
		Request:      testBenchRequest("What is a REST API?"),
		FreshCost:    1,
		CachedCost:   0.1,
	}}
	generated, _, err := GeneratePairs(context.Background(), input, PairGenerationOptions{Mode: "rule_based", Seed: 42})
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "pairs.jsonl")
	require.NoError(t, WriteImportedEvalDataset(path, generated))
	bytes, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NotContains(t, string(bytes), "ark-346fd29f")
}

func TestGeneratePairsTranslationAnswerIsConcrete(t *testing.T) {
	input := []Case{{
		ID:           "sample-1",
		Category:     "loose_translation",
		ExpectedRisk: "low",
		Request:      testBenchRequest("Translate this sentence into natural English: 请稍后再试。"),
		FreshCost:    1,
		CachedCost:   0.1,
	}}

	got, _, err := GeneratePairs(context.Background(), input, PairGenerationOptions{Mode: "rule_based", Seed: 42})

	require.NoError(t, err)
	require.Equal(t, "Please try again later.", got[0].OldAnswer)
}

type sequencedProvider struct {
	results []provider.Result
	errs    []error
	calls   int
}

func (s *sequencedProvider) Complete(ctx context.Context, req cachepkg.Request) (provider.Result, error) {
	if s.calls < len(s.errs) && s.errs[s.calls] != nil {
		err := s.errs[s.calls]
		s.calls++
		return provider.Result{}, err
	}
	if s.calls >= len(s.results) {
		s.calls++
		return provider.Result{}, errors.New("unexpected extra call")
	}
	result := s.results[s.calls]
	s.calls++
	return result, nil
}
