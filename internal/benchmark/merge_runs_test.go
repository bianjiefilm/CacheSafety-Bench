package benchmark

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMergeBenchmarkRunsAggregatesMetricsAndDecisions(t *testing.T) {
	dir := t.TempDir()
	metricsA := filepath.Join(dir, "a_metrics.json")
	metricsB := filepath.Join(dir, "b_metrics.json")
	decisionsA := filepath.Join(dir, "a_decisions.jsonl")
	decisionsB := filepath.Join(dir, "b_decisions.jsonl")
	require.NoError(t, writeJSONFile(metricsA, Metrics{
		Total:                    2,
		SafeHitRate:              0.5,
		BadHitRate:               0.5,
		LayerContribution:        map[string]int{"exact": 1, "miss": 1},
		RefusedByRule:            map[string]int{"format_sensitive": 1},
		JudgeErrorCount:          1,
		UnknownCount:             1,
		SemanticCandidates:       1,
		SemanticRejectedBySafety: 1,
		SemanticJudgedSafe:       0,
		SafeCostSaved:            0.9,
		CachedTokensSaved:        10,
	}))
	require.NoError(t, writeJSONFile(metricsB, Metrics{
		Total:              1,
		SafeHitRate:        1,
		LayerContribution:  map[string]int{"semantic": 1},
		RefusedByRule:      map[string]int{},
		SemanticCandidates: 1,
		SemanticJudgedSafe: 1,
		SafeCostSaved:      0.9,
		CachedTokensSaved:  20,
	}))
	safe := true
	require.NoError(t, WriteDecisionLog(decisionsA, []DecisionRecord{{
		SampleID:              "bad",
		Category:              "hard_trap",
		ExpectedCacheBehavior: "expected_refusal",
		CacheLayer:            "semantic",
		JudgeCacheSafe:        &safe,
		BadHit:                false,
	}}))
	require.NoError(t, WriteDecisionLog(decisionsB, []DecisionRecord{{
		SampleID:              "ok",
		Category:              "general",
		ExpectedCacheBehavior: "semantic_safe",
		CacheLayer:            "semantic",
		JudgeCacheSafe:        &safe,
	}}))

	result, err := MergeBenchmarkRuns(MergeBenchmarkRunsInput{
		MetricsPaths:       []string{metricsA, metricsB},
		DecisionLogPaths:   []string{decisionsA, decisionsB},
		OutputMetricsPath:  filepath.Join(dir, "merged_metrics.json"),
		OutputDecisionPath: filepath.Join(dir, "merged_decisions.jsonl"),
		OutputReportPath:   filepath.Join(dir, "merged.md"),
	})

	require.NoError(t, err)
	require.Equal(t, 3, result.Metrics.Total)
	require.Equal(t, 1, result.Metrics.JudgeErrorCount)
	require.Equal(t, 1, result.Metrics.UnknownCount)
	require.Equal(t, 1, result.HardTrapSafeHitCount)
	require.Equal(t, 1, result.ExpectedRefusalLeaks)
	require.FileExists(t, filepath.Join(dir, "merged_metrics.json"))
	require.FileExists(t, filepath.Join(dir, "merged_decisions.jsonl"))
	require.FileExists(t, filepath.Join(dir, "merged.md"))
}
