package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	cachepkg "github.com/bianjiefilm/CacheSafety-Bench/internal/cache"

	"github.com/stretchr/testify/require"
)

func TestNewOpenAIProviderRequiresAPIKey(t *testing.T) {
	_, err := NewOpenAIProvider(OpenAIConfig{BaseURL: "https://api.example.test/v1"}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "OPENAI_API_KEY")
}

func TestOpenAIProviderCapturesServeModeHeaders(t *testing.T) {
	var gotAuth string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/chat/completions", r.URL.Path)
		gotAuth = r.Header.Get("Authorization")
		payload, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(payload, &gotBody))

		w.Header().Set("x-nextmodel-serve-mode", "canonical_cache")
		w.Header().Set("x-nextmodel-request-id", "req-live-1")
		w.Header().Set("x-nextmodel-receipt-hash", "hash-live-1")
		w.Header().Set("x-nextmodel-receipt-url", "https://example.test/receipts/live-1")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"id": "chatcmpl-1",
			"model": "demo-model",
			"choices": [{"message": {"role": "assistant", "content": "cached reply"}, "finish_reason": "stop"}],
			"usage": {"prompt_tokens": 4, "completion_tokens": 2, "total_tokens": 6, "prompt_tokens_details": {"cached_tokens": 3}}
		}`))
	}))
	defer server.Close()

	client, err := NewOpenAIProvider(OpenAIConfig{
		APIKey:  "test-openai-key",
		BaseURL: server.URL + "/v1",
		Timeout: time.Second,
	}, server.Client())
	require.NoError(t, err)

	maxTokens := 32
	result, err := client.Complete(context.Background(), cachepkg.Request{
		Model:     "demo-model",
		Messages:  []cachepkg.Message{{Role: "user", Content: "What is the status?"}},
		MaxTokens: &maxTokens,
	})
	require.NoError(t, err)
	require.Equal(t, "Bearer test-openai-key", gotAuth)
	require.Equal(t, "demo-model", gotBody["model"])
	require.Equal(t, "cached reply", result.Response.Content)
	require.Equal(t, 4, result.Usage.PromptTokens)
	promptTokens := 4
	cachedTokens := 3
	require.Equal(t, cachepkg.Observation{
		ServeMode:    "canonical_cache",
		RequestID:    "req-live-1",
		ReceiptHash:  "hash-live-1",
		ReceiptURL:   "https://example.test/receipts/live-1",
		PromptTokens: &promptTokens,
		CachedTokens: &cachedTokens,
	}, result.Observation)

	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "test-openai-key")
}

func TestOpenAIProviderSanitizesAPIKeyInErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid test-openai-key"}}`))
	}))
	defer server.Close()

	client, err := NewOpenAIProvider(OpenAIConfig{
		APIKey:  "test-openai-key",
		BaseURL: server.URL + "/v1",
		Timeout: time.Second,
	}, server.Client())
	require.NoError(t, err)

	_, err = client.Complete(context.Background(), cachepkg.Request{
		Model:    "demo-model",
		Messages: []cachepkg.Message{{Role: "user", Content: "ping"}},
	})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "test-openai-key")
	require.Contains(t, err.Error(), "[redacted]")
}

func TestLoadOpenAIConfigFromEnv(t *testing.T) {
	t.Setenv(EnvOpenAIAPIKey, "sk-test-key")
	t.Setenv(EnvOpenAIBaseURL, "https://api.nextmodel.app/v1")
	t.Setenv(EnvOpenAIModel, "gateway-model")
	t.Setenv("OPENAI_TIMEOUT_SECONDS", "15")

	cfg := LoadOpenAIConfigFromEnv()

	require.Equal(t, "sk-test-key", cfg.APIKey)
	require.Equal(t, "https://api.nextmodel.app/v1", cfg.BaseURL)
	require.Equal(t, "gateway-model", cfg.Model)
	require.Equal(t, 15*time.Second, cfg.Timeout)
	require.NotContains(t, cfg.RedactedAPIKey, "sk-test-key")
}
