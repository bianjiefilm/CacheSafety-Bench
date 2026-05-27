package benchmark

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bianjiefilm/CacheSafety-Bench/internal/embedding"
	"github.com/bianjiefilm/CacheSafety-Bench/internal/teacher"

	"github.com/stretchr/testify/require"
)

func TestReplaySeedEnablesSemanticCandidate(t *testing.T) {
	cacheSafe := true
	cases := []Case{{
		ID:                    "replay-1",
		Category:              "general_explanation",
		ExpectedRisk:          "low",
		Request:               testBenchRequest("Describe when semantic cache reuse is safe."),
		FreshAnswer:           "Semantic cache reuse needs high similarity and a safety pass.",
		TeacherScore:          10,
		FreshCost:             1,
		CachedCost:            0.1,
		OldRequest:            "Explain safe semantic cache reuse.",
		OldAnswer:             "Semantic cache reuse needs high similarity and a safety pass.",
		ExpectedCacheBehavior: "semantic_safe",
		SourceJudgeCacheSafe:  &cacheSafe,
		ReplayQuality:         "full_from_dataset",
	}}

	got, err := RunWithEmbedding(context.Background(), cases, Config{EmbeddingMode: "fake", SemanticThreshold: 0.90}, nil, teacher.FakeJudge{}, embedding.FakeEmbedder{})

	require.NoError(t, err)
	require.Equal(t, 1, got.SemanticCandidates)
	require.Equal(t, 1, got.SemanticJudgedSafe)
	require.Equal(t, 0.0, got.BadHitRate)
}

func TestExportReplayDatasetFromRequestLogUsesDatasetResolver(t *testing.T) {
	ctx := context.Background()
	store := openTestRequestLogStore(t)
	defer func() { _ = store.Close() }()
	cacheSafe := true
	require.NoError(t, store.Write(ctx, RequestLogWrite{
		Run: BenchmarkRunLog{
			RunID:         "run-1",
			Dataset:       "safe_paraphrase_eval_v0.jsonl",
			ProviderMode:  "fake",
			JudgeMode:     "fake",
			EmbeddingMode: "fake",
			VectorStore:   "memory",
			VectorDim:     2,
			Threshold:     0.9,
		},
		Metrics: Metrics{Total: 1},
		Records: []DecisionRecord{{
			SampleID:               "002-general-rest-para",
			Category:               "general_explanation",
			ExpectedRisk:           "low",
			CacheLayer:             "semantic",
			RequestPreview:         "Describe a REST API in simple language.",
			SemanticCandidateFound: true,
			Similarity:             0.95,
			CandidatePromptPreview: "Explain what a REST API is in simple language.",
			CandidateAnswerPreview: "truncated",
			FreshAnswerPreview:     "truncated",
			SafetyAllowed:          true,
			JudgeCacheSafe:         &cacheSafe,
			EstimatedUpstreamCost:  1,
			SafeCostSaved:          0.9,
		}},
	}))

	cases, summary, err := ExportReplayDatasetFromRequestLog(ctx, store, "run-1", ReplayDatasetOptions{
		Include:    ParseReplayInclude("semantic_safe"),
		OutputPath: "replay.jsonl",
	})

	require.NoError(t, err)
	require.Equal(t, 1, summary.Total)
	require.Len(t, cases, 1)
	require.Equal(t, "full_from_dataset", cases[0].ReplayQuality)
	require.Equal(t, "Explain what a REST API is in simple language.", cases[0].OldRequest)
	require.Equal(t, "Describe a REST API in simple language.", cases[0].Request.Messages[0].Content)
	require.Equal(t, "A REST API lets software systems exchange data through standard web requests.", cases[0].FreshAnswer)
	require.NotEqual(t, "truncated", cases[0].OldAnswer)
}

