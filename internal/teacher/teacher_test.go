package teacher

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFakeJudgeStable(t *testing.T) {
	judge := FakeJudge{}

	first, err := judge.Score(context.Background(), JudgeInput{
		NewRequest:   "Give one word describing safe semantic cache reuse.",
		CachedAnswer: "Integrity",
		FreshAnswer:  "safe",
		Category:     "semantic_paraphrase",
	})
	require.NoError(t, err)
	second, err := judge.Score(context.Background(), JudgeInput{
		NewRequest:   "Give one word describing safe semantic cache reuse.",
		CachedAnswer: "Integrity",
		FreshAnswer:  "safe",
		Category:     "semantic_paraphrase",
	})
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.False(t, first.BadHit)
	require.True(t, first.CacheSafe)
}

func TestParseJudgeJSONSuccess(t *testing.T) {
	got, err := ParseJudgeJSON(`{"cache_safe":true,"cached_score":8,"fresh_score":9,"delta":-1,"bad_hit":false,"reason":"minor wording difference only"}`)

	require.NoError(t, err)
	require.True(t, got.CacheSafe)
	require.Equal(t, 8.0, got.CachedScore)
	require.Equal(t, -1.0, got.Delta)
	require.False(t, got.BadHit)
}

func TestParseJudgeJSONFailureDoesNotPanic(t *testing.T) {
	got, err := ParseJudgeJSON(`not-json`)

	require.Error(t, err)
	require.True(t, errors.Is(err, ErrJudgeParse))
	require.Equal(t, ErrJudgeParse.Error(), got.Reason)
}
