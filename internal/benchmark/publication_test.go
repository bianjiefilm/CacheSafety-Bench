package benchmark

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	cachepkg "github.com/bianjiefilm/CacheSafety-Bench/internal/cache"

	"github.com/stretchr/testify/require"
)

func TestLabScorecardUsesTotalDenominator(t *testing.T) {
	cases := []Case{
		{
			ID:           "lab-seed",
			Request:      testBenchRequest("same prompt"),
			FreshAnswer:  "ok",
			TeacherScore: 10,
			FreshCost:    1,
			CachedCost:   0.1,
		},
		{
			ID:           "lab-hit",
			Request:      testBenchRequest("same prompt"),
			FreshAnswer:  "ok",
			TeacherScore: 10,
			FreshCost:    1,
			CachedCost:   0.1,
		},
	}
	got, err := Run(context.Background(), cases, Config{})
	require.NoError(t, err)
	require.Equal(t, 2, got.Total)
	require.Equal(t, 0.5, got.SafeHitRate)
	require.Equal(t, "", got.CacheSource)
}

func TestNormalizeScorecardDefaultsToLab(t *testing.T) {
	got, err := NormalizeScorecard("")
	require.NoError(t, err)
	require.Equal(t, ScorecardLab, got)
	got, err = NormalizeScorecard("publication")
	require.NoError(t, err)
	require.Equal(t, ScorecardPublication, got)
	_, err = NormalizeScorecard("unknown")
	require.Error(t, err)
}

func TestScorePublicationNodeFixtures(t *testing.T) {
	tests := []struct {
		name       string
		records    []PublicationRecord
		price      float64
		wantSH     *float64
		wantBH     *float64
		wantTrap   *float64
		wantCost   *float64
		wantSample int
		wantModes  map[string]int
	}{
		{
			name: "all ten repeat-seconds exact_cache correct, traps and fresh miss",
			records: concatRecords(
				repeatSeconds(10, "exact_cache", "France is not this: 100 366 carbon dioxide CO2 12 Everest 32 lion six 6 whale", true),
				trapSeconds(10, "fresh", "Argentina Vienna five 5 Ag Jupiter 1989 Sahara Orwell won", nil),
				freshRecords(10, "fresh", "2 hydrogen diamond Canada nitrogen 206 Einstein 7 octopus", false),
			),
			wantSH:     rate4(1.0),
			wantBH:     rate4(0),
			wantTrap:   rate4(0),
			wantSample: 30,
			wantModes:  map[string]int{"exact_cache": 10, "fresh": 20},
		},
		{
			name: "eight of ten repeat-seconds hit and correct (gpt-5-mini shape)",
			records: concatRecords(
				repeatSeconds(8, "exact_cache", "100 366 carbon dioxide CO2 12 Everest 32 lion six 6 whale", true),
				repeatSeconds(2, "fresh", "100 366 carbon dioxide CO2 12 Everest 32 lion six 6 whale", true),
				trapSeconds(10, "fresh", "Argentina", nil),
				freshRecords(10, "fresh", "2", false),
			),
			wantSH:     rate4(0.8),
			wantBH:     rate4(0),
			wantTrap:   rate4(0),
			wantSample: 30,
		},
		{
			name: "trap-second hit whose content contains all staleKeywords is a bad hit",
			records: concatRecords(
				repeatSeconds(1, "exact_cache", "100", true),
				[]PublicationRecord{{
					Class:          ClassTrapSecond,
					ServeMode:      "canonical_cache",
					Content:        "France won in 2018",
					ExpectKeywords: []string{"Argentina"},
					StaleKeywords:  []string{"France"},
				}},
				freshRecords(1, "fresh", "2", false),
			),
			wantSH:     rate4(1.0),
			wantBH:     rate4(1.0),
			wantTrap:   rate4(0),
			wantSample: 3,
		},
		{
			name: "fresh hit counts as semantic trap failure",
			records: concatRecords(
				repeatSeconds(1, "exact_cache", "100", true),
				trapSeconds(1, "fresh", "Argentina", nil),
				[]PublicationRecord{{
					Class:          ClassFresh,
					ServeMode:      "ln_beta",
					Content:        "2",
					ExpectKeywords: []string{"2"},
				}},
			),
			wantSH:     rate4(1.0),
			wantBH:     rate4(0),
			wantTrap:   rate4(1.0),
			wantSample: 3,
		},
		{
			name: "repeat-second hit missing expectKeywords is not a safe hit",
			records: []PublicationRecord{{
				Class:          ClassRepeatSecond,
				ServeMode:      "exact_cache",
				Content:        "about one hundred degrees",
				ExpectKeywords: []string{"100"},
			}},
			wantSH:     rate4(0),
			wantBH:     nil,
			wantTrap:   nil,
			wantSample: 1,
		},
		{
			name:       "empty cohort rates are null not 0.0",
			records:    nil,
			wantSH:     nil,
			wantBH:     nil,
			wantTrap:   nil,
			wantSample: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ScorePublication(tc.records, tc.price)
			require.Equal(t, tc.wantSample, got.SampleSize)
			require.Equal(t, tc.wantSH, got.SafeHitRate)
			require.Equal(t, tc.wantBH, got.BadHitRate)
			require.Equal(t, tc.wantTrap, got.SemanticTrapFailureRate)
			require.Equal(t, tc.wantCost, got.CostSavedPer1KRequests)
			if tc.wantModes != nil {
				require.Equal(t, tc.wantModes, got.ServedModeCounts)
			}
			payload, err := json.Marshal(got)
			require.NoError(t, err)
			if tc.wantSH == nil {
				require.Contains(t, string(payload), `"safe_hit_rate":null`)
			}
			if tc.wantBH == nil {
				require.Contains(t, string(payload), `"bad_hit_rate":null`)
			}
			if tc.wantTrap == nil {
				require.Contains(t, string(payload), `"semantic_trap_failure_rate":null`)
			}
			if tc.wantCost == nil {
				require.Contains(t, string(payload), `"cost_saved_per_1k_requests":null`)
			}
		})
	}
}

