package provider

import (
	"context"
	"testing"

	cachepkg "github.com/bianjiefilm/CacheSafety-Bench/internal/cache"

	"github.com/stretchr/testify/require"
)

func TestFakeProviderComplete(t *testing.T) {
	provider := &FakeProvider{
		Result: Result{
			Response: Response{Text: "ok", Content: "ok", Model: "test-model"},
			Usage:    Usage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5},
			Cost:     0.01,
		},
	}

	got, err := provider.Complete(context.Background(), cachepkg.Request{Model: "test-model"})

	require.NoError(t, err)
	require.Equal(t, "ok", got.Response.Text)
	require.Equal(t, 1, provider.Calls)
}

func TestVolcengineErrorSanitizesSecretsAndModels(t *testing.T) {
	p := &VolcengineProvider{cfg: VolcengineConfig{
		APIKey: "secret-api-key",
		Models: []string{"sensitive-model-id"},
	}}

	got := p.sanitizeError("secret-api-key failed for sensitive-model-id. Your account %!s(int64=12345) failed. Request id: abc123", "another-sensitive-model")

	require.NotContains(t, got, "secret-api-key")
	require.NotContains(t, got, "sensitive-model-id")
	require.NotContains(t, got, "12345")
	require.NotContains(t, got, "abc123")
	require.Contains(t, got, "[redacted]")
	require.Contains(t, got, "[volcengine_model_1]")
}
