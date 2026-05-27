package benchmark

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	cachepkg "github.com/bianjiefilm/CacheSafety-Bench/internal/cache"
	"github.com/bianjiefilm/CacheSafety-Bench/internal/embedding"
	"github.com/bianjiefilm/CacheSafety-Bench/internal/teacher"

	"github.com/stretchr/testify/require"
)

func TestBenchmarkRunOutputsRequiredMetrics(t *testing.T) {
	cases, err := LoadJSONL(filepath.Join("..", "..", "testdata", "benchmark", "synthetic_v0.jsonl"))
	require.NoError(t, err)

	got, err := Run(context.Background(), cases, Config{Seed: 42, SemanticThreshold: 0.95})

	require.NoError(t, err)
	require.Greater(t, got.Total, 0)
	require.Greater(t, got.HitRate, 0.0)
	require.Greater(t, got.SafeHitRate, 0.0)
	require.Equal(t, 0.0, got.BadHitRate)
	require.Greater(t, got.SafeCostSaved, 0.0)
	require.Greater(t, got.BlendedCostPerRequest, 0.0)
	require.Contains(t, got.LayerContribution, "exact")
	require.Contains(t, got.LayerContribution, "canonical")
	require.Contains(t, got.LayerContribution, "semantic")
	require.NotEmpty(t, got.RefusedByRule)
}

func TestBenchmarkIsReproducibleWithSameSeedAndConfig(t *testing.T) {
	cases, err := LoadJSONL(filepath.Join("..", "..", "testdata", "benchmark", "synthetic_v0.jsonl"))
	require.NoError(t, err)

	first, err := Run(context.Background(), cases, Config{Seed: 7, SemanticThreshold: 0.95, Shuffle: true})
	require.NoError(t, err)
	second, err := Run(context.Background(), cases, Config{Seed: 7, SemanticThreshold: 0.95, Shuffle: true})
	require.NoError(t, err)

	require.Equal(t, first, second)
}

func TestBenchmarkCanUseFreshAnswerProviderForMisses(t *testing.T) {
	cases := []Case{
		{
			ID:           "01-seed",
			Request:      testBenchRequest("Reply exactly with provider answer."),
			FreshAnswer:  "provider answer",
			TeacherScore: 10,
			FreshCost:    1,
			CachedCost:   0.1,
		},
		{
			ID:           "02-exact",
			Request:      testBenchRequest("Reply exactly with provider answer."),
			FreshAnswer:  "provider answer",
			TeacherScore: 10,
			FreshCost:    1,
			CachedCost:   0.1,
		},
	}
	calls := 0

	got, err := RunWithFreshAnswer(context.Background(), cases, Config{Seed: 1}, func(ctx context.Context, item Case) (cachepkg.Response, error) {
		calls++
		return cachepkg.Response{Text: "provider answer", TeacherScore: item.TeacherScore}, nil
	})

	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Equal(t, 1, got.LayerContribution["exact"])
	require.Equal(t, 1, got.LayerContribution["miss"])
	require.Equal(t, 0.0, got.BadHitRate)
}

func TestBenchmarkUsesJudgeInsteadOfStringEquality(t *testing.T) {
	cases := []Case{
		{
			ID:                 "01-seed",
			Kind:               "seed",
			Request:            testBenchRequest("Describe safe semantic cache reuse."),
			FreshAnswer:        "Integrity",
			TeacherScore:       10,
			FreshCost:          1,
			CachedCost:         0.1,
			SemanticGroup:      "safe",
			SemanticSimilarity: 0,
		},
		{
			ID:                 "02-paraphrase",
			Kind:               "semantic_paraphrase",
			Request:            testBenchRequest("Describe safe semantic cache reuse in brief terms."),
			FreshAnswer:        "safe",
			TeacherScore:       10,
			FreshCost:          1,
			CachedCost:         0.1,
			SemanticGroup:      "safe",
			SemanticSimilarity: 0.97,
		},
	}
	judge := teacher.FakeJudge{Results: map[string]teacher.JudgeResult{
		"semantic_paraphrase": {
			CacheSafe:   true,
			CachedScore: 8,
			FreshScore:  9,
			Delta:       -1,
			BadHit:      false,
			Reason:      "minor wording difference only",
		},
	}}

	got, err := RunWithOptions(context.Background(), cases, Config{Seed: 1, JudgeMode: "fake"}, nil, judge)

	require.NoError(t, err)
	require.Equal(t, 1, got.JudgedCount)
	require.Equal(t, 0, got.UnknownCount)
	require.Equal(t, 0.0, got.BadHitRate)
	require.Equal(t, 1.0, got.CachedGEFreshMinus1Rate)
	require.Empty(t, got.BadHits)
}

