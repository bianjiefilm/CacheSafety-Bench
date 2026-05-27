package benchmark

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestImportEvalJSONLPromptOnly(t *testing.T) {
	cases, summary, err := ImportEvalDataset(ImportEvalOptions{
		InputPath:   filepath.Join("..", "..", "testdata", "import", "sample_eval.jsonl"),
		InputFormat: "jsonl",
		OutputPath:  "out.jsonl",
		MaxSamples:  1,
	})

	require.NoError(t, err)
	require.Len(t, cases, 1)
	require.Equal(t, 1, summary.Total)
	require.NotEmpty(t, cases[0].Request.Messages)
}

func TestImportEvalJSONLOldNewPair(t *testing.T) {
	cases, _, err := ImportEvalDataset(ImportEvalOptions{
		InputPath:   filepath.Join("..", "..", "testdata", "import", "sample_eval.jsonl"),
		InputFormat: "jsonl",
		OutputPath:  "out.jsonl",
	})

	require.NoError(t, err)
	require.Equal(t, "Explain prompt caching in simple product terms.", cases[1].OldRequest)
	require.Equal(t, "imported_pair", cases[1].ExpectedCacheBehavior)
}

func TestImportEvalCSVPromptOnly(t *testing.T) {
	cases, summary, err := ImportEvalDataset(ImportEvalOptions{
		InputPath:   filepath.Join("..", "..", "testdata", "import", "sample_eval.csv"),
		InputFormat: "csv",
		OutputPath:  "out.jsonl",
		MaxSamples:  1,
	})

	require.NoError(t, err)
	require.Equal(t, 1, summary.Total)
	require.Len(t, cases, 1)
	require.NotEmpty(t, cases[0].Request.Messages[0].Content)
}

func TestImportEvalTextLineByLine(t *testing.T) {
	cases, summary, err := ImportEvalDataset(ImportEvalOptions{
		InputPath:       filepath.Join("..", "..", "testdata", "import", "sample_prompts.txt"),
		InputFormat:     "text",
		OutputPath:      "out.jsonl",
		DefaultCategory: "imported_general",
		DefaultRisk:     "medium",
		MaxSamples:      3,
	})

	require.NoError(t, err)
	require.Equal(t, 3, summary.Total)
	require.Equal(t, "text-001", cases[0].ID)
	require.Equal(t, "single_request", cases[0].Kind)
}

func TestImportEvalCategoryInferenceWorks(t *testing.T) {
	cases, _, err := ImportEvalDataset(ImportEvalOptions{
		InputPath:   filepath.Join("..", "..", "testdata", "import", "sample_prompts.txt"),
		InputFormat: "text",
		OutputPath:  "out.jsonl",
		MaxSamples:  6,
	})

	require.NoError(t, err)
	require.Equal(t, "general_explanation", cases[0].Category)
	require.Equal(t, "howto_guidance", cases[1].Category)
	require.Equal(t, "rewrite_polish", cases[2].Category)
	require.Equal(t, "loose_translation", cases[3].Category)
	require.Equal(t, "faq_generalization", cases[4].Category)
	require.Equal(t, "hard_negative", cases[5].Category)
}

func TestImportEvalSensitiveRiskHigher(t *testing.T) {
	cases, _, err := ImportEvalDataset(ImportEvalOptions{
		InputPath:   filepath.Join("..", "..", "testdata", "import", "sample_prompts.txt"),
		InputFormat: "text",
		OutputPath:  "out.jsonl",
		MaxSamples:  6,
	})

	require.NoError(t, err)
	require.Equal(t, "high", cases[5].ExpectedRisk)
}

func TestImportEvalCategoryInferenceAvoidsFalseFAQ(t *testing.T) {
	cases, _, err := ImportEvalDataset(ImportEvalOptions{
		InputPath:   filepath.Join("..", "..", "testdata", "import", "sample_prompts.txt"),
		InputFormat: "text",
		OutputPath:  "out.jsonl",
		MaxSamples:  13,
	})

	require.NoError(t, err)
	require.Equal(t, "howto_guidance", cases[12].Category)
}

func TestImportEvalMaxSamplesWorks(t *testing.T) {
	cases, summary, err := ImportEvalDataset(ImportEvalOptions{
		InputPath:   filepath.Join("..", "..", "testdata", "import", "sample_prompts.txt"),
		InputFormat: "text",
		OutputPath:  "out.jsonl",
		MaxSamples:  2,
	})

	require.NoError(t, err)
	require.Len(t, cases, 2)
	require.Equal(t, 2, summary.Total)
}

func TestImportEvalPairModeExactDuplicate(t *testing.T) {
	cases, _, err := ImportEvalDataset(ImportEvalOptions{
		InputPath:   filepath.Join("..", "..", "testdata", "import", "sample_prompts.txt"),
		InputFormat: "text",
		OutputPath:  "out.jsonl",
		MaxSamples:  1,
		PairMode:    "exact_duplicate",
	})

	require.NoError(t, err)
	require.Equal(t, "exact_duplicate", cases[0].Kind)
	require.Equal(t, cases[0].Request.Messages[0].Content, cases[0].OldRequest)
}

func TestImportedOutputCanBeLoadedByBenchmark(t *testing.T) {
	path := filepath.Join(t.TempDir(), "external.jsonl")
	cases, _, err := ImportEvalDataset(ImportEvalOptions{
		InputPath:   filepath.Join("..", "..", "testdata", "import", "sample_eval.csv"),
		InputFormat: "csv",
		OutputPath:  path,
		MaxSamples:  2,
	})
	require.NoError(t, err)
	require.NoError(t, WriteImportedEvalDataset(path, cases))

	loaded, err := LoadJSONL(path)
	require.NoError(t, err)
	require.Len(t, loaded, 2)

	bytes, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NotContains(t, string(bytes), "ark-346fd29f")
}
