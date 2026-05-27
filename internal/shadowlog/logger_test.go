package shadowlog

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cachepkg "github.com/bianjiefilm/CacheSafety-Bench/internal/cache"

	"github.com/stretchr/testify/require"
)

func TestLoggerDisabledByDefaultDoesNotWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "real_logs.jsonl")
	logger := New(false, path)

	require.NoError(t, logger.Log(context.Background(), Record{
		Model:    "provider-model-id",
		Messages: []cachepkg.Message{{Role: "user", Content: "hello"}},
		Response: "world",
	}))

	_, err := os.Stat(path)
	require.True(t, os.IsNotExist(err))
}

func TestLoggerEnabledWritesRedactedJSONL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "real_logs.jsonl")
	logger := New(true, path)
	logger.now = func() time.Time { return time.Date(2026, 5, 17, 1, 2, 3, 0, time.UTC) }

	require.NoError(t, logger.Log(context.Background(), Record{
		Model: "provider-full-model-id",
		Messages: []cachepkg.Message{{
			Role:    "user",
			Content: "Email a@example.com and call +1 415 555 1212. Authorization: Bearer tokenvalue",
		}},
		Response:     "Order 1234567890. See https://example.com/order",
		Usage:        Usage{PromptTokens: 12, CompletionTokens: 34, TotalTokens: 46},
		LatencyMS:    99,
		Status:       "success",
		FinishReason: "stop",
	}))

	var got Record
	readOneRecord(t, path, &got)
	require.Equal(t, "2026-05-17T01:02:03Z", got.Timestamp)
	require.True(t, strings.HasPrefix(got.Model, "model_"))
	require.NotContains(t, got.Model, "provider-full-model-id")
	require.Equal(t, 12, got.Usage.PromptTokens)
	require.Equal(t, 34, got.Usage.CompletionTokens)
	require.Equal(t, int64(99), got.LatencyMS)
	require.Equal(t, "stop", got.FinishReason)
	require.True(t, got.RedactionApplied)

	payload := readFile(t, path)
	require.NotContains(t, payload, "a@example.com")
	require.NotContains(t, payload, "415 555 1212")
	require.NotContains(t, payload, "https://example.com")
	require.NotContains(t, payload, "1234567890")
	require.NotContains(t, payload, "Bearer")
	require.Contains(t, payload, "[REDACTED_EMAIL]")
	require.Contains(t, payload, "[REDACTED_PHONE]")
	require.Contains(t, payload, "[REDACTED_URL]")
	require.Contains(t, payload, "[REDACTED_NUMBER]")
	require.Contains(t, payload, "[REDACTED_SECRET]")
}

func TestRedactTextRules(t *testing.T) {
	input := "api_key=secretvalue email x@y.com phone +1 415 555 1212 url https://example.com id 123456789"
	got := RedactText(input)

	require.True(t, got.Changed)
	require.Contains(t, got.Value, "[REDACTED_SECRET]")
	require.Contains(t, got.Value, "[REDACTED_EMAIL]")
	require.Contains(t, got.Value, "[REDACTED_PHONE]")
	require.Contains(t, got.Value, "[REDACTED_URL]")
	require.Contains(t, got.Value, "[REDACTED_NUMBER]")
	require.NotContains(t, got.Value, "secretvalue")
	require.NotContains(t, got.Value, "x@y.com")
	require.NotContains(t, got.Value, "415 555 1212")
	require.NotContains(t, got.Value, "https://example.com")
	require.NotContains(t, got.Value, "123456789")
}

func TestLogBestEffortIgnoresWriteFailure(t *testing.T) {
	dir := t.TempDir()
	logger := New(true, dir)
	require.NotPanics(t, func() {
		logger.LogBestEffort(context.Background(), Record{
			Model:    "provider-model-id",
			Messages: []cachepkg.Message{{Role: "user", Content: "hello"}},
			Response: "world",
		})
	})
}

func TestFromEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "real_logs.jsonl")
	t.Setenv(EnvEnableShadowTrafficLog, "1")
	t.Setenv(EnvShadowTrafficLogPath, path)

	logger := FromEnv()
	require.True(t, logger.Enabled())
	require.Equal(t, path, logger.Path())
}

func readOneRecord(t *testing.T, path string, out *Record) {
	t.Helper()
	file, err := os.Open(path)
	require.NoError(t, err)
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	require.True(t, scanner.Scan())
	require.NoError(t, json.Unmarshal(scanner.Bytes(), out))
	require.NoError(t, scanner.Err())
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	payload, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(payload)
}