func TestBenchmarkJudgeErrorMarksUnknown(t *testing.T) {
	cases := []Case{
		{
			ID:            "01-seed",
			Request:       testBenchRequest("A"),
			FreshAnswer:   "A",
			TeacherScore:  10,
			FreshCost:     1,
			CachedCost:    0.1,
			SemanticGroup: "safe",
		},
		{
			ID:                 "02-paraphrase",
			Kind:               "semantic_paraphrase",
			Request:            testBenchRequest("B"),
			FreshAnswer:        "B",
			TeacherScore:       10,
			FreshCost:          1,
			CachedCost:         0.1,
			SemanticGroup:      "safe",
			SemanticSimilarity: 0.97,
		},
	}
	judge := teacher.FakeJudge{Err: teacher.ErrJudgeParse}

	got, err := RunWithOptions(context.Background(), cases, Config{Seed: 1, JudgeMode: "fake"}, nil, judge)

	require.NoError(t, err)
	require.Equal(t, 1, got.UnknownCount)
	require.Equal(t, 1, got.JudgeErrorCount)
	require.Equal(t, 0.0, got.BadHitRate)
	require.Len(t, got.JudgeRecords, 1)
}

func TestBenchmarkEmbeddingSkipsTooLongPrompt(t *testing.T) {
	longPrompt := strings.Repeat("x", 513)
	cases := []Case{{
		ID:           "01-long",
		Request:      testBenchRequest(longPrompt),
		FreshAnswer:  "fresh",
		TeacherScore: 10,
		FreshCost:    1,
		CachedCost:   0.1,
	}}
	embedder := &countingEmbedder{}

	got, err := RunWithEmbedding(context.Background(), cases, Config{EmbeddingMode: "counting"}, nil, nil, embedder)

	require.NoError(t, err)
	require.Equal(t, 0, embedder.calls)
	require.Equal(t, "counting", got.EmbeddingMode)
}

func TestBenchmarkSemanticStatsWithEmbedding(t *testing.T) {
	cases := []Case{
		{
			ID:           "01-seed",
			Kind:         "seed",
			Request:      testBenchRequest("Explain safe semantic cache reuse."),
			FreshAnswer:  "safe",
			TeacherScore: 10,
			FreshCost:    1,
			CachedCost:   0.1,
		},
		{
			ID:           "02-paraphrase",
			Kind:         "semantic_paraphrase",
			Request:      testBenchRequest("Describe safe semantic cache reuse."),
			FreshAnswer:  "safe",
			TeacherScore: 10,
			FreshCost:    1,
			CachedCost:   0.1,
		},
	}

	got, err := RunWithEmbedding(context.Background(), cases, Config{EmbeddingMode: "fake"}, nil, teacher.FakeJudge{}, embedding.FakeEmbedder{})

	require.NoError(t, err)
	require.Equal(t, 1, got.SemanticCandidates)
	require.Equal(t, 0, got.SemanticRejectedBySafety)
	require.Equal(t, 1, got.SemanticJudgedSafe)
	require.GreaterOrEqual(t, got.AvgSemanticSimilarity, 0.95)
}

