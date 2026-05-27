package benchmark

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildCompareReportWarnsOnSampleCountMismatch(t *testing.T) {
	dir := t.TempDir()
	baselineManifest := mustWriteManifestFixture(t, dir, "base", 2, []DecisionRecord{
		fixtureDecision("a", "general", "semantic", 8, 9, false, ""),
		fixtureDecision("b", "general", "miss", 0, 0, false, ""),
	}, nil)
	candidateManifest := mustWriteManifestFixture(t, dir, "cand", 1, []DecisionRecord{
		fixtureDecision("a", "general", "semantic", 8, 9, false, ""),
	}, nil)

	data, markdown, err := BuildCompareReport(context.Background(), CompareReportInput{
		BaselineManifestPath:  baselineManifest,
		CandidateManifestPath: candidateManifest,
	})
	require.NoError(t, err)
	require.False(t, data.TotalSamplesAligned)
	require.Contains(t, markdown, "total samples aligned: `false`")
}

func TestBuildCompareReportListsNewBadHitsAndResolved(t *testing.T) {
	dir := t.TempDir()
	baselineManifest := mustWriteManifestFixture(t, dir, "base", 1, []DecisionRecord{
		fixtureDecision("old", "general", "semantic", 5, 8, false, ""),
	}, nil)
	candidateManifest := mustWriteManifestFixture(t, dir, "cand", 1, []DecisionRecord{
		fixtureDecision("new", "general", "semantic", 5, 8, false, ""),
	}, nil)

	data, markdown, err := BuildCompareReport(context.Background(), CompareReportInput{
		BaselineManifestPath:  baselineManifest,
		CandidateManifestPath: candidateManifest,
	})
	require.NoError(t, err)
	require.Len(t, data.BadHitDiff.New, 1)
	require.Len(t, data.BadHitDiff.Resolved, 1)
	require.Equal(t, "new", data.BadHitDiff.New[0].SampleID)
	require.Equal(t, "old", data.BadHitDiff.Resolved[0].SampleID)
	require.Contains(t, markdown, "new: `1`")
	require.Contains(t, markdown, "resolved: `1`")
}

func TestBuildCompareReportMissingRequestLogDoesNotPanic(t *testing.T) {
	dir := t.TempDir()
	baselineManifest := mustWriteManifestFixture(t, dir, "base", 1, []DecisionRecord{
		fixtureDecision("a", "hard_negative", "miss", 0, 0, false, "format_sensitive"),
	}, &manifestOverrides{requestLogPath: filepath.Join(dir, "missing.db")})
	candidateManifest := mustWriteManifestFixture(t, dir, "cand", 1, []DecisionRecord{
		fixtureDecision("a", "hard_negative", "miss", 0, 0, false, "format_sensitive"),
	}, &manifestOverrides{requestLogPath: filepath.Join(dir, "missing-2.db")})

	data, _, err := BuildCompareReport(context.Background(), CompareReportInput{
		BaselineManifestPath:  baselineManifest,
		CandidateManifestPath: candidateManifest,
	})
	require.NoError(t, err)
	require.False(t, data.BaselineData.Consistency.RequestLogPresent)
	require.False(t, data.CandidateData.Consistency.RequestLogPresent)
}

type manifestOverrides struct {
	requestLogPath string
}

func mustWriteManifestFixture(t *testing.T, dir, prefix string, total int, records []DecisionRecord, overrides *manifestOverrides) string {
	t.Helper()
	metricsPath := filepath.Join(dir, prefix+"_metrics.json")
	decisionPath := filepath.Join(dir, prefix+"_decisions.jsonl")
	manifestPath := filepath.Join(dir, prefix+"_manifest.json")
	requestLogPath := ""
	if overrides != nil {
		requestLogPath = overrides.requestLogPath
	}
	metricsJSON := `{"total":` + strconv.Itoa(total) + `,"layer_contribution":{"semantic":1,"miss":0},"semantic_rejected_by_safety":0,"sampling_mode":"stratified","strata_count":1,"sampled_by_stratum":{"category=general|source_kind=safe_paraphrase|expected_cache_behavior=semantic_safe":1}}`
	require.NoError(t, os.WriteFile(metricsPath, []byte(metricsJSON), 0o644))
	require.NoError(t, WriteDecisionLog(decisionPath, records))
	require.NoError(t, WriteBaselineManifest(manifestPath, BaselineManifest{
		RunID:              prefix + "-run",
		Dataset:            "fixture.jsonl",
		SamplingMode:       "stratified",
		SampleCount:        total,
		StrataCount:        1,
		SampledByStratum:   map[string]int{"category=general|source_kind=safe_paraphrase|expected_cache_behavior=semantic_safe": 1},
		MetricsPath:        metricsPath,
		DecisionLogPath:    decisionPath,
		RequestLogDBPath:   requestLogPath,
		BaselineReportPath: filepath.Join(dir, prefix+"_report.md"),
		CreatedAt:          "2026-05-15T00:00:00Z",
	}))
	return manifestPath
}

func fixtureDecision(id, category, layer string, cached, fresh float64, safe bool, refused string) DecisionRecord {
	record := DecisionRecord{
		SampleID:               id,
		Category:               category,
		SourceKind:             "safe_paraphrase",
		ExpectedCacheBehavior:  "semantic_safe",
		ModelAlias:             "test-model",
		VectorStore:            "sqlite",
		EmbeddingMode:          "fake",
		Threshold:              0.9,
		CacheLayer:             layer,
		SafetyAllowed:          refused == "",
		RefusedReason:          refused,
		JudgeMode:              "fake",
		CachedScore:            cached,
		FreshScore:             fresh,
		JudgeDelta:             cached - fresh,
		EstimatedUpstreamCost:  1,
		SafeCostSaved:          0.9,
		SemanticCandidateFound: layer == "semantic",
	}
	if cached > 0 || fresh > 0 {
		record.JudgeCacheSafe = boolPtr(true)
	}
	if cached > 0 && fresh > 0 && cached < fresh-2 {
		record.BadHit = true
	}
	if !safe && refused != "" {
		record.JudgeCacheSafe = nil
	}
	return record
}
