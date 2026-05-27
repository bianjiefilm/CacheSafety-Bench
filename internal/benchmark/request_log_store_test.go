package benchmark

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRequestLogStoreSchemaInsertAndQueries(t *testing.T) {
	ctx := context.Background()
	store := openTestRequestLogStore(t)
	defer func() { _ = store.Close() }()
	cacheSafe := true
	cacheUnsafe := false
	records := []DecisionRecord{
		{
			SampleID:               "miss-1",
			Category:               "seed",
			ExpectedRisk:           "low",
			SourceKind:             "safe_paraphrase",
			ExpectedCacheBehavior:  "unspecified",
			ModelAlias:             "benchmark-model",
			CacheLayer:             "miss",
			RequestPreview:         "Explain prompt caching.",
			SafetyAllowed:          true,
			JudgeMode:              "fake",
			EstimatedUpstreamCost:  1,
			CandidateAnswerPreview: strings.Repeat("x", 250),
		},
		{
			SampleID:               "safe-1",
			Category:               "general",
			ExpectedRisk:           "low",
			SourceKind:             "safe_paraphrase",
			ExpectedCacheBehavior:  "semantic_safe",
			ModelAlias:             "benchmark-model",
			CacheLayer:             "semantic",
			RequestPreview:         "Describe prompt caching.",
			SemanticCandidateFound: true,
			Similarity:             0.96,
			SafetyAllowed:          true,
			JudgeMode:              "fake",
			JudgeCacheSafe:         &cacheSafe,
			CachedScore:            8,
			FreshScore:             9,
			JudgeDelta:             -1,
			EstimatedUpstreamCost:  1,
			SafeCostSaved:          0.9,
		},
		{
			SampleID:               "refused-1",
			Category:               "hard_negative",
			ExpectedRisk:           "guard_refuse",
			SourceKind:             "regression",
			ExpectedCacheBehavior:  "expected_refusal",
			ModelAlias:             "benchmark-model",
			CacheLayer:             "miss",
			RequestPreview:         "Answer with one word.",
			CandidatePromptPreview: "Explain.",
			SemanticCandidateFound: true,
			Similarity:             0.91,
			SafetyAllowed:          false,
			RefusedReason:          "format_sensitive",
			RefusedDetail:          "format_sensitive:one_word",
			JudgeMode:              "fake",
			EstimatedUpstreamCost:  1,
		},
		{
			SampleID:               "bad-1",
			Category:               "trap",
			SourceKind:             "rule_based",
			ExpectedCacheBehavior:  "semantic_safe",
			ModelAlias:             "benchmark-model",
			CacheLayer:             "semantic",
			SemanticCandidateFound: true,
			Similarity:             0.92,
			SafetyAllowed:          true,
			JudgeMode:              "fake",
			JudgeCacheSafe:         &cacheUnsafe,
			BadHit:                 true,
			EstimatedUpstreamCost:  1,
		},
		{
			SampleID:               "unknown-1",
			Category:               "general",
			SourceKind:             "llm_paraphrase",
			ExpectedCacheBehavior:  "semantic_safe",
			ModelAlias:             "benchmark-model",
			CacheLayer:             "semantic",
			SemanticCandidateFound: true,
			Similarity:             0.95,
			SafetyAllowed:          true,
			JudgeMode:              "fake",
			JudgeError:             "judge_parse_error",
			EstimatedUpstreamCost:  1,
		},
	}
	metrics := Metrics{Total: 5, BadHitRate: 0.2, SafeCostSaved: 0.9, VectorStore: "memory", VectorDim: 2}
	run := testBenchmarkRunLog("run-1")

	require.NoError(t, store.Write(ctx, RequestLogWrite{Run: run, Metrics: metrics, Records: records}))

	runs, err := store.ListRuns(ctx)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.Equal(t, "run-1", runs[0].RunID)
	var decoded Metrics
	require.NoError(t, json.Unmarshal(runs[0].MetricsJSON, &decoded))
	require.Equal(t, 5, decoded.Total)
	require.Equal(t, 0.9, decoded.SafeCostSaved)

	summary, err := store.Summary(ctx, "run-1")
	require.NoError(t, err)
	require.Equal(t, 5, summary.Total)
	require.Equal(t, 4, summary.SemanticCandidates)
	require.Equal(t, 1, summary.SemanticJudgedSafe)
	require.Equal(t, 1, summary.SemanticRefused)
	require.Equal(t, 1, summary.BadHitCount)
	require.Equal(t, 1, summary.UnknownCount)
	require.Equal(t, 0.2, summary.BadHitRate)
	require.Equal(t, 0.9, summary.SafeCostSaved)
	require.Equal(t, 1, summary.ByRefusedReason["format_sensitive"])
	require.Equal(t, 2, summary.BySourceKind["safe_paraphrase"])
	require.Equal(t, 3, summary.ByExpectedCacheBehavior["semantic_safe"])
	require.Equal(t, 1, summary.ByCategory["general"].SemanticJudgedSafe)

	badHits, err := store.BadHits(ctx, "run-1")
	require.NoError(t, err)
	require.Len(t, badHits, 1)
	require.Equal(t, "bad-1", badHits[0].SampleID)

	refusals, err := store.Refusals(ctx, "run-1")
	require.NoError(t, err)
	require.Len(t, refusals, 1)
	require.Equal(t, "format_sensitive", refusals[0].RefusedReason)
}