func TestCategoryMetricsAggregateSemanticOutcomes(t *testing.T) {
	cases := []Case{
		{
			ID:           "01-seed",
			Category:     "general_explanation",
			Request:      testBenchRequest("Explain safe semantic cache reuse."),
			FreshAnswer:  "safe",
			TeacherScore: 10,
			FreshCost:    1,
			CachedCost:   0.1,
		},
		{
			ID:           "02-paraphrase",
			Category:     "general_explanation",
			Request:      testBenchRequest("Describe safe semantic cache reuse."),
			FreshAnswer:  "safe",
			TeacherScore: 10,
			FreshCost:    1,
			CachedCost:   0.1,
		},
		{
			ID:           "03-hard-negative",
			Category:     "hard_negative",
			Request:      testBenchRequest("Answer with one word about safe semantic cache reuse."),
			FreshAnswer:  "safe",
			TeacherScore: 10,
			FreshCost:    1,
			CachedCost:   0.1,
		},
	}

	got, err := RunWithEmbedding(context.Background(), cases, Config{EmbeddingMode: "fake"}, nil, teacher.FakeJudge{}, embedding.FakeEmbedder{})

	require.NoError(t, err)
	require.Equal(t, 2, got.CategoryMetrics["general_explanation"].Total)
	require.Equal(t, 1, got.CategoryMetrics["general_explanation"].SemanticCandidates)
	require.Equal(t, 1, got.CategoryMetrics["general_explanation"].SemanticJudgedSafe)
	require.Equal(t, 0.9, got.CategoryMetrics["general_explanation"].SafeCostSaved)
	require.Equal(t, 1, got.CategoryMetrics["hard_negative"].Total)
	require.Equal(t, 1, got.CategoryMetrics["hard_negative"].SemanticCandidates)
	require.Equal(t, 1, got.CategoryMetrics["hard_negative"].SemanticRejectedBySafety)
	require.Equal(t, 0.0, got.CategoryMetrics["hard_negative"].BadHitRate)
}

func TestSafeParaphraseCanProduceSemanticCandidate(t *testing.T) {
	cases := []Case{
		{
			ID:           "01-seed",
			Category:     "general_explanation",
			Request:      testBenchRequest("Explain prompt caching in simple language."),
			FreshAnswer:  "Prompt caching reuses previous prompt work.",
			TeacherScore: 10,
			FreshCost:    1,
			CachedCost:   0.1,
		},
		{
			ID:           "02-safe",
			Category:     "general_explanation",
			Request:      testBenchRequest("Describe prompt caching in simple language."),
			FreshAnswer:  "Prompt caching reuses previous prompt work.",
			TeacherScore: 10,
			FreshCost:    1,
			CachedCost:   0.1,
		},
	}
	embedder := &countingEmbedder{vector: []float64{1, 0}}

	got, err := RunWithEmbedding(context.Background(), cases, Config{EmbeddingMode: "counting"}, nil, teacher.FakeJudge{}, embedder)

	require.NoError(t, err)
	require.Equal(t, 1, got.SemanticCandidates)
	require.Equal(t, 1, got.SemanticJudgedSafe)
	require.Equal(t, 0.0, got.BadHitRate)
}

func TestHardNegativeRejectedBySafetyGuard(t *testing.T) {
	cases := []Case{
		{
			ID:           "01-seed",
			Category:     "hard_negative",
			Request:      testBenchRequest("Explain prompt caching in simple language."),
			FreshAnswer:  "Prompt caching reuses previous prompt work.",
			TeacherScore: 10,
			FreshCost:    1,
			CachedCost:   0.1,
		},
		{
			ID:           "02-hard-negative",
			Category:     "hard_negative",
			Request:      testBenchRequest("Answer with one word about prompt caching."),
			FreshAnswer:  "reuse",
			TeacherScore: 10,
			FreshCost:    1,
			CachedCost:   0.1,
		},
	}
	embedder := &countingEmbedder{vector: []float64{1, 0}}

	got, err := RunWithEmbedding(context.Background(), cases, Config{EmbeddingMode: "counting"}, nil, teacher.FakeJudge{}, embedder)

	require.NoError(t, err)
	require.Equal(t, 1, got.SemanticCandidates)
	require.Equal(t, 1, got.SemanticRejectedBySafety)
	require.Equal(t, 1, got.RefusedByRule["format_sensitive"])
	require.Equal(t, 0.0, got.BadHitRate)
}

