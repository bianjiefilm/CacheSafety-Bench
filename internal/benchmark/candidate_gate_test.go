package benchmark

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	cachepkg "github.com/bianjiefilm/CacheSafety-Bench/internal/cache"

	"github.com/stretchr/testify/require"
)

func TestCandidateGatePassesHappyPath(t *testing.T) {
	dir := t.TempDir()
	baseRecord := gateSemanticDecision("text-001", "general_explanation", 8, 9)
	candidateRecord := gateSemanticDecision("text-001", "general_explanation", 8, 9)
	baseline := mustWriteGateManifest(t, dir, "base", gateFixtureConfig{
		semanticJudgedSafe: 1,
		safeCostSaved:      1,
		cachedRate:         1,
		records:            []DecisionRecord{baseRecord},
	})
	candidate := mustWriteGateManifest(t, dir, "cand", gateFixtureConfig{
		vectorStore:        "sqlite-int8",
		semanticRerank:     "same_prompt_answer_quality",
		topKTrace:          5,
		semanticJudgedSafe: 1,
		safeCostSaved:      1,
		cachedRate:         1,
		records:            []DecisionRecord{candidateRecord},
	})

	report, markdown, err := BuildCandidateGate(context.Background(), CandidateGateInput{
		BaselineManifestPath:  baseline,
		CandidateManifestPath: candidate,
	})
	require.NoError(t, err)
	require.Equal(t, "PASS", report.Status)
	require.True(t, report.CandidateCanBeRetained)
	require.False(t, report.DefaultEnableAllowed)
	require.Contains(t, markdown, "result: `PASS`")
}

func TestCandidateGateFailsOnNewBadHit(t *testing.T) {
	dir := t.TempDir()
	baseline := mustWriteGateManifest(t, dir, "base", gateFixtureConfig{
		semanticJudgedSafe: 1,
		safeCostSaved:      1,
		cachedRate:         1,
		records:            []DecisionRecord{gateSemanticDecision("text-001", "general_explanation", 8, 9)},
	})
	badHit := gateSemanticDecision("text-001", "general_explanation", 5, 9)
	badHit.BadHit = true
	candidate := mustWriteGateManifest(t, dir, "cand", gateFixtureConfig{
		semanticJudgedSafe: 1,
		safeCostSaved:      1,
		cachedRate:         1,
		records:            []DecisionRecord{badHit},
	})

	report, _, err := BuildCandidateGate(context.Background(), CandidateGateInput{
		BaselineManifestPath:  baseline,
		CandidateManifestPath: candidate,
	})
	require.NoError(t, err)
	require.Equal(t, "FAIL", report.Status)
	require.False(t, gatePassed(report, "new_bad_hit_zero"))
}

func TestCandidateGateFailsOnUnknownCount(t *testing.T) {
	report := runGateWithCandidateMetrics(t, func(metrics *Metrics) {
		metrics.UnknownCount = 1
	})
	require.Equal(t, "FAIL", report.Status)
	require.False(t, gatePassed(report, "unknown_count_zero"))
}

func TestCandidateGateFailsOnJudgeErrorCount(t *testing.T) {
	report := runGateWithCandidateMetrics(t, func(metrics *Metrics) {
		metrics.JudgeErrorCount = 1
	})
	require.Equal(t, "FAIL", report.Status)
	require.False(t, gatePassed(report, "judge_error_count_zero"))
}

func TestCandidateGateFailsOnSemanticJudgedSafeDrop(t *testing.T) {
	report := runGateWithCandidateMetrics(t, func(metrics *Metrics) {
		metrics.SemanticJudgedSafe = 0
	})
	require.Equal(t, "FAIL", report.Status)
	require.False(t, gatePassed(report, "semantic_judged_safe_not_down"))
}

func TestCandidateGateFailsOnSafeCostSavedDrop(t *testing.T) {
	report := runGateWithCandidateMetrics(t, func(metrics *Metrics) {
		metrics.SafeCostSaved = 0.5
	})
	require.Equal(t, "FAIL", report.Status)
	require.False(t, gatePassed(report, "safe_cost_saved_not_down"))
}

