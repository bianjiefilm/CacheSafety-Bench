package benchmark

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildBaselineReportEmptyInputsNoPanic(t *testing.T) {
	dir := t.TempDir()
	metricsPath := filepath.Join(dir, "metrics.json")
	decisionPath := filepath.Join(dir, "decisions.jsonl")
	require.NoError(t, os.WriteFile(metricsPath, []byte(`{}`), 0o644))
	require.NoError(t, os.WriteFile(decisionPath, []byte(""), 0o644))

	data, markdown, err := BuildBaselineReport(context.Background(), BaselineReportInput{
		MetricsPath:     metricsPath,
		DecisionLogPath: decisionPath,
	})
	require.NoError(t, err)
	require.Equal(t, 0, data.Metrics.Total)
	require.Equal(t, 0, data.DecisionSummary.Total)
	require.Contains(t, markdown, "Sampling Summary")
}

func TestBuildBaselineReportMissingRequestLogDoesNotPanic(t *testing.T) {
	dir := t.TempDir()
	metricsPath := filepath.Join(dir, "metrics.json")
	decisionPath := filepath.Join(dir, "decisions.jsonl")
	require.NoError(t, os.WriteFile(metricsPath, []byte(`{"total":1}`), 0o644))
	require.NoError(t, os.WriteFile(decisionPath, []byte(`{"sample_id":"a","cache_layer":"miss","judge_mode":"fake","model":"test-model","vector_store":"memory","embedding_mode":"fake","threshold":0.9,"safety_allowed":true,"bad_hit":false,"estimated_upstream_cost":1,"safe_cost_saved":0}`+"\n"), 0o644))

	data, _, err := BuildBaselineReport(context.Background(), BaselineReportInput{
		MetricsPath:     metricsPath,
		DecisionLogPath: decisionPath,
		RequestLogDB:    filepath.Join(dir, "missing.db"),
		RunID:           "run-1",
	})
	require.NoError(t, err)
	require.Nil(t, data.RequestLogSummary)
	require.False(t, data.Consistency.RequestLogPresent)
}

func TestBuildBaselineReportWithRequestLogConsistency(t *testing.T) {
	dir := t.TempDir()
	metricsPath := filepath.Join(dir, "metrics.json")
	decisionPath := filepath.Join(dir, "decisions.jsonl")
	dbPath := filepath.Join(dir, "request_log.db")
	require.NoError(t, os.WriteFile(metricsPath, []byte(`{"total":1,"semantic_judged_safe":1}`), 0o644))
	require.NoError(t, os.WriteFile(decisionPath, []byte(`{"sample_id":"a","category":"general","source_kind":"safe_paraphrase","expected_cache_behavior":"semantic_safe","cache_layer":"semantic","judge_mode":"fake","model":"test-model","vector_store":"memory","embedding_mode":"fake","threshold":0.9,"semantic_candidate_found":true,"safety_allowed":true,"judge_cache_safe":true,"cached_score":8,"fresh_score":9,"judge_delta":-1,"bad_hit":false,"estimated_upstream_cost":1,"safe_cost_saved":0.9}`+"\n"), 0o644))

	store, err := OpenRequestLogStore(context.Background(), dbPath)
	require.NoError(t, err)
	defer func() { _ = store.Close() }()
	require.NoError(t, store.Write(context.Background(), RequestLogWrite{
		Run:     testBenchmarkRunLog("run-1"),
		Metrics: Metrics{Total: 1, SemanticJudgedSafe: 1},
		Records: []DecisionRecord{{
			SampleID:               "a",
			Category:               "general",
			SourceKind:             "safe_paraphrase",
			ExpectedCacheBehavior:  "semantic_safe",
			ModelAlias:             "test-model",
			CacheLayer:             "semantic",
			SemanticCandidateFound: true,
			SafetyAllowed:          true,
			JudgeMode:              "fake",
			JudgeCacheSafe:         boolPtr(true),
			CachedScore:            8,
			FreshScore:             9,
			JudgeDelta:             -1,
			EstimatedUpstreamCost:  1,
			SafeCostSaved:          0.9,
		}},
	}))

	data, markdown, err := BuildBaselineReport(context.Background(), BaselineReportInput{
		MetricsPath:     metricsPath,
		DecisionLogPath: decisionPath,
		RequestLogDB:    dbPath,
		RunID:           "run-1",
	})
	require.NoError(t, err)
	require.NotNil(t, data.RequestLogSummary)
	require.True(t, data.Consistency.Matches)
	require.Contains(t, markdown, "Consistency Check")
}

func boolPtr(value bool) *bool {
	return &value
}
