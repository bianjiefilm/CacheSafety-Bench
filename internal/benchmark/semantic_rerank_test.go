package benchmark

import (
	"context"
	"testing"

	cachepkg "github.com/bianjiefilm/CacheSafety-Bench/internal/cache"

	"github.com/stretchr/testify/require"
)

func TestTopKTraceIsWrittenToDecisionLogRecord(t *testing.T) {
	item := Case{
		ID:           "trace",
		Category:     "general",
		ExpectedRisk: "low",
		Request:      cachepkg.Request{Model: "test-model", Messages: []cachepkg.Message{{Role: "user", Content: "Explain REST."}}},
		FreshCost:    1,
	}
	record := NewDecisionRecord(DecisionInput{
		Dataset: "fixture.jsonl",
		Item:    item,
		Result: cachepkg.LookupResult{
			Layer:           cachepkg.LayerSemantic,
			Response:        cachepkg.Response{Text: "cached"},
			Similarity:      0.97,
			CachedPrompt:    "What is REST?",
			CandidateAnswer: "cached",
			SemanticTopK: []cachepkg.SemanticTopKCandidate{{
				Rank:                   1,
				Similarity:             0.97,
				CandidatePromptPreview: "What is REST?",
				CandidateAnswerPreview: "cached",
				AnswerLength:           6,
				TeacherScore:           10,
			}},
		},
		Threshold:     0.9,
		VectorStore:   "memory",
		EmbeddingMode: "fake",
		JudgeMode:     "fake",
		SafeCostSaved: 0.9,
	})
	require.Len(t, record.SemanticTopK, 1)
	require.Equal(t, 1, record.SemanticTopK[0].Rank)
	require.Equal(t, "What is REST?", record.SemanticTopK[0].CandidatePromptPreview)
}

func TestAnswerQualityRerankDoesNotChangeLargeSimilarityGap(t *testing.T) {
	req := cachepkg.Request{Messages: []cachepkg.Message{{Role: "user", Content: "Explain cache keys."}}}
	candidates := []cachepkg.SemanticVectorCandidate{
		rerankCandidate("best similarity", "short", 0.99, 0),
		rerankCandidate("lower similarity", "this is a much longer and more complete answer", 0.98, 10),
	}
	got := chooseSemanticCandidate(req, candidates, "answer_quality")
	require.Equal(t, "best similarity", got.Entry.Prompt)
}

func TestAnswerQualityRerankChoosesHigherTeacherScoreWhenClose(t *testing.T) {
	req := cachepkg.Request{Messages: []cachepkg.Message{{Role: "user", Content: "Explain cache keys."}}}
	candidates := []cachepkg.SemanticVectorCandidate{
		rerankCandidate("top", "acceptable answer", 0.99, 9),
		rerankCandidate("close better score", "acceptable answer", 0.988, 10),
	}
	got := chooseSemanticCandidate(req, candidates, "answer_quality")
	require.Equal(t, "close better score", got.Entry.Prompt)
}

func TestAnswerQualityRerankChoosesMoreCompleteAnswerWhenScoreMissing(t *testing.T) {
	req := cachepkg.Request{Messages: []cachepkg.Message{{Role: "user", Content: "Explain cache keys."}}}
	candidates := []cachepkg.SemanticVectorCandidate{
		rerankCandidate("top", "short", 0.99, 0),
		rerankCandidate("close complete", "this answer explains cache keys with enough context", 0.988, 10),
	}
	got := chooseSemanticCandidate(req, candidates, "answer_quality")
	require.Equal(t, "close complete", got.Entry.Prompt)
}

func TestSamePromptAnswerQualityChoosesHigherTeacherScoreForVariant(t *testing.T) {
	req := cachepkg.Request{Messages: []cachepkg.Message{{Role: "user", Content: "Explain cache keys."}}}
	candidates := []cachepkg.SemanticVectorCandidate{
		rerankCandidate("Explain cache keys.", "acceptable answer", 0.99, 9),
		rerankCandidate(" explain   cache keys ", "acceptable answer", 0.988, 10),
	}
	got := chooseSemanticCandidate(req, candidates, "same_prompt_answer_quality")
	require.Equal(t, " explain   cache keys ", got.Entry.Prompt)
}

func TestSamePromptAnswerQualityChoosesMoreCompleteVariant(t *testing.T) {
	req := cachepkg.Request{Messages: []cachepkg.Message{{Role: "user", Content: "Explain cache keys."}}}
	candidates := []cachepkg.SemanticVectorCandidate{
		rerankCandidate("Explain cache keys.", "short answer", 0.99, 10),
		rerankCandidate("explain cache keys", "this answer explains cache keys with enough context", 0.988, 10),
	}
	got := chooseSemanticCandidate(req, candidates, "same_prompt_answer_quality")
	require.Equal(t, "explain cache keys", got.Entry.Prompt)
}