func TestScorePublicationCostSavedOnlyWithUsageAndPrice(t *testing.T) {
	prompt := 100
	cached := 40
	records := []PublicationRecord{
		{Class: ClassRepeatSecond, ServeMode: "fresh", Content: "100", ExpectKeywords: []string{"100"}, PromptTokens: &prompt},
		{Class: ClassFresh, ServeMode: "fresh", Content: "2", ExpectKeywords: []string{"2"}, PromptTokens: &prompt, CachedTokens: &cached},
	}

	withoutPrice := ScorePublication(records, 0)
	require.Nil(t, withoutPrice.CostSavedPer1KRequests)

	missingCached := ScorePublication([]PublicationRecord{
		{Class: ClassFresh, ServeMode: "fresh", Content: "2", PromptTokens: &prompt},
	}, 2.5)
	require.Nil(t, missingCached.CostSavedPer1KRequests)

	got := ScorePublication(records, 2.5)
	// avgCachedPerCall = 40 / 2 = 20; cost = 20 * 1000 * 2.5 / 1_000_000 = 0.05
	require.Equal(t, rate4(0.05), got.CostSavedPer1KRequests)
}

func TestPublicationHitModesAreCaseSensitiveServeModesOrMappedLayers(t *testing.T) {
	require.True(t, IsPublicationHit("exact_cache", ""))
	require.True(t, IsPublicationHit("canonical_cache", ""))
	require.True(t, IsPublicationHit("ln_beta", ""))
	require.False(t, IsPublicationHit("EXACT_CACHE", ""))
	require.False(t, IsPublicationHit("fresh", ""))
	require.True(t, IsPublicationHit("", "exact"))
	require.True(t, IsPublicationHit("fresh", "canonical"))
	require.True(t, IsPublicationHit("", "ln_beta"))
	require.False(t, IsPublicationHit("fresh", "semantic"))
	require.False(t, IsPublicationHit("fresh", "miss"))
}

func TestPublicationScorecardDoesNotUseLabTotalDenominator(t *testing.T) {
	records := concatRecords(
		[]PublicationRecord{{Class: ClassRepeatFirst, ServeMode: "fresh", Content: "seed"}},
		repeatSeconds(1, "exact_cache", "100", true),
		[]PublicationRecord{{Class: ClassTrapFirst, ServeMode: "fresh", Content: "seed"}},
		trapSeconds(1, "fresh", "Argentina", nil),
		freshRecords(1, "fresh", "2", false),
	)
	got := ScorePublication(records, 0)
	require.Equal(t, 5, got.SampleSize)
	require.Equal(t, rate4(1.0), got.SafeHitRate)
	require.Equal(t, rate4(0), got.BadHitRate)
	require.Equal(t, rate4(0), got.SemanticTrapFailureRate)
}

