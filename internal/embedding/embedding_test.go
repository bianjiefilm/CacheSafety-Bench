package embedding

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFakeEmbedderStable(t *testing.T) {
	embedder := FakeEmbedder{}

	first, err := embedder.Embed(context.Background(), EmbedRequest{Text: "Explain semantic cache reuse."})
	require.NoError(t, err)
	second, err := embedder.Embed(context.Background(), EmbedRequest{Text: "Explain semantic cache reuse."})
	require.NoError(t, err)

	require.Equal(t, first.Vector, second.Vector)
	require.Equal(t, len(first.Vector), first.Dim)
}

func TestLocalHashEmbedderStable(t *testing.T) {
	embedder := LocalHashEmbedder{Dim: 16}

	first, err := embedder.Embed(context.Background(), EmbedRequest{Text: "hello world"})
	require.NoError(t, err)
	second, err := embedder.Embed(context.Background(), EmbedRequest{Text: "hello world"})
	require.NoError(t, err)

	require.Equal(t, first.Vector, second.Vector)
	require.Len(t, first.Vector, 16)
}

func TestLocalHashEmbedderSemanticSmoke(t *testing.T) {
	embedder := LocalHashEmbedder{Dim: 64}

	left, err := embedder.Embed(context.Background(), EmbedRequest{Text: "Reply with one word about safe semantic cache reuse."})
	require.NoError(t, err)
	right, err := embedder.Embed(context.Background(), EmbedRequest{Text: "Give one word describing safe semantic cache reuse."})
	require.NoError(t, err)

	require.Equal(t, left.Vector, right.Vector)
}

func TestVolcengineEmbeddingIntegration(t *testing.T) {
	if os.Getenv(EnvRunEmbeddingIntegration) != "1" {
		t.Skip("set RUN_EMBEDDING_INTEGRATION=1 to run Volcengine embedding integration")
	}
	embedder, enabled, err := NewVolcengineEmbedderFromEnv("")
	if !enabled {
		t.Skip("set VOLCENGINE_EMBEDDING_MODEL to run Volcengine embedding integration")
	}
	require.NoError(t, err)

	got, err := embedder.Embed(context.Background(), EmbedRequest{Text: "embedding smoke test"})

	require.NoError(t, err)
	require.NotEmpty(t, got.Vector)
	require.Greater(t, got.Dim, 0)
	require.Equal(t, len(got.Vector), got.Dim)
	t.Logf("embedding_dim=%d", got.Dim)

	again, err := embedder.Embed(context.Background(), EmbedRequest{Text: "embedding smoke test"})
	require.NoError(t, err)
	require.Equal(t, got.Dim, again.Dim)
}
