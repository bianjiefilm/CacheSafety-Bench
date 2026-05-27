package benchmark

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTop1DiagnosticsAlignsDecisionLogsByDisambiguatedSampleKey(t *testing.T) {
	dir := t.TempDir()
	floatPath := filepath.Join(dir, "float.jsonl")
	int8Path := filepath.Join(dir, "int8.jsonl")
	require.NoError(t, WriteDecisionLog(floatPath, []DecisionRecord{
		top1Fixture("same", "rule_based", "prompt A", "miss", "", "", 0, 0),
		top1Fixture("same", "llm_paraphrase", "prompt B", "semantic", "old B", "answer B", 8, 9),
	}))
	require.NoError(t, WriteDecisionLog(int8Path, []DecisionRecord{
		top1Fixture("same", "rule_based", "prompt A", "miss", "", "", 0, 0),
		top1Fixture("same", "llm_paraphrase", "prompt B", "semantic", "old B", "answer B", 8, 9),
	}))

	report, _, err := BuildTop1Diagnostics(context.Background(), Top1DiagnosticsInput{
		FloatDecisionLogPath: floatPath,
		Int8DecisionLogPath:  int8Path,
	})
	require.NoError(t, err)
	require.Equal(t, 2, report.TotalAlignedSamples)
	require.Empty(t, report.Warnings)
}

func TestTop1DiagnosticsDetectsCandidatePromptAndAnswerChange(t *testing.T) {
	dir := t.TempDir()
	floatPath := filepath.Join(dir, "float.jsonl")
	int8Path := filepath.Join(dir, "int8.jsonl")
	require.NoError(t, WriteDecisionLog(floatPath, []DecisionRecord{
		top1Fixture("a", "llm_paraphrase", "prompt", "semantic", "old prompt", "long helpful answer", 8, 9),
	}))
	require.NoError(t, WriteDecisionLog(int8Path, []DecisionRecord{
		top1Fixture("a", "llm_paraphrase", "prompt", "semantic", "different prompt", "short answer", 6, 9),
	}))

	report, _, err := BuildTop1Diagnostics(context.Background(), Top1DiagnosticsInput{
		FloatDecisionLogPath: floatPath,
		Int8DecisionLogPath:  int8Path,
	})
	require.NoError(t, err)
	require.Equal(t, 1, report.SemanticTop1ChangedCount)
	require.Equal(t, 1, report.AnswerChangedCount)
	require.True(t, report.Records[0].CandidatePromptChanged)
	require.True(t, report.Records[0].CandidateAnswerChanged)
}

func TestTop1DiagnosticsCountsScoreWorsenedAndWarnings(t *testing.T) {
	dir := t.TempDir()
	floatPath := filepath.Join(dir, "float.jsonl")
	int8Path := filepath.Join(dir, "int8.jsonl")
	require.NoError(t, WriteDecisionLog(floatPath, []DecisionRecord{
		top1Fixture("a", "llm_paraphrase", "prompt A", "semantic", "old A", "answer A", 8, 9),
		top1Fixture("missing", "llm_paraphrase", "prompt missing", "semantic", "old missing", "answer missing", 8, 9),
	}))
	require.NoError(t, WriteDecisionLog(int8Path, []DecisionRecord{
		top1Fixture("a", "llm_paraphrase", "prompt A", "semantic", "old A", "answer A", 6, 9),
	}))

	report, _, err := BuildTop1Diagnostics(context.Background(), Top1DiagnosticsInput{
		FloatDecisionLogPath: floatPath,
		Int8DecisionLogPath:  int8Path,
	})
	require.NoError(t, err)
	require.Equal(t, 1, report.ScoreWorsenedCount)
	require.Len(t, report.Warnings, 1)
	require.Equal(t, "missing_int8_sample", report.Warnings[0].Kind)
}