func TestThresholdScanOutputsEachThreshold(t *testing.T) {
	cases := []Case{
		{
			ID:           "01-seed",
			Category:     "seed",
			Request:      testBenchRequest("Explain safe semantic cache reuse."),
			FreshAnswer:  "safe",
			TeacherScore: 10,
			FreshCost:    1,
			CachedCost:   0.1,
		},
		{
			ID:           "02-paraphrase",
			Category:     "semantic_paraphrase",
			Request:      testBenchRequest("Describe safe semantic cache reuse."),
			FreshAnswer:  "safe",
			TeacherScore: 10,
			FreshCost:    1,
			CachedCost:   0.1,
		},
	}

	got, err := RunThresholdScan(context.Background(), cases, Config{EmbeddingMode: "fake"}, []float64{0.95, 0.90, 0.85}, nil, teacher.FakeJudge{}, embedding.FakeEmbedder{})

	require.NoError(t, err)
	require.Len(t, got.Results, 3)
	require.Equal(t, 0.95, got.Results[0].Threshold)
	require.Equal(t, 0.90, got.Results[1].Threshold)
	require.Equal(t, 0.85, got.Results[2].Threshold)
	for _, result := range got.Results {
		require.Equal(t, 2, result.Total)
		require.Contains(t, result.LayerContribution, "semantic")
		require.Equal(t, 0, result.BadHitExamplesCount)
	}
}

func TestDefaultSemanticThresholdRemains095(t *testing.T) {
	got, err := RunWithEmbedding(context.Background(), nil, Config{}, nil, nil, nil)

	require.NoError(t, err)
	require.Equal(t, 0.95, got.Threshold)
}

func TestSQLiteInt8VectorStoreModeIsExplicitCandidate(t *testing.T) {
	cacheSafe := true
	cases := []Case{
		{
			ID:                    "pair-1",
			Category:              "general_explanation",
			Request:               testBenchRequest("Describe safe semantic cache reuse."),
			FreshAnswer:           "safe",
			TeacherScore:          10,
			FreshCost:             1,
			CachedCost:            0.1,
			OldRequest:            "Explain safe semantic cache reuse.",
			OldAnswer:             "safe",
			ExpectedCacheBehavior: "semantic_safe",
			PairGenerationMode:    "rule_based",
			SourceJudgeCacheSafe:  &cacheSafe,
		},
	}

	got, err := RunWithEmbedding(context.Background(), cases, Config{
		Seed:              42,
		SemanticThreshold: 0.90,
		EmbeddingMode:     "fake",
		VectorStoreMode:   "sqlite-int8",
		SQLitePath:        filepath.Join(t.TempDir(), "int8.db"),
	}, nil, teacher.FakeJudge{}, embedding.FakeEmbedder{})

	require.NoError(t, err)
	require.Equal(t, "sqlite-int8", got.VectorStore)
	require.Equal(t, 1, got.SemanticCandidates)
	require.Equal(t, 1, got.SemanticJudgedSafe)
	require.Equal(t, 0.0, got.BadHitRate)
}

func TestBadHitExamplesCanBeWrittenWithContext(t *testing.T) {
	sample := BadHitSample{
		ID:           "case-1",
		Category:     "semantic_trap",
		OldRequest:   "refund in 7 days",
		NewRequest:   "refund in 30 days",
		CachedAnswer: "Refunds are allowed for 7 days.",
		FreshAnswer:  "Refunds are allowed for 30 days.",
		Similarity:   0.91,
		JudgeReason:  "duration changed",
	}
	dir := t.TempDir()

	require.NoError(t, WriteBadHitExamples(dir, 0.90, []BadHitSample{sample}))

	payload, err := LoadBadHitExamples(filepath.Join(dir, "threshold_0.90.jsonl"))
	require.NoError(t, err)
	require.Len(t, payload, 1)
	require.Equal(t, "semantic_trap", payload[0].Category)
	require.Equal(t, "refund in 7 days", payload[0].OldRequest)
	require.Equal(t, "refund in 30 days", payload[0].NewRequest)
	require.Equal(t, 0.91, payload[0].Similarity)
	require.Equal(t, "duration changed", payload[0].JudgeReason)
}

