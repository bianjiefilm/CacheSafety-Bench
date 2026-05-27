package cache

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCosineSimilarity(t *testing.T) {
	require.InDelta(t, 1.0, CosineSimilarity([]float64{1, 0}, []float64{2, 0}), 0.0001)
	require.InDelta(t, 0.0, CosineSimilarity([]float64{1, 0}, []float64{0, 1}), 0.0001)
}

func TestSemanticSearchBelowThresholdMisses(t *testing.T) {
	store := NewSemanticVectorStore()
	require.NoError(t, store.Insert(context.Background(), SemanticVectorEntry{Prompt: "a", Answer: "answer", Model: "model", Vector: []float64{1, 0}}))

	got, err := store.Search(context.Background(), []float64{0.94, 0.341174442}, 0.95, 1)

	require.NoError(t, err)
	require.Empty(t, got)
}

func TestSemanticSearchAboveThresholdHits(t *testing.T) {
	store := NewSemanticVectorStore()
	require.NoError(t, store.Insert(context.Background(), SemanticVectorEntry{Prompt: "a", Answer: "answer", Model: "model", Vector: []float64{1, 0}}))

	got, err := store.Search(context.Background(), []float64{0.96, 0.28}, 0.95, 1)

	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "a", got[0].Entry.Prompt)
}

func TestSemanticSearchTop1ReturnsHighestSimilarity(t *testing.T) {
	store := NewSemanticVectorStore()
	require.NoError(t, store.Insert(context.Background(), SemanticVectorEntry{Prompt: "low", Answer: "answer", Model: "model", Vector: []float64{0.96, 0.28}}))
	require.NoError(t, store.Insert(context.Background(), SemanticVectorEntry{Prompt: "high", Answer: "answer", Model: "model", Vector: []float64{1, 0}}))

	got, err := store.Search(context.Background(), []float64{1, 0}, 0.95, 1)

	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "high", got[0].Entry.Prompt)
}

func TestSemanticSearchDimensionMismatch(t *testing.T) {
	store := NewSemanticVectorStoreWithDim(2)
	require.NoError(t, store.Insert(context.Background(), SemanticVectorEntry{Prompt: "a", Answer: "answer", Model: "model", Vector: []float64{1, 0}}))

	_, err := store.Search(context.Background(), []float64{1, 0, 0}, 0.95, 1)

	require.ErrorContains(t, err, "vector dimension mismatch")
}
