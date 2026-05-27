package benchmark

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStratifiedSampleByCategory(t *testing.T) {
	cases := []Case{
		{ID: "a1", Category: "general_explanation"},
		{ID: "a2", Category: "general_explanation"},
		{ID: "b1", Category: "howto_guidance"},
	}
	got, meta := selectCases(cases, Config{Stratified: true, StratifyBy: []string{"category"}, MaxPerStratum: 1, Seed: 42})
	require.Len(t, got, 2)
	require.Equal(t, "stratified", meta.Mode)
	require.Equal(t, 2, meta.StrataCount)
	require.Equal(t, 1, meta.SampledByStratum["category=general_explanation"])
	require.Equal(t, 1, meta.SampledByStratum["category=howto_guidance"])
}

func TestStratifiedSampleByMultipleFieldsStable(t *testing.T) {
	cases := []Case{
		{ID: "a1", Category: "general_explanation", SourceKind: "safe_paraphrase", ExpectedCacheBehavior: "semantic_safe"},
		{ID: "a2", Category: "general_explanation", SourceKind: "safe_paraphrase", ExpectedCacheBehavior: "semantic_safe"},
		{ID: "a3", Category: "general_explanation", SourceKind: "rule_based", ExpectedCacheBehavior: "semantic_safe"},
	}
	first, meta1 := selectCases(cases, Config{Stratified: true, StratifyBy: []string{"category", "source_kind", "expected_cache_behavior"}, MaxPerStratum: 1, Seed: 42})
	second, meta2 := selectCases(cases, Config{Stratified: true, StratifyBy: []string{"category", "source_kind", "expected_cache_behavior"}, MaxPerStratum: 1, Seed: 42})
	require.Equal(t, first, second)
	require.Equal(t, meta1.SampledByStratum, meta2.SampledByStratum)
	require.Equal(t, 2, meta1.StrataCount)
}

func TestStratifiedSampleSmallStratumAllTaken(t *testing.T) {
	cases := []Case{
		{ID: "a1", Category: "general_explanation"},
		{ID: "a2", Category: "general_explanation"},
		{ID: "b1", Category: "howto_guidance"},
	}
	got, _ := selectCases(cases, Config{Stratified: true, StratifyBy: []string{"category"}, MaxPerStratum: 3, Seed: 42})
	require.Len(t, got, 3)
}

func TestDefaultMaxSamplesBehaviorUnchanged(t *testing.T) {
	cases := []Case{{ID: "1"}, {ID: "2"}, {ID: "3"}}
	got, meta := selectCases(cases, Config{MaxSamples: 2})
	require.Len(t, got, 2)
	require.Equal(t, "head", meta.Mode)
	require.Equal(t, "1", got[0].ID)
	require.Equal(t, "2", got[1].ID)
}

func TestStartEndIndexSelectsRangeBeforeMaxSamples(t *testing.T) {
	cases := []Case{{ID: "0"}, {ID: "1"}, {ID: "2"}, {ID: "3"}, {ID: "4"}}

	got, meta := selectCases(cases, Config{StartIndex: 1, EndIndex: 4, MaxSamples: 2})

	require.Equal(t, "head", meta.Mode)
	require.Equal(t, []Case{{ID: "1"}, {ID: "2"}}, got)
}

func TestStratifiedMaxSamplesCapsTotalAcrossStrata(t *testing.T) {
	cases := []Case{
		{ID: "a1", Category: "general_explanation"},
		{ID: "a2", Category: "general_explanation"},
		{ID: "a3", Category: "general_explanation"},
		{ID: "b1", Category: "hard_negative"},
		{ID: "b2", Category: "hard_negative"},
		{ID: "b3", Category: "hard_negative"},
	}

	got, meta := selectCases(cases, Config{Stratified: true, StratifyBy: []string{"category"}, MaxSamples: 4, Seed: 42})

	require.Len(t, got, 4)
	require.Equal(t, "stratified", meta.Mode)
	require.Equal(t, 2, meta.StrataCount)
	require.Equal(t, 2, meta.SampledByStratum["category=general_explanation"])
	require.Equal(t, 2, meta.SampledByStratum["category=hard_negative"])
}

func TestOnlyReplayPairsFiltersBeforeRangeAndMaxSamples(t *testing.T) {
	cases := []Case{
		{ID: "single-0", Kind: "single_request"},
		{ID: "pair-1", Kind: "replay_pair"},
		{ID: "pair-2", OldRequest: "old", OldAnswer: "answer"},
		{ID: "single-3", Kind: "single_request"},
		{ID: "pair-4", Kind: "replay_pair"},
	}

	got, meta := selectCases(cases, Config{OnlyReplayPairs: true, StartIndex: 1, MaxSamples: 1})

	require.Equal(t, "head", meta.Mode)
	require.Equal(t, []Case{{ID: "pair-2", OldRequest: "old", OldAnswer: "answer"}}, got)
}