func TestJudgeErrorCountsUnknownNotBadHit(t *testing.T) {
	cases := []Case{
		{
			ID:           "01-seed",
			Request:      testBenchRequest("Explain safe semantic cache reuse."),
			FreshAnswer:  "safe",
			TeacherScore: 10,
			FreshCost:    1,
			CachedCost:   0.1,
		},
		{
			ID:           "02-paraphrase",
			Category:     "semantic_paraphrase",
			Request:      testBenchRequest("Describe safe semantic cache reuse."),
			FreshAnswer:  "safe",
			TeacherScore: 10,
			FreshCost:    1,
			CachedCost:   0.1,
		},
	}

	got, err := RunWithEmbedding(context.Background(), cases, Config{EmbeddingMode: "fake"}, nil, teacher.FakeJudge{Err: teacher.ErrJudgeParse}, embedding.FakeEmbedder{})

	require.NoError(t, err)
	require.Equal(t, 1, got.UnknownCount)
	require.Equal(t, 1, got.JudgeErrorCount)
	require.Equal(t, 0.0, got.BadHitRate)
	require.Empty(t, got.BadHits)
}

func TestBenchmarkSampleIDsFilter(t *testing.T) {
	cases := []Case{
		{ID: "a", Request: testBenchRequest("A"), FreshAnswer: "A", TeacherScore: 10, FreshCost: 1, CachedCost: 0.1},
		{ID: "b", Request: testBenchRequest("B"), FreshAnswer: "B", TeacherScore: 10, FreshCost: 1, CachedCost: 0.1},
		{ID: "c", Request: testBenchRequest("C"), FreshAnswer: "C", TeacherScore: 10, FreshCost: 1, CachedCost: 0.1},
	}

	got, err := RunWithEmbedding(context.Background(), cases, Config{SampleIDs: []string{"b"}}, nil, nil, nil)

	require.NoError(t, err)
	require.Equal(t, 1, got.Total)
}

func TestJudgeTimeoutRetriesOnce(t *testing.T) {
	cases := []Case{
		{ID: "seed", Request: testBenchRequest("Explain one thing."), FreshAnswer: "A", TeacherScore: 10, FreshCost: 1, CachedCost: 0.1},
		{ID: "hit", Category: "general_explanation", Request: testBenchRequest("Explain one thing."), FreshAnswer: "A better answer", TeacherScore: 10, FreshCost: 1, CachedCost: 0.1},
	}
	judge := &retryJudge{
		results: []retryJudgeResult{
			{err: context.DeadlineExceeded},
			{result: teacher.JudgeResult{CacheSafe: true, CachedScore: 8, FreshScore: 9, Delta: -1, BadHit: false, Reason: "retry recovered"}},
		},
	}

	got, err := RunWithOptions(context.Background(), cases, Config{JudgeMode: "retry"}, nil, judge)

	require.NoError(t, err)
	require.Equal(t, 2, judge.calls)
	require.Equal(t, 0, got.UnknownCount)
	require.Equal(t, 0, got.JudgeErrorCount)
	require.Equal(t, 1, got.JudgedCount)
}

func TestJudgeTimeoutRetryFailureStaysUnknown(t *testing.T) {
	cases := []Case{
		{ID: "seed", Request: testBenchRequest("Explain two things."), FreshAnswer: "A", TeacherScore: 10, FreshCost: 1, CachedCost: 0.1},
		{ID: "hit", Category: "general_explanation", Request: testBenchRequest("Explain two things."), FreshAnswer: "A better answer", TeacherScore: 10, FreshCost: 1, CachedCost: 0.1},
	}
	judge := &retryJudge{
		results: []retryJudgeResult{
			{err: context.DeadlineExceeded},
			{err: context.DeadlineExceeded},
		},
	}

	got, err := RunWithOptions(context.Background(), cases, Config{JudgeMode: "retry"}, nil, judge)

	require.NoError(t, err)
	require.Equal(t, 2, judge.calls)
	require.Equal(t, 1, got.UnknownCount)
	require.Equal(t, 1, got.JudgeErrorCount)
}

type retryJudgeResult struct {
	result teacher.JudgeResult
	err    error
}

type retryJudge struct {
	results []retryJudgeResult
	calls   int
}