func TestSamePromptAnswerQualityDoesNotCrossDifferentPrompt(t *testing.T) {
	req := cachepkg.Request{Messages: []cachepkg.Message{{Role: "user", Content: "Explain cache keys."}}}
	candidates := []cachepkg.SemanticVectorCandidate{
		rerankCandidate("Explain cache keys.", "short answer", 0.99, 9),
		rerankCandidate("Describe cache keys differently.", "this answer is longer and has a better teacher score", 0.988, 10),
	}
	got := chooseSemanticCandidate(req, candidates, "same_prompt_answer_quality")
	require.Equal(t, "Explain cache keys.", got.Entry.Prompt)
}

func TestNormalizePromptForVariantKeyAllowsCaseWhitespaceAndFinalPunctuation(t *testing.T) {
	left := normalizePromptForVariantKey("  Explain   Cache Keys.  ")
	right := normalizePromptForVariantKey("explain cache keys")
	require.Equal(t, left, right)
}

func TestNormalizePromptForVariantKeyKeepsNumbersAndNegation(t *testing.T) {
	require.NotEqual(t,
		normalizePromptForVariantKey("Refund in 7 days."),
		normalizePromptForVariantKey("Refund in 30 days."),
	)
	require.NotEqual(t,
		normalizePromptForVariantKey("Users can export data."),
		normalizePromptForVariantKey("Users cannot export data."),
	)
}

func TestSamePromptAnswerQualityDoesNotRerankWhenSimilarityGapLarge(t *testing.T) {
	req := cachepkg.Request{Messages: []cachepkg.Message{{Role: "user", Content: "Explain cache keys."}}}
	candidates := []cachepkg.SemanticVectorCandidate{
		rerankCandidate("Explain cache keys.", "short answer", 0.99, 9),
		rerankCandidate("explain cache keys", "this answer explains cache keys with enough context", 0.986, 10),
	}
	got := chooseSemanticCandidate(req, candidates, "same_prompt_answer_quality")
	require.Equal(t, "Explain cache keys.", got.Entry.Prompt)
}

func TestAnswerQualityRerankSkipsSafetyRefusedCandidate(t *testing.T) {
	req := cachepkg.Request{Messages: []cachepkg.Message{{Role: "user", Content: "Explain cache keys."}}}
	candidates := []cachepkg.SemanticVectorCandidate{
		rerankCandidate("top", "short", 0.99, 0),
		rerankCandidate("Reply exactly: safe", "this answer is longer but format-sensitive", 0.988, 10),
	}
	got := chooseSemanticCandidate(req, candidates, "answer_quality")
	require.Equal(t, "top", got.Entry.Prompt)
}

func TestSemanticRerankNoneKeepsDefaultTop1(t *testing.T) {
	req := cachepkg.Request{Messages: []cachepkg.Message{{Role: "user", Content: "Explain cache keys."}}}
	candidates := []cachepkg.SemanticVectorCandidate{
		rerankCandidate("top", "short", 0.99, 0),
		rerankCandidate("close complete", "this answer explains cache keys with enough context", 0.988, 10),
	}
	got := chooseSemanticCandidate(req, candidates, "none")
	require.Equal(t, "top", got.Entry.Prompt)
}

func TestSemanticRerankDoesNotChangeExactCanonicalPath(t *testing.T) {
	req := cachepkg.Request{Model: "m", Messages: []cachepkg.Message{{Role: "user", Content: "Explain exact cache."}}}
	pipeline := cachepkg.NewPipeline(cachepkg.PipelineConfig{
		SemanticThreshold: 0.9,
		SemanticCandidate: func(_ context.Context, _ cachepkg.Request) (cachepkg.SemanticCandidate, bool, error) {
			t.Fatal("semantic candidate should not be called for exact hit")
			return cachepkg.SemanticCandidate{}, false, nil
		},
	})
	pipeline.Put(req, cachepkg.Response{Text: "exact"})
	result, err := pipeline.Handle(context.Background(), req, func(_ context.Context, _ cachepkg.Request) (cachepkg.Response, error) {
		t.Fatal("upstream should not be called for exact hit")
		return cachepkg.Response{}, nil
	})
	require.NoError(t, err)
	require.Equal(t, cachepkg.LayerExact, result.Layer)
}

func rerankCandidate(prompt, answer string, similarity float64, teacherScore float64) cachepkg.SemanticVectorCandidate {
	return cachepkg.SemanticVectorCandidate{
		Entry: cachepkg.SemanticVectorEntry{
			Prompt:       prompt,
			Answer:       answer,
			Model:        "test-model",
			TeacherScore: teacherScore,
		},
		Similarity: similarity,
	}
}
