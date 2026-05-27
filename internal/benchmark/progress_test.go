package benchmark

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cachepkg "github.com/bianjiefilm/CacheSafety-Bench/internal/cache"

	"github.com/stretchr/testify/require"
)

func TestProgressLogWritesStagesAndCheckpoint(t *testing.T) {
	dir := t.TempDir()
	progressPath := filepath.Join(dir, "progress.jsonl")
	checkpointPath := filepath.Join(dir, "checkpoint.json")
	cases := []Case{{
		ID:          "sample-1",
		SampleIndex: 7,
		Category:    "general",
		Request:     testBenchRequest("Explain cache keys."),
		FreshAnswer: "Cache keys identify entries.",
		FreshCost:   1,
		CachedCost:  0.1,
	}}

	_, err := RunWithEmbedding(context.Background(), cases, Config{
		ProgressLogPath: progressPath,
		CheckpointPath:  checkpointPath,
	}, nil, nil, nil)

	require.NoError(t, err)
	records := readProgressRecords(t, progressPath)
	require.NotEmpty(t, records)
	require.Contains(t, progressStages(records), ProgressStageProvider)
	require.Contains(t, progressStages(records), ProgressStageCacheSearch)
	require.Contains(t, progressStages(records), ProgressStageDone)
	require.Equal(t, 7, records[len(records)-1].SampleIndex)
	require.FileExists(t, checkpointPath)
}

func TestProgressLogRecordsTimeoutAndRedactsSensitiveError(t *testing.T) {
	dir := t.TempDir()
	progressPath := filepath.Join(dir, "progress.jsonl")
	cases := []Case{{
		ID:        "sample-timeout",
		Request:   testBenchRequest("Explain cache keys."),
		FreshCost: 1,
	}}
	fresh := func(ctx context.Context, item Case) (cachepkg.Response, error) {
		<-ctx.Done()
		return cachepkg.Response{}, errors.New(ctx.Err().Error() + " Request id: abc123 ark-12345678-secret")
	}

	_, err := RunWithEmbedding(context.Background(), cases, Config{
		ProgressLogPath:  progressPath,
		TimeoutPerSample: time.Millisecond,
	}, fresh, nil, nil)

	require.Error(t, err)
	records := readProgressRecords(t, progressPath)
	require.Contains(t, progressStages(records), ProgressStageTimeout)
	payload, readErr := os.ReadFile(progressPath)
	require.NoError(t, readErr)
	require.NotContains(t, string(payload), "abc123")
	require.NotContains(t, string(payload), "ark-12345678-secret")
	require.Contains(t, string(payload), "[redacted]")
}

func TestProgressLogWritesWriteLogStage(t *testing.T) {
	dir := t.TempDir()
	progressPath := filepath.Join(dir, "progress.jsonl")
	cases := []Case{{
		ID:          "sample-write-log",
		Request:     testBenchRequest("Explain cache keys."),
		FreshAnswer: "Cache keys identify entries.",
		FreshCost:   1,
		CachedCost:  0.1,
	}}

	_, err := RunWithEmbedding(context.Background(), cases, Config{ProgressLogPath: progressPath}, nil, nil, nil)

	require.NoError(t, err)
	require.Contains(t, progressStages(readProgressRecords(t, progressPath)), ProgressStageWriteLog)
}

func TestBuildProgressSummarySkipsInvalidLines(t *testing.T) {
	dir := t.TempDir()
	progressPath := filepath.Join(dir, "progress.jsonl")
	payload := strings.Join([]string{
		`{"sample_id":"a","category":"general","stage":"done","elapsed_ms":10}`,
		`}`,
		"\x00" + `{"sample_id":"b","category":"general","stage":"timeout","elapsed_ms":20,"error_summary":"context deadline exceeded"}`,
	}, "\n")
	require.NoError(t, os.WriteFile(progressPath, []byte(payload), 0o644))

	summary, err := BuildProgressSummary([]string{progressPath})

	require.NoError(t, err)
	require.Equal(t, 2, summary.TotalRecords)
	require.Equal(t, 1, summary.TimeoutCount)
	require.Len(t, summary.Warnings, 1)
	require.Len(t, summary.SlowestSamples, 1)
	require.Equal(t, "a", summary.SlowestSamples[0].SampleID)
}

func readProgressRecords(t *testing.T, path string) []ProgressRecord {
	t.Helper()
	payload, err := os.ReadFile(path)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(payload)), "\n")
	records := make([]ProgressRecord, 0, len(lines))
	for _, line := range lines {
		var record ProgressRecord
		require.NoError(t, json.Unmarshal([]byte(line), &record))
		records = append(records, record)
	}
	return records
}

func progressStages(records []ProgressRecord) []string {
	out := make([]string, 0, len(records))
	for _, record := range records {
		out = append(out, record.Stage)
	}
	return out
}