func (j *retryJudge) Score(ctx context.Context, input teacher.JudgeInput) (teacher.JudgeResult, error) {
	if j.calls >= len(j.results) {
		return teacher.JudgeResult{}, errors.New("unexpected judge call")
	}
	current := j.results[j.calls]
	j.calls++
	return current.result, current.err
}

func TestSafetyRefusalDoesNotBecomeSemanticSafeHit(t *testing.T) {
	cases := []Case{
		{
			ID:           "01-seed",
			Request:      testBenchRequest("Explain safe semantic cache reuse."),
			FreshAnswer:  "safe",
			TeacherScore: 10,
			FreshCost:    1,
			CachedCost:   0.1,
		},
		{
			ID:           "02-time-sensitive",
			Category:     "time_sensitive",
			Request:      testBenchRequest("What is the latest guidance about semantic cache reuse today?"),
			FreshAnswer:  "fresh required",
			TeacherScore: 10,
			FreshCost:    1,
			CachedCost:   0.1,
		},
	}

	got, err := RunWithEmbedding(context.Background(), cases, Config{EmbeddingMode: "fake"}, nil, teacher.FakeJudge{}, embedding.FakeEmbedder{})

	require.NoError(t, err)
	require.Equal(t, 1, got.SemanticCandidates)
	require.Equal(t, 1, got.SemanticRejectedBySafety)
	require.Equal(t, 0, got.SemanticJudgedSafe)
	require.Equal(t, 2, got.LayerContribution["miss"])
}

func TestConstraintMismatchRefusedByRuleCounted(t *testing.T) {
	cases := []Case{
		{
			ID:           "01-seed",
			Request:      testBenchRequest("Refund requests must be submitted within 7 days."),
			FreshAnswer:  "7 days",
			TeacherScore: 10,
			FreshCost:    1,
			CachedCost:   0.1,
		},
		{
			ID:           "02-trap",
			Category:     "semantic_trap",
			Request:      testBenchRequest("Refund requests must be submitted within 30 days."),
			FreshAnswer:  "30 days",
			TeacherScore: 10,
			FreshCost:    1,
			CachedCost:   0.1,
		},
	}
	embedder := &countingEmbedder{vector: []float64{1, 0}}

	got, err := RunWithEmbedding(context.Background(), cases, Config{EmbeddingMode: "counting"}, nil, teacher.FakeJudge{}, embedder)

	require.NoError(t, err)
	require.Equal(t, 1, got.SemanticCandidates)
	require.Equal(t, 1, got.SemanticRejectedBySafety)
	require.Equal(t, 0, got.SemanticJudgedSafe)
	require.Equal(t, 1, got.RefusedByRule["constraint_mismatch"])
	require.Equal(t, 1, got.RefusedByDetail["constraint_mismatch:duration"])
	require.Equal(t, 0.0, got.BadHitRate)
}

func TestFormatSensitiveRefusedByRuleCounted(t *testing.T) {
	cases := []Case{
		{
			ID:           "01-seed",
			Request:      testBenchRequest("Reply with one word about safe semantic cache reuse."),
			FreshAnswer:  "Valid",
			TeacherScore: 10,
			FreshCost:    1,
			CachedCost:   0.1,
		},
		{
			ID:           "02-format-sensitive",
			Category:     "paraphrase_safe",
			Request:      testBenchRequest("Give one word describing safe semantic cache reuse."),
			FreshAnswer:  "safe",
			TeacherScore: 10,
			FreshCost:    1,
			CachedCost:   0.1,
		},
	}
	embedder := &countingEmbedder{vector: []float64{1, 0}}

	got, err := RunWithEmbedding(context.Background(), cases, Config{EmbeddingMode: "counting"}, nil, teacher.FakeJudge{}, embedder)

	require.NoError(t, err)
	require.Equal(t, 1, got.SemanticCandidates)
	require.Equal(t, 1, got.SemanticRejectedBySafety)
	require.Equal(t, 0, got.SemanticJudgedSafe)
	require.Equal(t, 1, got.RefusedByRule["format_sensitive"])
	require.Equal(t, 1, got.RefusedByDetail["format_sensitive:one_word"])
	require.Equal(t, 0.0, got.BadHitRate)
}

