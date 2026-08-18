package benchmark

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	cachepkg "github.com/bianjiefilm/CacheSafety-Bench/internal/cache"
)

const (
	ScorecardLab         = "lab"
	ScorecardPublication = "publication"
)

type PublicationRecord struct {
	Class          string
	ServeMode      string
	Layer          string
	Content        string
	ExpectKeywords []string
	StaleKeywords  []string
	PromptTokens   *int
	CachedTokens   *int
}

type PublicationScorecard struct {
	Scorecard               string         `json:"scorecard"`
	PromptSetVersion        int            `json:"promptset_version,omitempty"`
	SafeHitRate             *float64       `json:"safe_hit_rate"`
	BadHitRate              *float64       `json:"bad_hit_rate"`
	SemanticTrapFailureRate *float64       `json:"semantic_trap_failure_rate"`
	CostSavedPer1KRequests  *float64       `json:"cost_saved_per_1k_requests"`
	ServedModeCounts        map[string]int `json:"served_mode_counts"`
	SampleSize              int            `json:"sample_size"`
}

func ScorePublication(records []PublicationRecord, inputPriceUsdPer1m float64) PublicationScorecard {
	score := PublicationScorecard{
		Scorecard:        ScorecardPublication,
		ServedModeCounts: map[string]int{},
		SampleSize:       len(records),
	}
	var repeatTotal, repeatSafe int
	var trapTotal, trapBad int
	var freshTotal, freshHits int
	var promptTokenRecords, cachedTokenRecords int
	var cachedTokenSum int
	for _, record := range records {
		score.ServedModeCounts[record.ServeMode]++
		hit := IsPublicationHit(record.ServeMode, record.Layer)
		switch record.Class {
		case ClassRepeatSecond:
			repeatTotal++
			if hit && containsAllKeywords(record.Content, record.ExpectKeywords) {
				repeatSafe++
			}
		case ClassTrapSecond:
			trapTotal++
			if hit && containsAllKeywords(record.Content, record.StaleKeywords) {
				trapBad++
			}
		case ClassFresh:
			freshTotal++
			if hit {
				freshHits++
			}
		}
		if record.PromptTokens != nil {
			promptTokenRecords++
		}
		if record.CachedTokens != nil {
			cachedTokenRecords++
			cachedTokenSum += *record.CachedTokens
		}
	}
	score.SafeHitRate = publicationRate(repeatSafe, repeatTotal)
	score.BadHitRate = publicationRate(trapBad, trapTotal)
	score.SemanticTrapFailureRate = publicationRate(freshHits, freshTotal)
	if promptTokenRecords > 0 && cachedTokenRecords > 0 && inputPriceUsdPer1m > 0 {
		avgCachedPerCall := float64(cachedTokenSum) / float64(promptTokenRecords)
		cost := (avgCachedPerCall * 1000 * inputPriceUsdPer1m) / 1_000_000
		score.CostSavedPer1KRequests = &cost
	}
	return score
}

func IsPublicationHit(serveMode, layer string) bool {
	switch serveMode {
	case cachepkg.ServeModeExactCache, cachepkg.ServeModeCanonicalCache, cachepkg.ServeModeLNBeta:
		return true
	}
	switch layer {
	case string(cachepkg.LayerExact), string(cachepkg.LayerCanonical), string(cachepkg.LayerLNBeta):
		return true
	}
	return false
}

func RoundPublicationRate(value float64) float64 {
	parsed, err := strconv.ParseFloat(strconv.FormatFloat(value, 'f', 4, 64), 64)
	if err != nil {
		return value
	}
	return parsed
}

func publicationRate(hits, total int) *float64 {
	if total == 0 {
		return nil
	}
	rate := RoundPublicationRate(float64(hits) / float64(total))
	return &rate
}

func containsAllKeywords(content string, keywords []string) bool {
	haystack := strings.ToLower(content)
	for _, keyword := range keywords {
		if !strings.Contains(haystack, strings.ToLower(keyword)) {
			return false
		}
	}
	return true
}

func RunPublication(ctx context.Context, set PromptSet, cfg Config, fresh FreshAnswerFunc) (PublicationScorecard, []DecisionRecord, error) {
	if !observesGateway(cfg) {
		return PublicationScorecard{}, nil, fmt.Errorf("publication scorecard requires -observe so gateway serve-mode is used; refusing to score the lab pipeline as publication")
	}
	if fresh == nil {
		return PublicationScorecard{}, nil, fmt.Errorf("publication scorecard requires a gateway provider")
	}
	steps := WalkPromptSet(set)
	records := make([]PublicationRecord, 0, len(steps))
	decisions := make([]DecisionRecord, 0, len(steps))
	for _, step := range steps {
		item := publicationCase(step)
		result, err := observeGateway(ctx, item, fresh)
		if err != nil {
			return PublicationScorecard{}, nil, fmt.Errorf("%s: %w", step.ID, err)
		}
		record := PublicationRecord{
			Class:          step.Class,
			ServeMode:      result.Response.Observation.ServeMode,
			Layer:          string(result.Layer),
			Content:        result.Response.Text,
			ExpectKeywords: step.ExpectKeywords,
			StaleKeywords:  step.StaleKeywords,
			PromptTokens:   result.Response.Observation.PromptTokens,
			CachedTokens:   result.Response.Observation.CachedTokens,
		}
		records = append(records, record)
		decisions = append(decisions, NewDecisionRecord(DecisionInput{
			Dataset:       cfg.Dataset,
			Item:          item,
			Result:        result,
			Threshold:     cfg.SemanticThreshold,
			VectorStore:   "none",
			EmbeddingMode: "disabled",
			JudgeMode:     "publication-keywords",
			CacheSource:   "observed",
		}))
	}
	score := ScorePublication(records, cfg.PublicationInputPricePer1M)
	score.PromptSetVersion = set.Version
	return score, decisions, nil
}

func publicationCase(step PromptStep) Case {
	return Case{
		ID:       step.ID,
		Kind:     step.Class,
		Category: step.Class,
		Request: cachepkg.Request{
			Model:    "publication-gateway-model",
			Messages: []cachepkg.Message{{Role: "user", Content: step.Text}},
		},
	}
}

func NormalizeScorecard(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", ScorecardLab:
		return ScorecardLab, nil
	case ScorecardPublication:
		return ScorecardPublication, nil
	default:
		return "", fmt.Errorf("unsupported scorecard %q", value)
	}
}
