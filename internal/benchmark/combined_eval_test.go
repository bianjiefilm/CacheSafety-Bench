package benchmark

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildCombinedEvalReadsJSONLAndRegression(t *testing.T) {
	dir := t.TempDir()
	jsonlPath := filepath.Join(dir, "pairs.jsonl")
	regressionPath := filepath.Join(dir, "refusal.json")

	cases := []Case{
		{ID: "a", Category: "general_explanation", ExpectedRisk: "low", Request: testBenchRequest("Explain REST API."), OldRequest: "What is a REST API?", OldAnswer: "A REST API lets systems exchange data.", ExpectedCacheBehavior: "semantic_safe", PairGenerationMode: "rule_based"},
	}
	require.NoError(t, WriteImportedEvalDataset(jsonlPath, cases))
	regression := regressionCase{
		CaseID:                "reg-1",
		Category:              "hard_negative",
		ExpectedRisk:          "high",
		OldRequest:            "Describe whether a request looks safe.",
		CachedAnswer:          "Long explanation.",
		NewRequest:            "Answer with one word whether the request looks safe.",
		FreshAnswer:           "safe",
		ExpectedRefusedReason: "format_sensitive:one_word",
		ExpectedCacheSafe:     false,
	}
	bytes, err := json.Marshal(regression)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(regressionPath, bytes, 0o644))

	got, report, err := BuildCombinedEval(BuildCombinedEvalOptions{
		Sources:           []string{jsonlPath, regressionPath},
		OutputPath:        filepath.Join(dir, "combined.jsonl"),
		Dedupe:            true,
		IncludeRegression: true,
	})
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, 1, report.RegressionCount)
	require.Equal(t, "regression", got[1].SourceKind)
	require.Equal(t, "expected_refusal", got[1].ExpectedCacheBehavior)
}

func TestBuildCombinedEvalDedupePrefersRegression(t *testing.T) {
	dir := t.TempDir()
	jsonlPath := filepath.Join(dir, "pairs.jsonl")
	regressionPath := filepath.Join(dir, "refusal.json")
	cases := []Case{
		{ID: "dup", Category: "hard_negative", ExpectedRisk: "high", Request: testBenchRequest("Answer with one word whether the request looks safe."), OldRequest: "Describe whether a request looks safe.", OldAnswer: "Long explanation.", ExpectedCacheBehavior: "expected_refusal", PairGenerationMode: "rule_based"},
	}
	require.NoError(t, WriteImportedEvalDataset(jsonlPath, cases))
	regression := regressionCase{
		CaseID:                "reg-dup",
		Category:              "hard_negative",
		ExpectedRisk:          "high",
		OldRequest:            "Describe whether a request looks safe.",
		CachedAnswer:          "Long explanation.",
		NewRequest:            "Answer with one word whether the request looks safe.",
		FreshAnswer:           "safe",
		ExpectedRefusedReason: "format_sensitive:one_word",
		ExpectedCacheSafe:     false,
	}
	bytes, err := json.Marshal(regression)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(regressionPath, bytes, 0o644))

	got, report, err := BuildCombinedEval(BuildCombinedEvalOptions{
		Sources:           []string{jsonlPath, regressionPath},
		OutputPath:        filepath.Join(dir, "combined.jsonl"),
		Dedupe:            true,
		IncludeRegression: true,
	})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, 1, report.DuplicateRemoved)
	require.Equal(t, "regression", got[0].SourceKind)
}

func TestBuildCombinedEvalReportAndLoadable(t *testing.T) {
	dir := t.TempDir()
	jsonlPath := filepath.Join(dir, "pairs.jsonl")
	outputPath := filepath.Join(dir, "combined.jsonl")
	cases := []Case{
		{ID: "a", Category: "general_explanation", ExpectedRisk: "low", Request: testBenchRequest("Explain REST API."), OldRequest: "What is a REST API?", OldAnswer: "A REST API lets systems exchange data.", ExpectedCacheBehavior: "semantic_safe", PairGenerationMode: "rule_based"},
		{ID: "b", Category: "hard_negative", ExpectedRisk: "high", Request: testBenchRequest("What is the latest stock outlook today?"), OldRequest: "What is the stock outlook?", OldAnswer: "Answer requires fresh data.", ExpectedCacheBehavior: "expected_refusal", PairGenerationMode: "rule_based"},
	}
	require.NoError(t, WriteImportedEvalDataset(jsonlPath, cases))

	got, report, err := BuildCombinedEval(BuildCombinedEvalOptions{
		Sources:           []string{jsonlPath},
		OutputPath:        outputPath,
		Dedupe:            true,
		IncludeRegression: true,
	})
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.NoError(t, WriteImportedEvalDataset(outputPath, got))
	loaded, err := LoadJSONL(outputPath)
	require.NoError(t, err)
	require.Len(t, loaded, 2)
	reportPath, err := WriteCombinedEvalReport(outputPath, report)
	require.NoError(t, err)
	require.FileExists(t, reportPath)
	markdownReportPath, err := WriteCombinedEvalMarkdownReport(outputPath, report)
	require.NoError(t, err)
	require.FileExists(t, markdownReportPath)
	reportBytes, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	require.NotContains(t, string(reportBytes), "ark-346fd29f")
}