func TestTeacherScoringOutputIsJSONLCompatible(t *testing.T) {
	line, err := MarshalTeacherScoreJSONL(TeacherScoreRecord{
		ID:          "case-1",
		Prompt:      "Explain cache safety.",
		CachedScore: 0.8,
		FreshScore:  0.9,
		Delta:       0.1,
	})

	require.NoError(t, err)
	require.True(t, bytes.HasSuffix(line, []byte("\n")))

	var decoded TeacherScoreRecord
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(line), &decoded))
	require.Equal(t, "case-1", decoded.ID)
}

func TestBadHitSampleCanBeReplayedAsRegressionCase(t *testing.T) {
	cases := badHitCasesWithoutConstraintMismatch()

	metrics, err := Run(context.Background(), cases, Config{Seed: 1, SemanticThreshold: 0.95})
	require.NoError(t, err)
	require.Len(t, metrics.BadHits, 1)

	replay := ReplayBadHit(metrics.BadHits[0])
	require.Equal(t, metrics.BadHits[0].ID, replay.ID)
	require.Equal(t, metrics.BadHits[0].FreshAnswer, replay.FreshAnswer)
}

func TestBadHitCanBeWrittenAndLoadedAsRegressionSample(t *testing.T) {
	cases := badHitCasesWithoutConstraintMismatch()

	metrics, err := Run(context.Background(), cases, Config{Seed: 1, SemanticThreshold: 0.95})
	require.NoError(t, err)
	require.Len(t, metrics.BadHits, 1)

	path := filepath.Join(t.TempDir(), "bad_hits", "case.jsonl")
	require.NoError(t, WriteBadHitRegression(path, metrics.BadHits[0]))

	replay, err := LoadRegressionSample(path)
	require.NoError(t, err)
	require.Equal(t, "02-concept-trap", replay.ID)
	require.Equal(t, "beta", replay.FreshAnswer)
}

func TestDecisionLogWriteReadAndPreview(t *testing.T) {
	path := filepath.Join(t.TempDir(), "decisions.jsonl")
	records := []DecisionRecord{{
		SampleID:               "sample-1",
		Dataset:                "dataset.jsonl",
		Category:               "general",
		ModelAlias:             ModelAlias("sensitive-real-model-id"),
		VectorStore:            "sqlite",
		EmbeddingMode:          "fake",
		Threshold:              0.9,
		CacheLayer:             "semantic",
		SemanticCandidateFound: true,
		CandidateAnswerPreview: Preview(strings.Repeat("a", 250), 200),
		SafetyAllowed:          true,
		JudgeMode:              "fake",
		EstimatedUpstreamCost:  1,
		SafeCostSaved:          0.9,
	}}

	require.NoError(t, WriteDecisionLog(path, records))
	got, err := LoadDecisionLog(path)

	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Len(t, []rune(got[0].CandidateAnswerPreview), 200)
	require.NotContains(t, got[0].ModelAlias, "sensitive-real-model-id")
	require.Equal(t, "model_", got[0].ModelAlias[:6])
}

func TestDecisionLogIncludesSemanticRefusalAndJudgeFields(t *testing.T) {
	cases := []Case{
		{
			ID:           "01-seed",
			Category:     "general_explanation",
			Request:      testBenchRequest("Explain safe semantic cache reuse."),
			FreshAnswer:  "safe",
			TeacherScore: 10,
			FreshCost:    1,
			CachedCost:   0.1,
		},
		{
			ID:           "02-safe",
			Category:     "general_explanation",
			Request:      testBenchRequest("Describe safe semantic cache reuse."),
			FreshAnswer:  "safe",
			TeacherScore: 10,
			FreshCost:    1,
			CachedCost:   0.1,
		},
		{
			ID:           "03-refused",
			Category:     "hard_negative",
			Request:      testBenchRequest("Answer with one word about safe semantic cache reuse."),
			FreshAnswer:  "safe",
			TeacherScore: 10,
			FreshCost:    1,
			CachedCost:   0.1,
		},
	}

	got, err := RunWithEmbedding(context.Background(), cases, Config{Dataset: "fixtures.jsonl", EmbeddingMode: "fake", SemanticThreshold: 0.95}, nil, teacher.FakeJudge{}, embedding.FakeEmbedder{})

	require.NoError(t, err)
	require.Len(t, got.DecisionRecords, 3)
	require.Equal(t, 0.95, got.DecisionRecords[1].Threshold)
	require.NotNil(t, got.DecisionRecords[1].JudgeCacheSafe)
	require.True(t, *got.DecisionRecords[1].JudgeCacheSafe)
	require.Equal(t, "format_sensitive", got.DecisionRecords[2].RefusedReason)
	require.Equal(t, "format_sensitive:one_word", got.DecisionRecords[2].RefusedDetail)
}