func TestTop1DiagnosticsMarkdownIncludesTopKSection(t *testing.T) {
	dir := t.TempDir()
	floatPath := filepath.Join(dir, "float.jsonl")
	int8Path := filepath.Join(dir, "int8.jsonl")
	require.NoError(t, WriteDecisionLog(floatPath, []DecisionRecord{
		top1Fixture("a", "llm_paraphrase", "prompt", "semantic", "old", "answer", 8, 9),
	}))
	require.NoError(t, WriteDecisionLog(int8Path, []DecisionRecord{
		top1Fixture("a", "llm_paraphrase", "prompt", "semantic", "old", "answer", 8, 9),
	}))

	report, markdown, err := BuildTop1Diagnostics(context.Background(), Top1DiagnosticsInput{
		FloatDecisionLogPath: floatPath,
		Int8DecisionLogPath:  int8Path,
		FocusSampleIDs:       []string{"a"},
	})
	require.NoError(t, err)
	require.False(t, report.TopKAvailable)
	require.Contains(t, markdown, "## TopK Diagnostics")
	require.Contains(t, markdown, "topK available: `false`")
}

func TestTop1DiagnosticsDoesNotWriteSensitiveModelFields(t *testing.T) {
	dir := t.TempDir()
	floatPath := filepath.Join(dir, "float.jsonl")
	int8Path := filepath.Join(dir, "int8.jsonl")
	outputJSON := filepath.Join(dir, "diagnostics.json")
	outputMD := filepath.Join(dir, "diagnostics.md")
	record := top1Fixture("a", "llm_paraphrase", "prompt", "semantic", "old", "answer", 8, 9)
	record.ModelAlias = "doubao-seed-sensitive-model"
	require.NoError(t, WriteDecisionLog(floatPath, []DecisionRecord{record}))
	require.NoError(t, WriteDecisionLog(int8Path, []DecisionRecord{record}))

	report, markdown, err := BuildTop1Diagnostics(context.Background(), Top1DiagnosticsInput{
		FloatDecisionLogPath: floatPath,
		Int8DecisionLogPath:  int8Path,
	})
	require.NoError(t, err)
	require.NoError(t, WriteTop1DiagnosticsJSON(outputJSON, report))
	require.NoError(t, WriteTop1DiagnosticsMarkdown(outputMD, markdown))
	jsonPayload, err := os.ReadFile(outputJSON)
	require.NoError(t, err)
	markdownPayload, err := os.ReadFile(outputMD)
	require.NoError(t, err)
	require.NotContains(t, string(jsonPayload), "doubao-seed-sensitive-model")
	require.NotContains(t, string(markdownPayload), "doubao-seed-sensitive-model")
}

func top1Fixture(sampleID, sourceKind, requestPreview, layer, candidatePrompt, candidateAnswer string, cachedScore, freshScore float64) DecisionRecord {
	record := DecisionRecord{
		SampleID:               sampleID,
		Category:               "howto_guidance",
		ExpectedRisk:           "low",
		SourceKind:             sourceKind,
		ExpectedCacheBehavior:  "semantic_safe",
		ModelAlias:             "test-model",
		VectorStore:            "sqlite",
		EmbeddingMode:          "fake",
		Threshold:              0.9,
		CacheLayer:             layer,
		RequestPreview:         requestPreview,
		SemanticCandidateFound: layer == "semantic",
		Similarity:             0.95,
		CandidatePromptPreview: candidatePrompt,
		CandidateAnswerPreview: candidateAnswer,
		SafetyAllowed:          true,
		JudgeMode:              "fake",
		CachedScore:            cachedScore,
		FreshScore:             freshScore,
		JudgeDelta:             cachedScore - freshScore,
		JudgeReason:            "fixture",
		BadHit:                 false,
		EstimatedUpstreamCost:  1,
		SafeCostSaved:          0.9,
	}
	if cachedScore > 0 || freshScore > 0 {
		record.JudgeCacheSafe = boolPtr(true)
	}
	return record
}
