package provider

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	cachepkg "github.com/bianjiefilm/CacheSafety-Bench/internal/cache"

	"github.com/stretchr/testify/require"
)

func TestVolcengineProviderMissingConfig(t *testing.T) {
	_, err := NewVolcengineProvider(VolcengineConfig{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing volcengine config")
}

func TestVolcengineIntegration_SingleModel(t *testing.T) {
	provider, cfg := requireVolcengineIntegration(t)
	model := requireModel(t, cfg, 0)

	result, err := provider.Complete(context.Background(), shortRequest(model, "Reply with exactly: pong"))
	require.NoError(t, err)
	require.NotEmpty(t, result.Response.Content)
	require.NotEmpty(t, result.Response.Model)
	require.Greater(t, result.Response.LatencyMS, int64(0))

	encoded, marshalErr := json.Marshal(result)
	require.NoError(t, marshalErr)
	require.NotContains(t, string(encoded), cfg.APIKey)
}

func TestVolcengineIntegration_AllConfiguredModels(t *testing.T) {
	provider, cfg := requireVolcengineIntegration(t)
	require.NotEmpty(t, cfg.Models)

	for i, model := range cfg.Models {
		t.Run("model_"+strconv.Itoa(i+1), func(t *testing.T) {
			result, err := provider.Complete(context.Background(), shortRequest(model, "Reply with one word: ok"))
			require.NoError(t, err)
			require.NotEmpty(t, result.Response.Content)
			t.Logf("model_%d ok latency_ms=%d", i+1, result.Response.LatencyMS)
		})
	}
}

func TestVolcengineIntegration_OpenAIShape(t *testing.T) {
	provider, cfg := requireVolcengineIntegration(t)
	model := requireModel(t, cfg, 0)

	result, err := provider.Complete(context.Background(), shortRequest(model, "Reply with one short sentence."))
	require.NoError(t, err)
	if result.Usage.TotalTokens == 0 {
		t.Log("volcengine response did not include usage tokens")
		return
	}
	require.GreaterOrEqual(t, result.Usage.TotalTokens, result.Usage.PromptTokens)
}

func requireVolcengineIntegration(t *testing.T) (*VolcengineProvider, VolcengineConfig) {
	t.Helper()
	cfg := LoadVolcengineConfigFromEnv()
	if !cfg.Enabled {
		t.Skip("set RUN_VOLCENGINE_INTEGRATION=1 to run Volcengine integration tests")
	}
	if cfg.APIKey == "" {
		t.Skip("set VOLCENGINE_API_KEY to run Volcengine integration tests")
	}
	if len(cfg.Models) == 0 {
		t.Skip("set at least one VOLCENGINE_MODEL_N to run Volcengine integration tests")
	}
	provider, err := NewVolcengineProvider(cfg)
	require.NoError(t, err)
	return provider, cfg
}

func requireModel(t *testing.T, cfg VolcengineConfig, index int) string {
	t.Helper()
	if len(cfg.Models) <= index || strings.TrimSpace(cfg.Models[index]) == "" {
		t.Skipf("set VOLCENGINE_MODEL_%d to run this integration test", index+1)
	}
	return cfg.Models[index]
}

func shortRequest(model, prompt string) cachepkg.Request {
	temp := 0.0
	maxTokens := 32
	return cachepkg.Request{
		Model:       model,
		Messages:    []cachepkg.Message{{Role: "user", Content: prompt}},
		Temperature: &temp,
		MaxTokens:   &maxTokens,
	}
}