func TestAnalyzeDecisionRecordsAggregates(t *testing.T) {
	cacheSafe := true
	cacheUnsafe := false
	records := []DecisionRecord{
		{Category: "general", SourceKind: "safe_paraphrase", ExpectedCacheBehavior: "semantic_safe", CacheLayer: "semantic", Threshold: 0.9, SemanticCandidateFound: true, Similarity: 0.96, JudgeCacheSafe: &cacheSafe, SafeCostSaved: 0.9},
		{Category: "hard", SourceKind: "regression", ExpectedCacheBehavior: "expected_refusal", CacheLayer: "miss", Threshold: 0.9, SemanticCandidateFound: true, Similarity: 0.91, RefusedReason: "format_sensitive"},
		{Category: "trap", SourceKind: "rule_based", ExpectedCacheBehavior: "semantic_safe", CacheLayer: "semantic", Threshold: 0.85, SemanticCandidateFound: true, Similarity: 0.88, JudgeCacheSafe: &cacheUnsafe, BadHit: true},
	}

	got := AnalyzeDecisionRecords(records)

	require.Equal(t, 3, got.Total)
	require.Equal(t, 2, got.ByCacheLayer["semantic"])
	require.Equal(t, 1, got.ByRefusedReason["format_sensitive"])
	require.Equal(t, 2, got.ByThreshold["0.90"])
	require.Equal(t, 1, got.BySourceKind["safe_paraphrase"])
	require.Equal(t, 1, got.ByExpectedCacheBehavior["expected_refusal"])
	require.Equal(t, 3, got.SemanticCandidates)
	require.Equal(t, 1, got.JudgedSafe)
	require.Equal(t, 1, got.BadHitCount)
	require.Equal(t, 1, got.TopBadHitCategories["trap"])
	require.Equal(t, 0.9, got.SafeCostSavedTotal)
	require.Equal(t, 1, got.CategoryCoverage["general"].SemanticJudgedSafe)
}

func badHitCasesWithoutConstraintMismatch() []Case {
	return []Case{
		{
			ID:                 "01-concept-seed",
			Kind:               "seed",
			Request:            testBenchRequest("Describe alpha concept."),
			FreshAnswer:        "alpha",
			TeacherScore:       10,
			FreshCost:          1,
			CachedCost:         0.1,
			SemanticGroup:      "concept",
			SemanticSimilarity: 0.99,
		},
		{
			ID:                 "02-concept-trap",
			Kind:               "semantic_trap",
			Request:            testBenchRequest("Describe beta concept."),
			FreshAnswer:        "beta",
			TeacherScore:       10,
			FreshCost:          1,
			CachedCost:         0.1,
			SemanticGroup:      "concept",
			SemanticSimilarity: 0.99,
		},
	}
}

func testBenchRequest(prompt string) cachepkg.Request {
	temp := 0.0
	return cachepkg.Request{
		Model:       "benchmark-model",
		Messages:    []cachepkg.Message{{Role: "user", Content: prompt}},
		Temperature: &temp,
	}
}

type countingEmbedder struct {
	calls  int
	vector []float64
}

func (e *countingEmbedder) Embed(ctx context.Context, req embedding.EmbedRequest) (embedding.EmbedResult, error) {
	e.calls++
	vector := e.vector
	if len(vector) == 0 {
		vector = []float64{1, 0}
	}
	return embedding.EmbedResult{Vector: vector, Dim: len(vector), Model: req.Model}, nil
}
