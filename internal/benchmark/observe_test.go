package benchmark

import (
	"context"
	"testing"

	cachepkg "github.com/bianjiefilm/CacheSafety-Bench/internal/cache"
	"github.com/bianjiefilm/CacheSafety-Bench/internal/teacher"

	"github.com/stretchr/testify/require"
)

func TestObservedServeModeScoresExactHitInsteadOfLocalMiss(t *testing.T) {
	cases := []Case{{
		ID:           "obs-exact",
		Category:     "support",
		Request:      testBenchRequest("new request that would miss locally"),
		FreshAnswer:  "gateway cached reply",
		TeacherScore: 10,
		FreshCost:    1,
		CachedCost:   0.1,
		OldRequest:   "old request",
		OldAnswer:    "old answer",
	}}

	got, err := RunWithEmbedding(context.Background(), cases, Config{
		CacheSource: "observed",
		Dataset:     "testdata/observe.jsonl",
		JudgeMode:   "fake",
	}, observeFresh("exact_cache", "gateway cached reply", "req-exact", "hash-exact", "https://example.test/r/exact"), teacher.FakeJudge{}, nil)

	require.NoError(t, err)
	require.Equal(t, "observed", got.CacheSource)
	require.Equal(t, 1, got.LayerContribution["exact"])
	require.Equal(t, 0, got.LayerContribution["miss"])
	require.Equal(t, 1.0, got.HitRate)
	require.Equal(t, 1.0, got.SafeHitRate)
	require.Equal(t, 0.0, got.BadHitRate)
	require.Len(t, got.DecisionRecords, 1)
	record := got.DecisionRecords[0]
	require.Equal(t, "exact", record.CacheLayer)
	require.Equal(t, "observed", record.CacheSource)
	require.Equal(t, "exact_cache", record.ObservedServeMode)
	require.Equal(t, "req-exact", record.ObservedRequestID)
	require.Equal(t, "hash-exact", record.ObservedReceiptHash)
	require.Equal(t, "https://example.test/r/exact", record.ObservedReceiptURL)
}

func TestObservedCanonicalAndLNBetaMapToNamedLayers(t *testing.T) {
	cases := []Case{
		{
			ID:           "obs-canonical",
			Request:      testBenchRequest("canonical new"),
			FreshAnswer:  "canonical reply",
			TeacherScore: 10,
			FreshCost:    1,
			CachedCost:   0.1,
			OldRequest:   "canonical old",
			OldAnswer:    "old",
		},
		{
			ID:           "obs-ln-beta",
			Request:      testBenchRequest("semantic new"),
			FreshAnswer:  "ln reply",
			TeacherScore: 10,
			FreshCost:    1,
			CachedCost:   0.1,
			OldRequest:   "semantic old",
			OldAnswer:    "old",
		},
	}
	fresh := func(ctx context.Context, item Case) (cachepkg.Response, error) {
		mode := "canonical_cache"
		text := "canonical reply"
		if item.ID == "obs-ln-beta" {
			mode = "ln_beta"
			text = "ln reply"
		}
		return cachepkg.Response{
			Text:         text,
			TeacherScore: item.TeacherScore,
			Observation:  cachepkg.Observation{ServeMode: mode},
		}, nil
	}

	got, err := RunWithEmbedding(context.Background(), cases, Config{CacheSource: "observed", JudgeMode: "fake"}, fresh, teacher.FakeJudge{}, nil)

	require.NoError(t, err)
	require.Equal(t, 1, got.LayerContribution["canonical"])
	require.Equal(t, 1, got.LayerContribution["ln_beta"])
	require.Equal(t, 0, got.LayerContribution["miss"])
	require.Equal(t, 1.0, got.HitRate)
}

func TestObservedUnknownServeModeIsMiss(t *testing.T) {
	cases := []Case{{
		ID:           "obs-miss",
		Request:      testBenchRequest("same prompt"),
		FreshAnswer:  "fresh from gateway",
		TeacherScore: 10,
		FreshCost:    1,
		CachedCost:   0.1,
		OldRequest:   "same prompt",
		OldAnswer:    "seeded local answer",
	}}

	got, err := RunWithEmbedding(context.Background(), cases, Config{CacheSource: "observed"}, observeFresh("fresh", "fresh from gateway", "req-miss", "", ""), nil, nil)

	require.NoError(t, err)
	require.Equal(t, 1, got.LayerContribution["miss"])
	require.Equal(t, 0, got.LayerContribution["exact"])
	require.Equal(t, 0.0, got.HitRate)
	require.Equal(t, "fresh", got.DecisionRecords[0].ObservedServeMode)
	require.Equal(t, "miss", got.DecisionRecords[0].CacheLayer)
}

func TestSimulatedRunKeepsLocalPipelineLayerWhenHeadersPresent(t *testing.T) {
	cases := []Case{{
		ID:           "sim-miss",
		Request:      testBenchRequest("unique prompt"),
		FreshAnswer:  "provider answer",
		TeacherScore: 10,
		FreshCost:    1,
		CachedCost:   0.1,
	}}

	got, err := RunWithEmbedding(context.Background(), cases, Config{}, observeFresh("exact_cache", "provider answer", "req-sim", "hash-sim", ""), nil, nil)

	require.NoError(t, err)
	require.Equal(t, "", got.CacheSource)
	require.Equal(t, 1, got.LayerContribution["miss"])
	require.Equal(t, 0, got.LayerContribution["exact"])
	require.Equal(t, "miss", got.DecisionRecords[0].CacheLayer)
	require.Equal(t, "exact_cache", got.DecisionRecords[0].ObservedServeMode)
	require.Equal(t, "req-sim", got.DecisionRecords[0].ObservedRequestID)
}

func observeFresh(mode, text, requestID, receiptHash, receiptURL string) FreshAnswerFunc {
	return func(ctx context.Context, item Case) (cachepkg.Response, error) {
		return cachepkg.Response{
			Text:         text,
			TeacherScore: item.TeacherScore,
			Observation: cachepkg.Observation{
				ServeMode:   mode,
				RequestID:   requestID,
				ReceiptHash: receiptHash,
				ReceiptURL:  receiptURL,
			},
		}, nil
	}
}
