package benchmark

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHardTrapSamplesV1Coverage(t *testing.T) {
	items := HardTrapSamplesV1()

	require.GreaterOrEqual(t, len(items), 80)
	byTrap := map[string]int{}
	for _, item := range items {
		require.NotEmpty(t, item.OldRequest)
		require.NotEmpty(t, item.OldAnswer)
		require.NotEmpty(t, item.TrapType)
		require.NotEmpty(t, item.GuardExpected)
		require.NotEqual(t, "semantic_safe", item.ExpectedCacheBehavior)
		require.NotEmpty(t, item.Request.Messages)
		byTrap[item.TrapType]++
	}
	for _, trapType := range []string{
		"negation_trap",
		"number_trap",
		"money_trap",
		"duration_deadline_trap",
		"entity_trap",
		"region_jurisdiction_trap",
		"format_trap",
	} {
		require.Greater(t, byTrap[trapType], 0, trapType)
	}
}

func TestBuildGoldenDatasetWritesReportableCoverage(t *testing.T) {
	dir := t.TempDir()
	combinedPath := filepath.Join(dir, "combined.jsonl")
	require.NoError(t, WriteImportedEvalDataset(combinedPath, []Case{
		{
			ID:                    "safe",
			Category:              "general_explanation",
			ExpectedRisk:          "low",
			Request:               testBenchRequest("Explain REST."),
			FreshAnswer:           "REST is an API style.",
			TeacherScore:          10,
			FreshCost:             1,
			CachedCost:            0.1,
			OldRequest:            "What is REST?",
			OldAnswer:             "REST is an API style.",
			ExpectedCacheBehavior: "semantic_safe",
			SourceKind:            "fixture",
		},
		{
			ID:                    "hard",
			Category:              "hard_negative",
			ExpectedRisk:          "guard_refuse",
			Request:               testBenchRequest("Reply exactly: safe"),
			FreshAnswer:           "safe",
			TeacherScore:          10,
			FreshCost:             1,
			CachedCost:            0.1,
			OldRequest:            "Describe whether this is safe.",
			OldAnswer:             "This is safe.",
			ExpectedCacheBehavior: "expected_refusal",
			ExpectedRefusedReason: "format_sensitive",
			SourceKind:            "fixture",
		},
	}))

	items, report, err := BuildGoldenDataset(BuildGoldenDatasetOptions{
		CombinedEvalPath: combinedPath,
		OutputPath:       filepath.Join(dir, "golden.jsonl"),
		Dedupe:           true,
	})

	require.NoError(t, err)
	require.Len(t, items, 2+len(HardTrapSamplesV1()))
	require.Equal(t, len(items), report.Total)
	require.GreaterOrEqual(t, report.HardTrapCount, 1+len(HardTrapSamplesV1()))
	require.Equal(t, 1, report.SafePairCount)
	require.Greater(t, report.ByTrapType["format_trap"], 0)
	require.Equal(t, 0, report.MissingGuardExpectationCount)
}

func TestGoldenDatasetDedupe(t *testing.T) {
	dir := t.TempDir()
	combinedPath := filepath.Join(dir, "combined.jsonl")
	duplicate := Case{
		ID:                    "dup",
		Category:              "general_explanation",
		ExpectedRisk:          "low",
		Request:               testBenchRequest("Explain JWT."),
		FreshAnswer:           "JWT is a signed token.",
		TeacherScore:          10,
		FreshCost:             1,
		CachedCost:            0.1,
		OldRequest:            "What is JWT?",
		OldAnswer:             "JWT is a signed token.",
		ExpectedCacheBehavior: "semantic_safe",
		SourceKind:            "fixture",
	}
	require.NoError(t, WriteImportedEvalDataset(combinedPath, []Case{duplicate, duplicate}))

	items, report, err := BuildGoldenDataset(BuildGoldenDatasetOptions{
		CombinedEvalPath: combinedPath,
		OutputPath:       filepath.Join(dir, "golden.jsonl"),
		Dedupe:           true,
	})

	require.NoError(t, err)
	require.Len(t, items, 1+len(HardTrapSamplesV1()))
	require.Equal(t, 1, report.DuplicateRemoved)
}

func TestGoldenDatasetOutputIsBenchmarkReadable(t *testing.T) {
	dir := t.TempDir()
	combinedPath := filepath.Join(dir, "combined.jsonl")
	outputPath := filepath.Join(dir, "golden.jsonl")
	require.NoError(t, WriteImportedEvalDataset(combinedPath, []Case{{
		ID:                    "safe",
		Category:              "general_explanation",
		ExpectedRisk:          "low",
		Request:               testBenchRequest("Explain cache."),
		FreshAnswer:           "Cache stores reusable data.",
		TeacherScore:          10,
		FreshCost:             1,
		CachedCost:            0.1,
		OldRequest:            "What is cache?",
		OldAnswer:             "Cache stores reusable data.",
		ExpectedCacheBehavior: "semantic_safe",
	}}))
	items, _, err := BuildGoldenDataset(BuildGoldenDatasetOptions{
		CombinedEvalPath: combinedPath,
		OutputPath:       outputPath,
		Dedupe:           true,
	})
	require.NoError(t, err)
	require.NoError(t, WriteImportedEvalDataset(outputPath, items))

	loaded, err := LoadJSONL(outputPath)
	require.NoError(t, err)
	require.Len(t, loaded, len(items))
}