func TestCandidateGateFailsOnCachedGEFreshMinus1RateDrop(t *testing.T) {
	report := runGateWithCandidateMetrics(t, func(metrics *Metrics) {
		metrics.CachedGEFreshMinus1Rate = 0.5
	})
	require.Equal(t, "FAIL", report.Status)
	require.False(t, gatePassed(report, "cached_ge_fresh_minus_1_rate_not_down"))
}

func TestCandidateGateWarningDoesNotFail(t *testing.T) {
	dir := t.TempDir()
	baseRecord := gateSemanticDecision("text-001", "general_explanation", 9, 9)
	baseRecord.CandidateAnswerPreview = "complete answer"
	candidateRecord := gateSemanticDecision("text-001", "general_explanation", 8, 9)
	candidateRecord.CandidateAnswerPreview = "short answer"
	baseline := mustWriteGateManifest(t, dir, "base", gateFixtureConfig{
		semanticJudgedSafe: 1,
		safeCostSaved:      1,
		cachedRate:         1,
		records:            []DecisionRecord{baseRecord},
	})
	candidate := mustWriteGateManifest(t, dir, "cand", gateFixtureConfig{
		semanticJudgedSafe: 1,
		safeCostSaved:      1,
		cachedRate:         1,
		records:            []DecisionRecord{candidateRecord},
	})

	report, _, err := BuildCandidateGate(context.Background(), CandidateGateInput{
		BaselineManifestPath:  baseline,
		CandidateManifestPath: candidate,
	})
	require.NoError(t, err)
	require.Equal(t, "PASS", report.Status)
	require.True(t, warningActive(report, "answer_changed_count"))
	require.True(t, warningActive(report, "score_worsened_count"))
}

func TestWriteCandidateGateReport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gate.md")
	require.NoError(t, WriteCandidateGateReport(path, "# ok\n"))
	bytes, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "# ok\n", string(bytes))
}

type gateFixtureConfig struct {
	vectorStore        string
	semanticRerank     string
	topKTrace          int
	unknownCount       int
	judgeErrorCount    int
	semanticJudgedSafe int
	safeCostSaved      float64
	cachedRate         float64
	records            []DecisionRecord
}

func runGateWithCandidateMetrics(t *testing.T, mutate func(*Metrics)) CandidateGateReport {
	t.Helper()
	dir := t.TempDir()
	record := gateSemanticDecision("text-001", "general_explanation", 8, 9)
	baseline := mustWriteGateManifest(t, dir, "base", gateFixtureConfig{
		semanticJudgedSafe: 1,
		safeCostSaved:      1,
		cachedRate:         1,
		records:            []DecisionRecord{record},
	})
	candidate := mustWriteGateManifestWithMetrics(t, dir, "cand", gateFixtureConfig{
		semanticJudgedSafe: 1,
		safeCostSaved:      1,
		cachedRate:         1,
		records:            []DecisionRecord{record},
	}, mutate)
	report, _, err := BuildCandidateGate(context.Background(), CandidateGateInput{
		BaselineManifestPath:  baseline,
		CandidateManifestPath: candidate,
	})
	require.NoError(t, err)
	return report
}

func mustWriteGateManifest(t *testing.T, dir, prefix string, cfg gateFixtureConfig) string {
	t.Helper()
	return mustWriteGateManifestWithMetrics(t, dir, prefix, cfg, nil)
}