func TestExportReplayDatasetFromDecisionLogPreviewFallback(t *testing.T) {
	cacheSafe := true
	cases, summary, err := ExportReplayDatasetFromDecisionLog([]DecisionRecord{{
		SampleID:               "missing-id",
		Dataset:                "safe_paraphrase_eval_v0.jsonl",
		Category:               "general_explanation",
		ExpectedRisk:           "low",
		CacheLayer:             "semantic",
		RequestPreview:         "Describe a REST API in simple language.",
		SemanticCandidateFound: true,
		Similarity:             0.95,
		CandidatePromptPreview: "Explain what a REST API is in simple language.",
		CandidateAnswerPreview: strings.Repeat("a", 200),
		FreshAnswerPreview:     "preview fresh",
		SafetyAllowed:          true,
		JudgeCacheSafe:         &cacheSafe,
	}}, ReplayDatasetOptions{
		Include:    ParseReplayInclude("semantic_safe"),
		OutputPath: "replay.jsonl",
	})

	require.NoError(t, err)
	require.Equal(t, 1, summary.Total)
	require.Equal(t, "preview_only", cases[0].ReplayQuality)
	require.Equal(t, "Describe a REST API in simple language.", cases[0].Request.Messages[0].Content)
	require.Equal(t, "preview fresh", cases[0].FreshAnswer)
}

func TestReplayDatasetFiltersAndMaxSamples(t *testing.T) {
	cacheSafe := true
	records := []DecisionRecord{
		{SampleID: "002-general-rest-para", Dataset: "safe_paraphrase_eval_v0.jsonl", Category: "general_explanation", CacheLayer: "semantic", RequestPreview: "Describe a REST API in simple language.", CandidatePromptPreview: "Explain what a REST API is in simple language.", SafetyAllowed: true, JudgeCacheSafe: &cacheSafe},
		{SampleID: "098-hard-oneword", Dataset: "safe_paraphrase_eval_v0.jsonl", Category: "hard_negative", CacheLayer: "miss", RequestPreview: "Answer with one word whether the request looks safe.", CandidatePromptPreview: "Describe whether a request looks safe.", SafetyAllowed: false, RefusedReason: "format_sensitive", RefusedDetail: "format_sensitive:one_word"},
	}

	cases, summary, err := ExportReplayDatasetFromDecisionLog(records, ReplayDatasetOptions{
		Include:    ParseReplayInclude("refusals"),
		Category:   "hard_negative",
		MaxSamples: 1,
		OutputPath: "replay.jsonl",
	})

	require.NoError(t, err)
	require.Equal(t, 1, summary.Total)
	require.Len(t, cases, 1)
	require.Equal(t, "hard_negative", cases[0].Category)
	require.Equal(t, "refusal", cases[0].ExpectedCacheBehavior)
}

func TestReplayDatasetWriteCanBeLoadedByBenchmark(t *testing.T) {
	path := filepath.Join(t.TempDir(), "replay.jsonl")
	payload := []ReplayDatasetCase{{
		ID:                    "replay-1",
		Category:              "general_explanation",
		ExpectedRisk:          "low",
		Request:               testBenchRequest("Describe a REST API in simple language."),
		FreshAnswer:           "A REST API lets software systems exchange data through standard web requests.",
		TeacherScore:          10,
		FreshCost:             1,
		CachedCost:            0.1,
		EstimatedUpstreamCost: 1,
		OldRequest:            "Explain what a REST API is in simple language.",
		OldAnswer:             "A REST API is a way for software systems to exchange data through standard web requests.",
		ExpectedCacheBehavior: "semantic_safe",
		ReplayQuality:         "full_from_dataset",
	}}

	require.NoError(t, WriteReplayDataset(path, payload))
	loaded, err := LoadJSONL(path)
	require.NoError(t, err)
	require.Len(t, loaded, 1)
	require.Equal(t, payload[0].OldRequest, loaded[0].OldRequest)
	require.Equal(t, payload[0].Request.Messages[0].Content, loaded[0].Request.Messages[0].Content)
	bytes, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NotContains(t, string(bytes), "ark-346fd29f")
}
