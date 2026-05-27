package benchmark

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestExportRegressionCasesByTypeAndIdempotent(t *testing.T) {
	root := t.TempDir()
	cacheSafe := false
	records := []DecisionRecord{
		{
			SampleID:               "bad-1",
			Category:               "semantic_trap",
			ExpectedRisk:           "medium",
			CacheLayer:             "semantic",
			RequestPreview:         "Describe beta concept.",
			CandidatePromptPreview: "Describe alpha concept.",
			CandidateAnswerPreview: "alpha",
			FreshAnswerPreview:     "beta",
			SemanticCandidateFound: true,
			Similarity:             0.97,
			Threshold:              0.90,
			SafetyAllowed:          true,
			JudgeCacheSafe:         &cacheSafe,
			BadHit:                 true,
			JudgeReason:            "subject changed",
		},
		{
			SampleID:               "unknown-1",
			Category:               "general",
			CacheLayer:             "semantic",
			RequestPreview:         "Describe prompt caching.",
			CandidatePromptPreview: "Explain prompt caching.",
			CandidateAnswerPreview: "cached",
			SemanticCandidateFound: true,
			Similarity:             0.96,
			Threshold:              0.90,
			SafetyAllowed:          true,
			JudgeError:             "judge_parse_error",
		},
		{
			SampleID:               "refused-1",
			Category:               "hard_negative",
			ExpectedRisk:           "high",
			CacheLayer:             "miss",
			RequestPreview:         "Answer with one word about prompt caching.",
			CandidatePromptPreview: "Explain prompt caching.",
			CandidateAnswerPreview: "Prompt caching reuses previous work.",
			SemanticCandidateFound: true,
			Similarity:             0.92,
			Threshold:              0.90,
			SafetyAllowed:          false,
			RefusedReason:          "format_sensitive",
			RefusedDetail:          "format_sensitive:one_word",
		},
	}
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)

	first, err := ExportRegressionCases(records, ExportRegressionOptions{
		OutputDir:         root,
		Mode:              "all",
		SourceResultFile:  "metrics.json",
		SourceDecisionLog: "decisions.jsonl",
		Now:               func() time.Time { return now },
	})
	require.NoError(t, err)
	require.Equal(t, 1, first.BadHits)
	require.Equal(t, 1, first.Unknown)
	require.Equal(t, 1, first.SafetyRefusals)
	require.Equal(t, 3, first.Written)
	require.FileExists(t, filepath.Join(root, "bad_hits", "semantic_trap_bad_1_bad_hits.json"))
	require.FileExists(t, filepath.Join(root, "unknown", "general_unknown_1_unknown.json"))
	require.FileExists(t, filepath.Join(root, "safety_refusals", "hard_negative_refused_1_format_sensitive_one_word.json"))

	second, err := ExportRegressionCases(records, ExportRegressionOptions{
		OutputDir: root,
		Mode:      "all",
		Now:       func() time.Time { return now },
	})
	require.NoError(t, err)
	require.Equal(t, 0, second.Written)
	require.Equal(t, 3, second.Skipped)
}

func TestExportRegressionCasesModeSafetyRefusals(t *testing.T) {
	root := t.TempDir()
	records := []DecisionRecord{
		{SampleID: "bad-1", Category: "trap", BadHit: true},
		{SampleID: "refused-1", Category: "hard_negative", RequestPreview: "Answer with one word.", CandidatePromptPreview: "Explain.", SafetyAllowed: false, RefusedReason: "format_sensitive", RefusedDetail: "format_sensitive:one_word"},
	}

	got, err := ExportRegressionCases(records, ExportRegressionOptions{OutputDir: root, Mode: "safety_refusals"})

	require.NoError(t, err)
	require.Equal(t, 0, got.BadHits)
	require.Equal(t, 1, got.SafetyRefusals)
	require.Equal(t, 1, got.Written)
}

