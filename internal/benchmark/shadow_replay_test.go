package benchmark

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBuildShadowReplayReportFromRecordsAggregates(t *testing.T) {
	safe := true
	unsafe := false
	records := []DecisionRecord{
		{
			SampleID:   "exact-1",
			Category:   "general_explanation",
			CacheLayer: "exact",
		},
		{
			SampleID:                    "semantic-safe",
			Category:                    "rewrite_polish",
			ExpectedCacheBehavior:       "semantic_safe",
			CacheLayer:                  "semantic",
			SemanticCandidateFound:      true,
			JudgeCacheSafe:              &safe,
			CachedPromptTokensSaved:     10,
			CachedCompletionTokensSaved: 20,
			NetCostSavedUSD:             0.01,
			SafeCostSaved:               1,
		},
		{
			SampleID:               "refused-1",
			Category:               "hard_trap",
			ExpectedCacheBehavior:  "expected_refusal",
			CacheLayer:             "miss",
			SemanticCandidateFound: true,
			RefusedReason:          "constraint_mismatch",
		},
		{
			SampleID:               "bad-1",
			Category:               "general_explanation",
			CacheLayer:             "semantic",
			SemanticCandidateFound: true,
			JudgeCacheSafe:         &unsafe,
			BadHit:                 true,
			JudgeError:             "judge_parse_error",
		},
	}

	report := BuildShadowReplayReportFromRecords(records)

	require.Equal(t, 4, report.DecisionLogTotal)
	require.Equal(t, 3, report.WouldCacheHitCount)
	require.Equal(t, 3, report.SemanticCandidates)
	require.Equal(t, 1, report.SemanticJudgedSafe)
	require.Equal(t, 1, report.RefusedCount)
	require.Equal(t, 1, report.BadHitCount)
	require.Equal(t, 1, report.JudgeErrorCount)
	require.Equal(t, 1, report.UnknownCount)
	require.Equal(t, 30, report.CachedTokensSaved)
	require.False(t, report.OfflineShadowReplayClean)
}

func TestShadowReplayCountsExpectedRefusalLeaks(t *testing.T) {
	safe := true
	report := BuildShadowReplayReportFromRecords([]DecisionRecord{{
		SampleID:              "leak-1",
		Category:              "hard_trap",
		ExpectedCacheBehavior: "expected_refusal",
		CacheLayer:            "semantic",
		JudgeCacheSafe:        &safe,
	}})

	require.Equal(t, 1, report.ExpectedRefusalLeakCount)
	require.Equal(t, 1, report.HardTrapSafeHitCount)
	require.False(t, report.OfflineShadowReplayClean)
}

func TestBuildShadowReplayReportMissingRequestLogDoesNotPanic(t *testing.T) {
	dir := t.TempDir()
	decisionPath := filepath.Join(dir, "decisions.jsonl")
	require.NoError(t, WriteDecisionLog(decisionPath, []DecisionRecord{{SampleID: "a", CacheLayer: "miss"}}))

	report, markdown, err := BuildShadowReplayReport(context.Background(), ShadowReplayInput{
		DecisionLogPath: decisionPath,
		RequestLogDB:    filepath.Join(dir, "missing.db"),
	})

	require.NoError(t, err)
	require.False(t, report.RequestLogPresent)
	require.False(t, report.RequestLogConsistent)
	require.Len(t, report.Warnings, 1)
	require.Contains(t, markdown, "request_log_missing")
}

func TestBuildShadowReplayReportWithRequestLogConsistency(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	decisionPath := filepath.Join(dir, "decisions.jsonl")
	records := []DecisionRecord{
		{SampleID: "a", CacheLayer: "miss"},
		{SampleID: "b", CacheLayer: "exact"},
	}
	require.NoError(t, WriteDecisionLog(decisionPath, records))
	dbPath := filepath.Join(dir, "request_log.db")
	store, err := OpenRequestLogStore(ctx, dbPath)
	require.NoError(t, err)
	defer func() { _ = store.Close() }()
	require.NoError(t, store.Write(ctx, RequestLogWrite{
		Run: BenchmarkRunLog{
			RunID:      "run-1",
			Dataset:    "dataset.jsonl",
			StartedAt:  time.Now().UTC(),
			FinishedAt: time.Now().UTC(),
		},
		Metrics: Metrics{Total: 2},
		Records: records,
	}))

	report, markdown, err := BuildShadowReplayReport(ctx, ShadowReplayInput{
		DecisionLogPath: decisionPath,
		RequestLogDB:    dbPath,
		RunID:           "run-1",
	})

	require.NoError(t, err)
	require.True(t, report.RequestLogPresent)
	require.True(t, report.RequestLogConsistent)
	require.Equal(t, 2, report.RequestLogTotal)
	require.Contains(t, markdown, "consistent: `true`")
}

func TestBuildShadowReplayReportSumsChunkedRequestLogs(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	decisionPath := filepath.Join(dir, "decisions.jsonl")
	records := []DecisionRecord{
		{SampleID: "a", CacheLayer: "miss"},
		{SampleID: "b", CacheLayer: "exact"},
	}
	require.NoError(t, WriteDecisionLog(decisionPath, records))
	dbA := writeShadowReplayRequestLogChunk(t, ctx, dir, "a.db", "run-a", records[:1])
	dbB := writeShadowReplayRequestLogChunk(t, ctx, dir, "b.db", "run-b", records[1:])

	report, _, err := BuildShadowReplayReport(ctx, ShadowReplayInput{
		DecisionLogPath: decisionPath,
		RequestLogDBs:   []string{dbA, dbB},
	})

	require.NoError(t, err)
	require.True(t, report.RequestLogPresent)
	require.True(t, report.RequestLogConsistent)
	require.Equal(t, 2, report.RequestLogTotal)
	require.Equal(t, "multiple", report.RequestLogRunID)
}

func TestShadowReplayMarkdownDoesNotExposeSensitiveFields(t *testing.T) {
	safe := true
	report := BuildShadowReplayReportFromRecords([]DecisionRecord{{
		SampleID:               "a",
		ModelAlias:             "doubao-seed-sensitive-model",
		CacheLayer:             "semantic",
		JudgeCacheSafe:         &safe,
		CandidateAnswerPreview: "secret answer preview",
	}})

	markdown := RenderShadowReplayMarkdown(report)

	require.NotContains(t, markdown, "doubao-seed-sensitive-model")
	require.NotContains(t, markdown, "secret answer preview")
}

func writeShadowReplayRequestLogChunk(t *testing.T, ctx context.Context, dir string, name string, runID string, records []DecisionRecord) string {
	t.Helper()
	dbPath := filepath.Join(dir, name)
	store, err := OpenRequestLogStore(ctx, dbPath)
	require.NoError(t, err)
	defer func() { _ = store.Close() }()
	require.NoError(t, store.Write(ctx, RequestLogWrite{
		Run: BenchmarkRunLog{
			RunID:      runID,
			Dataset:    "dataset.jsonl",
			StartedAt:  time.Now().UTC(),
			FinishedAt: time.Now().UTC(),
		},
		Metrics: Metrics{Total: len(records)},
		Records: records,
	}))
	return dbPath
}