func TestRequestLogStoreOverwritesRunIDAndTruncatesPreviews(t *testing.T) {
	ctx := context.Background()
	path := testDBPath(t)
	store, err := OpenRequestLogStore(ctx, path)
	require.NoError(t, err)
	defer func() { _ = store.Close() }()
	run := testBenchmarkRunLog("same-run")
	longPreview := strings.Repeat("a", 250)

	require.NoError(t, store.Write(ctx, RequestLogWrite{Run: run, Metrics: Metrics{Total: 1}, Records: []DecisionRecord{{
		SampleID:               "first",
		ModelAlias:             ModelAlias("sensitive-real-model"),
		CacheLayer:             "miss",
		RequestPreview:         longPreview,
		CandidateAnswerPreview: longPreview,
		SafetyAllowed:          true,
	}}}))
	require.NoError(t, store.Write(ctx, RequestLogWrite{Run: run, Metrics: Metrics{Total: 1}, Records: []DecisionRecord{{
		SampleID:       "second",
		ModelAlias:     ModelAlias("sensitive-real-model"),
		CacheLayer:     "miss",
		RequestPreview: longPreview,
		SafetyAllowed:  true,
	}}}))

	var count int
	require.NoError(t, store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM request_log WHERE run_id = ?`, "same-run").Scan(&count))
	require.Equal(t, 1, count)
	var sampleID, modelAlias, requestPreview string
	require.NoError(t, store.db.QueryRowContext(ctx, `SELECT sample_id, model_alias, request_preview FROM request_log WHERE run_id = ?`, "same-run").Scan(&sampleID, &modelAlias, &requestPreview))
	require.Equal(t, "second", sampleID)
	require.Len(t, []rune(requestPreview), 200)
	require.NotContains(t, modelAlias, "sensitive-real-model")
	payload, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NotContains(t, string(payload), "sensitive-real-model")
}

func TestDefaultRunIDIsReadable(t *testing.T) {
	got := DefaultRunID("testdata/benchmark/safe_paraphrase_eval_v0.jsonl", time.Date(2026, 5, 15, 8, 7, 6, 0, time.UTC))

	require.Equal(t, "20260515T080706Z_safe_paraphrase_eval_v0", got)
}

func openTestRequestLogStore(t *testing.T) *RequestLogStore {
	t.Helper()
	store, err := OpenRequestLogStore(context.Background(), testDBPath(t))
	require.NoError(t, err)
	return store
}

func testDBPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "request-log.db")
}

func testBenchmarkRunLog(runID string) BenchmarkRunLog {
	return BenchmarkRunLog{
		RunID:         runID,
		Dataset:       "dataset.jsonl",
		ProviderMode:  "fake",
		JudgeMode:     "fake",
		EmbeddingMode: "fake",
		VectorStore:   "memory",
		VectorDim:     2,
		Threshold:     0.9,
		Seed:          42,
		StartedAt:     time.Date(2026, 5, 15, 8, 0, 0, 0, time.UTC),
		FinishedAt:    time.Date(2026, 5, 15, 8, 1, 0, 0, time.UTC),
	}
}