func TestRunPublicationRequiresObserve(t *testing.T) {
	set, err := LoadPromptSet(filepath.Join("..", "..", "examples", "promptset_v3.json"))
	require.NoError(t, err)

	_, _, err = RunPublication(context.Background(), set, Config{}, func(ctx context.Context, item Case) (cachepkg.Response, error) {
		return cachepkg.Response{Text: "100", Observation: cachepkg.Observation{ServeMode: "exact_cache"}}, nil
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "observe")
}

func TestRunPublicationScoresObservedServeModeNotLabPipeline(t *testing.T) {
	set := PromptSet{
		Version: 3,
		TrapPairs: []PromptPair{{
			PairID: "trap-01",
			First:  PromptTurn{ID: "trap-01-a", Text: "Who won the FIFA World Cup in 2018? Reply with the country name only.", ExpectKeywords: []string{"France"}},
			Second: PromptTurn{ID: "trap-01-b", Text: "Who won the FIFA World Cup in 2022? Reply with the country name only.", ExpectKeywords: []string{"Argentina"}, StaleKeywords: []string{"France"}},
		}},
		RepeatPairs: []PromptPair{{
			PairID: "stable-01",
			First:  PromptTurn{ID: "stable-01-a", Text: "What is the boiling point of water at sea level in Celsius? Reply with the number only.", ExpectKeywords: []string{"100"}},
			Second: PromptTurn{ID: "stable-01-b", Text: "What is the boiling point of water at sea level in Celsius? Reply with the number only.", ExpectKeywords: []string{"100"}},
		}},
		FreshQuestions: []PromptTurn{{
			ID: "fresh-01", Text: "What is the smallest prime number? Reply with the number only.", ExpectKeywords: []string{"2"},
		}},
	}

	byID := map[string]cachepkg.Response{
		"trap-01-a":   {Text: "France", Observation: cachepkg.Observation{ServeMode: "fresh"}},
		"trap-01-b":   {Text: "Argentina", Observation: cachepkg.Observation{ServeMode: "fresh"}},
		"stable-01-a": {Text: "100", Observation: cachepkg.Observation{ServeMode: "fresh"}},
		"stable-01-b": {Text: "100", Observation: cachepkg.Observation{ServeMode: "exact_cache"}},
		"fresh-01":    {Text: "2", Observation: cachepkg.Observation{ServeMode: "fresh"}},
	}
	score, decisions, err := RunPublication(context.Background(), set, Config{CacheSource: "observed"}, func(ctx context.Context, item Case) (cachepkg.Response, error) {
		resp, ok := byID[item.ID]
		require.True(t, ok, item.ID)
		return resp, nil
	})
	require.NoError(t, err)
	require.Equal(t, 5, score.SampleSize)
	require.Equal(t, rate4(1.0), score.SafeHitRate)
	require.Equal(t, rate4(0), score.BadHitRate)
	require.Equal(t, rate4(0), score.SemanticTrapFailureRate)
	require.Len(t, decisions, 5)
	require.Equal(t, "exact_cache", decisions[3].ObservedServeMode)
	require.Equal(t, "exact", decisions[3].CacheLayer)
}

func rate4(value float64) *float64 {
	rounded := RoundPublicationRate(value)
	return &rounded
}

func concatRecords(groups ...[]PublicationRecord) []PublicationRecord {
	var out []PublicationRecord
	for _, group := range groups {
		out = append(out, group...)
	}
	return out
}

func repeatSeconds(n int, mode, content string, includeKeyword bool) []PublicationRecord {
	out := make([]PublicationRecord, n)
	text := content
	if !includeKeyword {
		text = "missing"
	}
	for i := 0; i < n; i++ {
		out[i] = PublicationRecord{
			Class:          ClassRepeatSecond,
			ServeMode:      mode,
			Content:        text,
			ExpectKeywords: []string{"100"},
		}
	}
	return out
}

func trapSeconds(n int, mode, content string, stale []string) []PublicationRecord {
	out := make([]PublicationRecord, n)
	for i := 0; i < n; i++ {
		out[i] = PublicationRecord{
			Class:          ClassTrapSecond,
			ServeMode:      mode,
			Content:        content,
			ExpectKeywords: []string{"Argentina"},
			StaleKeywords:  stale,
		}
	}
	return out
}

func freshRecords(n int, mode, content string, hit bool) []PublicationRecord {
	out := make([]PublicationRecord, n)
	serve := mode
	if hit && serve == "fresh" {
		serve = "exact_cache"
	}
	for i := 0; i < n; i++ {
		out[i] = PublicationRecord{
			Class:          ClassFresh,
			ServeMode:      serve,
			Content:        content,
			ExpectKeywords: []string{"2"},
		}
	}
	return out
}
