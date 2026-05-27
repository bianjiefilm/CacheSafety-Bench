package cache

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestQuantizeInt8RoundTripKeepsCosineClose(t *testing.T) {
	vector := []float64{0.1, -0.2, 0.7, 0.4, -0.05}

	quantized := QuantizeInt8(vector)
	roundTrip := DequantizeInt8(quantized)

	require.Len(t, quantized.Values, len(vector))
	require.Len(t, roundTrip, len(vector))
	require.Greater(t, CosineSimilarity(vector, roundTrip), 0.999)
}

func TestEncodeDecodeInt8(t *testing.T) {
	values := []int8{-127, -1, 0, 1, 127}

	got := DecodeInt8(EncodeInt8(values))

	require.Equal(t, values, got)
}

func TestSQLiteInt8VectorStoreSearchMatchesMemoryTop1(t *testing.T) {
	ctx := context.Background()
	memory := NewSemanticVectorStoreWithDim(3)
	int8Store, err := NewSQLiteInt8VectorStore(ctx, filepath.Join(t.TempDir(), "int8.db"), 3)
	require.NoError(t, err)
	defer func() { _ = int8Store.Close() }()

	entries := []SemanticVectorEntry{
		{Prompt: "a", Answer: "A", Model: "m", Vector: []float64{1, 0, 0}},
		{Prompt: "b", Answer: "B", Model: "m", Vector: []float64{0.8, 0.2, 0}},
		{Prompt: "c", Answer: "C", Model: "m", Vector: []float64{0, 1, 0}},
	}
	for _, entry := range entries {
		require.NoError(t, memory.Insert(ctx, entry))
		require.NoError(t, int8Store.Insert(ctx, entry))
	}

	query := []float64{0.9, 0.1, 0}
	memoryHits, err := memory.Search(ctx, query, 0, 1)
	require.NoError(t, err)
	int8Hits, err := int8Store.Search(ctx, query, 0, 1)
	require.NoError(t, err)

	require.Len(t, memoryHits, 1)
	require.Len(t, int8Hits, 1)
	require.Equal(t, memoryHits[0].Entry.Prompt, int8Hits[0].Entry.Prompt)
}

func TestSQLiteInt8VectorStoreBelowThresholdMisses(t *testing.T) {
	ctx := context.Background()
	store, err := NewSQLiteInt8VectorStore(ctx, filepath.Join(t.TempDir(), "int8.db"), 2)
	require.NoError(t, err)
	defer func() { _ = store.Close() }()
	require.NoError(t, store.Insert(ctx, SemanticVectorEntry{Prompt: "a", Answer: "A", Model: "m", Vector: []float64{1, 0}}))

	hits, err := store.Search(ctx, []float64{0, 1}, 0.95, 1)

	require.NoError(t, err)
	require.Empty(t, hits)
}

func TestSQLiteInt8VectorStoreDimensionMismatch(t *testing.T) {
	ctx := context.Background()
	store, err := NewSQLiteInt8VectorStore(ctx, filepath.Join(t.TempDir(), "int8.db"), 2)
	require.NoError(t, err)
	defer func() { _ = store.Close() }()

	require.ErrorContains(t, store.Insert(ctx, SemanticVectorEntry{Prompt: "bad", Answer: "bad", Model: "m", Vector: []float64{1, 2, 3}}), "vector dimension mismatch")
	_, err = store.Search(ctx, []float64{1, 2, 3}, 0.9, 1)
	require.ErrorContains(t, err, "vector dimension mismatch")
}

func TestEvaluateQuantizationReportsTop1AndThresholdCrossings(t *testing.T) {
	samples := []VectorSample{
		{ID: "a", Vector: []float64{1, 0}},
		{ID: "b", Vector: []float64{0.91, 0.41}},
		{ID: "c", Vector: []float64{0, 1}},
	}

	metrics := EvaluateQuantization(samples, 0.90)

	require.Equal(t, 3, metrics.TotalVectors)
	require.Equal(t, 2, metrics.Dim)
	require.Greater(t, metrics.Top1MatchRate, 0.5)
	require.GreaterOrEqual(t, metrics.P95SimilarityDelta, 0.0)
	require.GreaterOrEqual(t, metrics.ThresholdCrossingCount, 0)
}