func mustWriteGateManifestWithMetrics(t *testing.T, dir, prefix string, cfg gateFixtureConfig, mutate func(*Metrics)) string {
	t.Helper()
	if cfg.vectorStore == "" {
		cfg.vectorStore = "sqlite"
	}
	if cfg.semanticRerank == "" {
		cfg.semanticRerank = "none"
	}
	if cfg.topKTrace == 0 {
		cfg.topKTrace = 5
	}
	total := len(cfg.records)
	metrics := Metrics{
		Threshold:               0.90,
		Total:                   total,
		BadHitRate:              0,
		SafeCostSaved:           cfg.safeCostSaved,
		LayerContribution:       map[string]int{"semantic": total, "miss": 0},
		RefusedByRule:           map[string]int{},
		JudgeMode:               "fake",
		UnknownCount:            cfg.unknownCount,
		JudgeErrorCount:         cfg.judgeErrorCount,
		CachedGEFreshMinus1Rate: cfg.cachedRate,
		VectorStore:             cfg.vectorStore,
		SemanticRerank:          cfg.semanticRerank,
		TopKTrace:               cfg.topKTrace,
		SemanticCandidates:      total,
		SemanticJudgedSafe:      cfg.semanticJudgedSafe,
		SamplingMode:            "stratified",
		StrataCount:             1,
		SampledByStratum:        map[string]int{"category=general_explanation|source_kind=safe_paraphrase|expected_cache_behavior=semantic_safe": total},
	}
	if mutate != nil {
		mutate(&metrics)
	}
	metricsPath := filepath.Join(dir, prefix+"_metrics.json")
	decisionPath := filepath.Join(dir, prefix+"_decisions.jsonl")
	manifestPath := filepath.Join(dir, prefix+"_manifest.json")
	payload, err := json.Marshal(metrics)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(metricsPath, payload, 0o644))
	require.NoError(t, WriteDecisionLog(decisionPath, cfg.records))
	require.NoError(t, WriteBaselineManifest(manifestPath, BaselineManifest{
		RunID:            prefix + "-run",
		Dataset:          "fixture.jsonl",
		SamplingMode:     "stratified",
		SampleCount:      total,
		StrataCount:      metrics.StrataCount,
		SampledByStratum: cloneIntMap(metrics.SampledByStratum),
		Threshold:        metrics.Threshold,
		VectorStore:      metrics.VectorStore,
		SemanticRerank:   metrics.SemanticRerank,
		TopKTrace:        metrics.TopKTrace,
		MetricsPath:      metricsPath,
		DecisionLogPath:  decisionPath,
		CreatedAt:        "2026-05-16T00:00:00Z",
	}))
	return manifestPath
}

func gateSemanticDecision(id, category string, cachedScore, freshScore float64) DecisionRecord {
	return DecisionRecord{
		SampleID:               id,
		Category:               category,
		ExpectedRisk:           "low",
		SourceKind:             "safe_paraphrase",
		ExpectedCacheBehavior:  "semantic_safe",
		ModelAlias:             "model-redacted",
		VectorStore:            "sqlite",
		EmbeddingMode:          "fake",
		Threshold:              0.90,
		CacheLayer:             "semantic",
		RequestPreview:         "request " + id,
		SemanticCandidateFound: true,
		Similarity:             0.91,
		CandidatePromptPreview: "candidate prompt " + id,
		CandidateAnswerPreview: "candidate answer " + id,
		SemanticTopK:           []cachepkg.SemanticTopKCandidate{{Rank: 1, Similarity: 0.91, CandidatePromptPreview: "candidate prompt " + id, CandidateAnswerPreview: "candidate answer " + id}},
		SafetyAllowed:          true,
		JudgeMode:              "fake",
		JudgeCacheSafe:         boolPtr(true),
		CachedScore:            cachedScore,
		FreshScore:             freshScore,
		JudgeDelta:             cachedScore - freshScore,
		EstimatedUpstreamCost:  1,
		SafeCostSaved:          1,
	}
}

func gatePassed(report CandidateGateReport, name string) bool {
	for _, check := range report.HardGates {
		if check.Name == name {
			return check.Passed
		}
	}
	return false
}

func warningActive(report CandidateGateReport, name string) bool {
	for _, warning := range report.Warnings {
		if warning.Name == name {
			return warning.Active
		}
	}
	return false
}