func TestBuildCombinedEvalCanAppendBoundarySamples(t *testing.T) {
	dir := t.TempDir()
	jsonlPath := filepath.Join(dir, "pairs.jsonl")
	outputPath := filepath.Join(dir, "combined.jsonl")
	require.NoError(t, WriteImportedEvalDataset(jsonlPath, []Case{
		{ID: "a", Category: "general_explanation", ExpectedRisk: "low", Request: testBenchRequest("Explain REST API."), OldRequest: "What is a REST API?", OldAnswer: "A REST API lets systems exchange data.", ExpectedCacheBehavior: "semantic_safe", PairGenerationMode: "rule_based"},
	}))

	got, report, err := BuildCombinedEval(BuildCombinedEvalOptions{
		Sources:           []string{jsonlPath},
		OutputPath:        outputPath,
		Dedupe:            true,
		IncludeRegression: true,
		IncludeBoundaryV0: true,
	})

	require.NoError(t, err)
	require.Greater(t, len(got), 120)
	require.Greater(t, report.BoundarySampleCount, 120)
	require.Greater(t, report.HardNegativeCount, 0)
	require.Equal(t, report.BoundarySampleCount, report.BySourceKind["boundary"])
}

func TestBuildCombinedEvalSkipsUnconvertibleLegacyBadHits(t *testing.T) {
	dir := t.TempDir()
	badHitDir := filepath.Join(dir, "bad_hits")
	require.NoError(t, os.MkdirAll(badHitDir, 0o755))
	legacyPath := filepath.Join(badHitDir, "threshold_0.90.jsonl")
	legacy := `{"id":"legacy","category":"rewrite_polish","old_request":"Explain idempotency.","new_request":"Rewrite this sentence.","cached_answer":"Idempotency means safe retries.","fresh_answer":"The team reviewed the report.","request":{"model":"benchmark-provider-model","messages":[{"role":"user","content":"Rewrite this sentence."}]}}`
	require.NoError(t, os.WriteFile(legacyPath, []byte(legacy+"\n"), 0o644))

	got, report, err := BuildCombinedEval(BuildCombinedEvalOptions{
		Sources:           []string{legacyPath},
		OutputPath:        filepath.Join(dir, "combined.jsonl"),
		Dedupe:            true,
		IncludeRegression: true,
	})

	require.NoError(t, err)
	require.Empty(t, got)
	require.Equal(t, 1, report.LegacyBadHitExamples)
	require.Equal(t, 0, report.LegacyBadHitConverted)
	require.Equal(t, 1, report.LegacyBadHitKeptAsExamples)
}

func TestNormalizeSafeParaphraseCasesPairsHardNegativeTrap(t *testing.T) {
	items := []Case{
		{ID: "seed", Kind: "seed", Category: "hard_negative", Request: testBenchRequest("Refund requests are accepted for 7 days."), FreshAnswer: "Refund requests are accepted for 7 days."},
		{ID: "trap", Kind: "semantic_trap", Category: "hard_negative", Request: testBenchRequest("Refund requests are accepted for 30 days."), FreshAnswer: "Refund requests are accepted for 30 days."},
	}
	got := normalizeSafeParaphraseCases(items)
	require.Len(t, got, 1)
	require.Equal(t, "trap", got[0].ID)
	require.Equal(t, "Refund requests are accepted for 7 days.", got[0].OldRequest)
	require.Equal(t, "expected_refusal", got[0].ExpectedCacheBehavior)
	require.Contains(t, got[0].ExpectedRefusedReason, "constraint_mismatch")
}
