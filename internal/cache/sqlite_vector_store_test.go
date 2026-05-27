package cache

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSQLiteVectorStoreSearchMatchesMemoryTop1(t *testing.T) {
	ctx := context.Background()
	memory := NewSemanticVectorStoreWithDim(2)
	sqliteStore := newTestSQLiteVectorStore(t, 2)
	items := []SemanticVectorEntry{
		{Prompt: "low", Answer: "low answer", Model: "model", Category: "test", TeacherScore: 9, Vector: []float64{0.96, 0.28}},
		{Prompt: "high", Answer: "high answer", Model: "model", Category: "test", TeacherScore: 10, Vector: []float64{1, 0}},
	}
	for _, item := range items {
		require.NoError(t, memory.Insert(ctx, item))
		require.NoError(t, sqliteStore.Insert(ctx, item))
	}

	memGot, err := memory.Search(ctx, []float64{1, 0}, 0.95, 1)
	require.NoError(t, err)
	sqlGot, err := sqliteStore.Search(ctx, []float64{1, 0}, 0.95, 1)
	require.NoError(t, err)

	require.Len(t, memGot, 1)
	require.Len(t, sqlGot, 1)
	require.Equal(t, memGot[0].Entry.Prompt, sqlGot[0].Entry.Prompt)
	require.InDelta(t, memGot[0].Similarity, sqlGot[0].Similarity, 0.0001)
}

func TestSQLiteVectorStoreBelowThresholdMisses(t *testing.T) {
	ctx := context.Background()
	store := newTestSQLiteVectorStore(t, 2)
	require.NoError(t, store.Insert(ctx, SemanticVectorEntry{Prompt: "a", Answer: "answer", Model: "model", Vector: []float64{1, 0}}))

	got, err := store.Search(ctx, []float64{0.94, 0.341174442}, 0.95, 1)

	require.NoError(t, err)
	require.Empty(t, got)
}

func TestSQLiteVectorStoreAboveThresholdHits(t *testing.T) {
	ctx := context.Background()
	store := newTestSQLiteVectorStore(t, 2)
	require.NoError(t, store.Insert(ctx, SemanticVectorEntry{Prompt: "a", Answer: "answer", Model: "model", Vector: []float64{1, 0}}))

	got, err := store.Search(ctx, []float64{0.96, 0.28}, 0.95, 1)

	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "a", got[0].Entry.Prompt)
}

func TestSQLiteVectorStoreTop1ReturnsHighestSimilarity(t *testing.T) {
	ctx := context.Background()
	store := newTestSQLiteVectorStore(t, 2)
	require.NoError(t, store.Insert(ctx, SemanticVectorEntry{Prompt: "low", Answer: "answer", Model: "model", Vector: []float64{0.96, 0.28}}))
	require.NoError(t, store.Insert(ctx, SemanticVectorEntry{Prompt: "high", Answer: "answer", Model: "model", Vector: []float64{1, 0}}))

	got, err := store.Search(ctx, []float64{1, 0}, 0.95, 1)

	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "high", got[0].Entry.Prompt)
}

func TestSQLiteVectorStoreMetadataRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := newTestSQLiteVectorStore(t, 2)
	require.NoError(t, store.Insert(ctx, SemanticVectorEntry{
		Prompt:       "prompt",
		Answer:       "answer",
		Model:        "model",
		Category:     "category",
		TeacherScore: 9.5,
		Vector:       []float64{1, 0},
		Metadata:     SemanticVectorMetadata{"source": "test"},
	}))

	got, err := store.Search(ctx, []float64{1, 0}, 0.95, 1)

	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "prompt", got[0].Entry.Prompt)
	require.Equal(t, "answer", got[0].Entry.Answer)
	require.Equal(t, "model", got[0].Entry.Model)
	require.Equal(t, "category", got[0].Entry.Category)
	require.Equal(t, 9.5, got[0].Entry.TeacherScore)
	require.Equal(t, "test", got[0].Entry.Metadata["source"])
}

func TestSQLiteVectorStoreEmptySearchDoesNotError(t *testing.T) {
	store := newTestSQLiteVectorStore(t, 2)

	got, err := store.Search(context.Background(), []float64{1, 0}, 0.95, 1)

	require.NoError(t, err)
	require.Empty(t, got)
}

func TestSQLiteVectorStoreDimensionMismatch(t *testing.T) {
	store := newTestSQLiteVectorStore(t, 2)

	err := store.Insert(context.Background(), SemanticVectorEntry{Prompt: "bad", Answer: "answer", Model: "model", Vector: []float64{1, 0, 0}})

	require.ErrorContains(t, err, "vector dimension mismatch")
	_, err = store.Search(context.Background(), []float64{1, 0, 0}, 0.95, 1)
	require.ErrorContains(t, err, "vector dimension mismatch")
}

func newTestSQLiteVectorStore(t *testing.T, dim int) *SQLiteVectorStore {
	t.Helper()
	store, err := NewSQLiteVectorStore(context.Background(), filepath.Join(t.TempDir(), "vectors.db"), dim)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	return store
}