func TestReplaySafetyRefusalPasses(t *testing.T) {
	root := t.TempDir()
	_, err := ExportRegressionCases([]DecisionRecord{{
		SampleID:               "refused-1",
		Category:               "hard_negative",
		CacheLayer:             "miss",
		RequestPreview:         "Answer with one word about prompt caching.",
		CandidatePromptPreview: "Explain prompt caching.",
		SafetyAllowed:          false,
		RefusedReason:          "format_sensitive",
		RefusedDetail:          "format_sensitive:one_word",
	}}, ExportRegressionOptions{OutputDir: root, Mode: "all"})
	require.NoError(t, err)

	got, err := ReplayRegressionCases(ReplayOptions{RegressionDir: root})

	require.NoError(t, err)
	require.Equal(t, 1, got.Total)
	require.Equal(t, 1, got.Passed)
	require.Equal(t, 0, got.Failed)
	require.Equal(t, 1, got.ByDir[RegressionDirSafetyRefusals].Passed)
}

func TestReplayBadHitFailsUnlessSafetyNowRejects(t *testing.T) {
	root := t.TempDir()
	_, err := ExportRegressionCases([]DecisionRecord{
		{
			SampleID:               "still-allowed",
			Category:               "semantic_trap",
			CacheLayer:             "semantic",
			RequestPreview:         "Describe beta concept.",
			CandidatePromptPreview: "Describe alpha concept.",
			SafetyAllowed:          true,
			BadHit:                 true,
		},
		{
			SampleID:               "fixed-by-guard",
			Category:               "semantic_trap",
			CacheLayer:             "semantic",
			RequestPreview:         "Refund requests must be submitted within 30 days.",
			CandidatePromptPreview: "Refund requests must be submitted within 7 days.",
			SafetyAllowed:          true,
			BadHit:                 true,
		},
	}, ExportRegressionOptions{OutputDir: root, Mode: "bad_hits"})
	require.NoError(t, err)

	got, err := ReplayRegressionCases(ReplayOptions{RegressionDir: filepath.Join(root, "bad_hits")})

	require.NoError(t, err)
	require.Equal(t, 2, got.Total)
	require.Equal(t, 1, got.Passed)
	require.Equal(t, 1, got.Failed)
	require.Len(t, got.FailureExamples, 1)
}

func TestReplayUnknownCannotBecomeSafe(t *testing.T) {
	root := t.TempDir()
	_, err := ExportRegressionCases([]DecisionRecord{{
		SampleID:               "unknown-1",
		Category:               "general",
		CacheLayer:             "semantic",
		RequestPreview:         "Describe prompt caching.",
		CandidatePromptPreview: "Explain prompt caching.",
		SafetyAllowed:          true,
		JudgeError:             "judge_parse_error",
	}}, ExportRegressionOptions{OutputDir: root, Mode: "unknown"})
	require.NoError(t, err)

	got, err := ReplayRegressionCases(ReplayOptions{RegressionDir: root})

	require.NoError(t, err)
	require.Equal(t, 1, got.Total)
	require.Equal(t, 0, got.Passed)
	require.Equal(t, 1, got.Failed)
	require.Contains(t, got.FailureExamples[0].Reason, "unknown")
}

func TestLoadRegressionCasesSupportsMaxCases(t *testing.T) {
	root := t.TempDir()
	records := []DecisionRecord{
		{SampleID: "a", Category: "hard_negative", RequestPreview: "Answer with one word.", CandidatePromptPreview: "Explain.", SafetyAllowed: false, RefusedReason: "format_sensitive"},
		{SampleID: "b", Category: "hard_negative", RequestPreview: "Return valid JSON.", CandidatePromptPreview: "Explain.", SafetyAllowed: false, RefusedReason: "format_sensitive"},
	}
	_, err := ExportRegressionCases(records, ExportRegressionOptions{OutputDir: root, Mode: "safety_refusals"})
	require.NoError(t, err)

	got, err := LoadRegressionCases(root, 1)

	require.NoError(t, err)
	require.Len(t, got, 1)
}

func TestLoadRegressionCasesSkipsLegacyBadHitExamplesWithoutCaseID(t *testing.T) {
	root := t.TempDir()
	badHitDir := filepath.Join(root, RegressionDirBadHits)
	require.NoError(t, os.MkdirAll(badHitDir, 0o755))
	legacy := `{"id":"legacy-bad-hit","category":"rewrite_polish","old_request":"A","new_request":"B","cached_answer":"A","fresh_answer":"B"}`
	require.NoError(t, os.WriteFile(filepath.Join(badHitDir, "threshold_0.90.jsonl"), []byte(legacy+"\n"), 0o644))

	got, err := LoadRegressionCases(root, 0)

	require.NoError(t, err)
	require.Empty(t, got)
}
