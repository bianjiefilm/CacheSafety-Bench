package benchmark

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const candidateGateAvgSimilarityDeltaWarnThreshold = 0.001

type CandidateGateInput struct {
	BaselineManifestPath  string
	CandidateManifestPath string
}

type CandidateGateReport struct {
	Status                 string                 `json:"status"`
	Baseline               BaselineManifest       `json:"baseline"`
	Candidate              BaselineManifest       `json:"candidate"`
	BaselineSummary        CandidateGateSummary   `json:"baseline_summary"`
	CandidateSummary       CandidateGateSummary   `json:"candidate_summary"`
	HardGates              []CandidateGateCheck   `json:"hard_gates"`
	Warnings               []CandidateGateWarning `json:"warnings,omitempty"`
	KeySampleChecks        []CandidateKeySample   `json:"key_sample_checks,omitempty"`
	CandidateCanBeRetained bool                   `json:"candidate_can_be_retained"`
	DefaultEnableAllowed   bool                   `json:"default_enable_allowed"`
}

type CandidateGateSummary struct {
	SampleCount                 int     `json:"sample_count"`
	SamplingMode                string  `json:"sampling_mode,omitempty"`
	StrataCount                 int     `json:"strata_count,omitempty"`
	VectorStore                 string  `json:"vector_store,omitempty"`
	SemanticRerank              string  `json:"semantic_rerank,omitempty"`
	TopKTrace                   int     `json:"top_k_trace,omitempty"`
	Threshold                   float64 `json:"threshold,omitempty"`
	BadHitCount                 int     `json:"bad_hit_count"`
	UnknownCount                int     `json:"unknown_count"`
	JudgeErrorCount             int     `json:"judge_error_count"`
	SemanticJudgedSafe          int     `json:"semantic_judged_safe"`
	SafeCostSaved               float64 `json:"safe_cost_saved"`
	CachedGEFreshMinus1Rate     float64 `json:"cached_ge_fresh_minus_1_rate"`
	HardNegativeSemanticSafeHit int     `json:"hard_negative_semantic_safe_hit"`
	ConsistencyOK               bool    `json:"consistency_ok"`
}

type CandidateGateCheck struct {
	Name      string `json:"name"`
	Passed    bool   `json:"passed"`
	Baseline  string `json:"baseline,omitempty"`
	Candidate string `json:"candidate,omitempty"`
	Message   string `json:"message,omitempty"`
}

type CandidateGateWarning struct {
	Name    string `json:"name"`
	Active  bool   `json:"active"`
	Message string `json:"message,omitempty"`
}

type CandidateKeySample struct {
	SampleID                string  `json:"sample_id"`
	Found                   bool    `json:"found"`
	Category                string  `json:"category,omitempty"`
	CacheLayer              string  `json:"cache_layer,omitempty"`
	CandidatePromptPreview  string  `json:"candidate_prompt_preview,omitempty"`
	CandidateAnswerPreview  string  `json:"candidate_answer_preview,omitempty"`
	CachedScore             float64 `json:"cached_score,omitempty"`
	FreshScore              float64 `json:"fresh_score,omitempty"`
	BadHit                  bool    `json:"bad_hit"`
	JudgeCacheSafe          *bool   `json:"judge_cache_safe,omitempty"`
	JudgeReason             string  `json:"judge_reason,omitempty"`
	DiagnosticScoreDelta    float64 `json:"diagnostic_score_delta,omitempty"`
	DiagnosticAnswerChanged bool    `json:"diagnostic_answer_changed,omitempty"`
	DiagnosticPromptChanged bool    `json:"diagnostic_prompt_changed,omitempty"`
	DiagnosticSafeHitLost   bool    `json:"diagnostic_safe_hit_lost,omitempty"`
	DiagnosticSafeHitGained bool    `json:"diagnostic_safe_hit_gained,omitempty"`
}

func BuildCandidateGate(ctx context.Context, input CandidateGateInput) (CandidateGateReport, string, error) {
	baselineManifest, err := LoadBaselineManifest(input.BaselineManifestPath)
	if err != nil {
		return CandidateGateReport{}, "", err
	}
	candidateManifest, err := LoadBaselineManifest(input.CandidateManifestPath)
	if err != nil {
		return CandidateGateReport{}, "", err
	}
	baselineData, _, err := BuildBaselineReport(ctx, BaselineReportInput{
		MetricsPath:     baselineManifest.MetricsPath,
		DecisionLogPath: baselineManifest.DecisionLogPath,
		RequestLogDB:    baselineManifest.RequestLogDBPath,
		RunID:           baselineManifest.RunID,
	})
	if err != nil {
		return CandidateGateReport{}, "", err
	}
	candidateData, _, err := BuildBaselineReport(ctx, BaselineReportInput{
		MetricsPath:     candidateManifest.MetricsPath,
		DecisionLogPath: candidateManifest.DecisionLogPath,
		RequestLogDB:    candidateManifest.RequestLogDBPath,
		RunID:           candidateManifest.RunID,
	})
	if err != nil {
		return CandidateGateReport{}, "", err
	}
	baselineRecords, err := LoadDecisionLog(baselineManifest.DecisionLogPath)
	if err != nil {
		return CandidateGateReport{}, "", err
	}
	candidateRecords, err := LoadDecisionLog(candidateManifest.DecisionLogPath)
	if err != nil {
		return CandidateGateReport{}, "", err
	}
	diagnostics, _, err := BuildTop1Diagnostics(ctx, Top1DiagnosticsInput{
		FloatDecisionLogPath: baselineManifest.DecisionLogPath,
		Int8DecisionLogPath:  candidateManifest.DecisionLogPath,
		FloatRequestLogDB:    baselineManifest.RequestLogDBPath,
		Int8RequestLogDB:     candidateManifest.RequestLogDBPath,
		FocusSampleIDs:       []string{"text-002", "text-031", "text-025"},
	})
	if err != nil {
		return CandidateGateReport{}, "", err
	}

	strataAligned, missingStrata, newStrata := compareStrataCoverage(baselineManifest.SampledByStratum, candidateManifest.SampledByStratum)
	newActualBadHits := compareActualBadHits(baselineRecords, candidateRecords)
	report := CandidateGateReport{
		Status:               "PASS",
		Baseline:             baselineManifest,
		Candidate:            candidateManifest,
		BaselineSummary:      buildCandidateGateSummary(baselineManifest, baselineData, baselineRecords),
		CandidateSummary:     buildCandidateGateSummary(candidateManifest, candidateData, candidateRecords),
		DefaultEnableAllowed: false,
	}
	report.HardGates = []CandidateGateCheck{
		gateCheck("sample_count_aligned", baselineManifest.SampleCount == candidateManifest.SampleCount, fmt.Sprintf("%d", baselineManifest.SampleCount), fmt.Sprintf("%d", candidateManifest.SampleCount), "sample_count must match"),
		gateCheck("strata_coverage_aligned", strataAligned, fmt.Sprintf("%d strata", len(baselineManifest.SampledByStratum)), fmt.Sprintf("%d strata", len(candidateManifest.SampledByStratum)), strataCoverageMessage(missingStrata, newStrata)),
		gateCheck("new_bad_hit_zero", len(newActualBadHits) == 0, "0", fmt.Sprintf("%d", len(newActualBadHits)), "candidate must not introduce actual bad hits"),
		gateCheck("unknown_count_zero", candidateData.Metrics.UnknownCount == 0, "0", fmt.Sprintf("%d", candidateData.Metrics.UnknownCount), "unknown_count must be zero"),
		gateCheck("judge_error_count_zero", candidateData.Metrics.JudgeErrorCount == 0, "0", fmt.Sprintf("%d", candidateData.Metrics.JudgeErrorCount), "judge_error_count must be zero"),
		gateCheck("semantic_judged_safe_not_down", candidateData.Metrics.SemanticJudgedSafe >= baselineData.Metrics.SemanticJudgedSafe, fmt.Sprintf("%d", baselineData.Metrics.SemanticJudgedSafe), fmt.Sprintf("%d", candidateData.Metrics.SemanticJudgedSafe), "semantic_judged_safe must not decrease"),
		gateCheck("safe_cost_saved_not_down", candidateData.Metrics.SafeCostSaved+1e-9 >= baselineData.Metrics.SafeCostSaved, fmt.Sprintf("%.4f", baselineData.Metrics.SafeCostSaved), fmt.Sprintf("%.4f", candidateData.Metrics.SafeCostSaved), "safe_cost_saved must not decrease"),
		gateCheck("cached_ge_fresh_minus_1_rate_not_down", candidateData.Metrics.CachedGEFreshMinus1Rate+1e-9 >= baselineData.Metrics.CachedGEFreshMinus1Rate, fmt.Sprintf("%.4f", baselineData.Metrics.CachedGEFreshMinus1Rate), fmt.Sprintf("%.4f", candidateData.Metrics.CachedGEFreshMinus1Rate), "cached_ge_fresh_minus_1_rate must not decrease"),
		gateCheck("hard_negative_safe_hit_zero", semanticHardNegativeSafeHits(candidateRecords) == 0, "0", fmt.Sprintf("%d", semanticHardNegativeSafeHits(candidateRecords)), "hard_negative must not become semantic safe hits"),
	}
	report.Warnings = buildCandidateGateWarnings(candidateManifest, candidateRecords, diagnostics)
	report.KeySampleChecks = buildCandidateKeySamples(candidateRecords, diagnostics, []string{"text-002", "text-031", "text-025"})
	for _, check := range report.HardGates {
		if !check.Passed {
			report.Status = "FAIL"
			break
		}
	}
	report.CandidateCanBeRetained = report.Status == "PASS"
	return report, renderCandidateGateMarkdown(report), nil
}

func WriteCandidateGateReport(path string, markdown string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(markdown), 0o644)
}

func buildCandidateGateSummary(manifest BaselineManifest, data BaselineReportData, records []DecisionRecord) CandidateGateSummary {
	return CandidateGateSummary{
		SampleCount:                 manifest.SampleCount,
		SamplingMode:                manifest.SamplingMode,
		StrataCount:                 manifest.StrataCount,
		VectorStore:                 firstNonEmptyTop1(manifest.VectorStore, data.Metrics.VectorStore),
		SemanticRerank:              firstNonEmptyTop1(manifest.SemanticRerank, data.Metrics.SemanticRerank),
		TopKTrace:                   maxManifestInt(manifest.TopKTrace, data.Metrics.TopKTrace),
		Threshold:                   nonZeroFloat(manifest.Threshold, data.Metrics.Threshold),
		BadHitCount:                 data.DecisionSummary.BadHitCount,
		UnknownCount:                data.Metrics.UnknownCount,
		JudgeErrorCount:             data.Metrics.JudgeErrorCount,
		SemanticJudgedSafe:          data.Metrics.SemanticJudgedSafe,
		SafeCostSaved:               data.Metrics.SafeCostSaved,
		CachedGEFreshMinus1Rate:     data.Metrics.CachedGEFreshMinus1Rate,
		HardNegativeSemanticSafeHit: semanticHardNegativeSafeHits(records),
		ConsistencyOK:               data.Consistency.Matches,
	}
}

func gateCheck(name string, passed bool, baseline string, candidate string, message string) CandidateGateCheck {
	return CandidateGateCheck{
		Name:      name,
		Passed:    passed,
		Baseline:  baseline,
		Candidate: candidate,
		Message:   message,
	}
}

func strataCoverageMessage(missing []string, added []string) string {
	if len(missing) == 0 && len(added) == 0 {
		return "strata keys must match"
	}
	return fmt.Sprintf("missing=%d new=%d", len(missing), len(added))
}

func compareActualBadHits(baseline []DecisionRecord, candidate []DecisionRecord) []DecisionRecord {
	base := map[string]struct{}{}
	for _, record := range baseline {
		if record.BadHit {
			base[top1AlignmentKey(record)] = struct{}{}
		}
	}
	var out []DecisionRecord
	for _, record := range candidate {
		if !record.BadHit {
			continue
		}
		if _, ok := base[top1AlignmentKey(record)]; !ok {
			out = append(out, record)
		}
	}
	sortDecisionRecords(out)
	return out
}

func semanticHardNegativeSafeHits(records []DecisionRecord) int {
	var count int
	for _, record := range records {
		if record.Category != "hard_negative" || record.CacheLayer != "semantic" {
			continue
		}
		if record.JudgeCacheSafe != nil && *record.JudgeCacheSafe && !record.BadHit {
			count++
		}
	}
	return count
}

func buildCandidateGateWarnings(manifest BaselineManifest, records []DecisionRecord, diagnostics Top1DiagnosticsReport) []CandidateGateWarning {
	warnings := []CandidateGateWarning{
		{
			Name:    "answer_changed_count",
			Active:  diagnostics.AnswerChangedCount > 0,
			Message: fmt.Sprintf("answer_changed_count=%d", diagnostics.AnswerChangedCount),
		},
		{
			Name:    "score_worsened_count",
			Active:  diagnostics.ScoreWorsenedCount > 0,
			Message: fmt.Sprintf("score_worsened_count=%d", diagnostics.ScoreWorsenedCount),
		},
		{
			Name:    "avg_similarity_delta",
			Active:  math.Abs(diagnostics.SimilarityDeltaAvg) > candidateGateAvgSimilarityDeltaWarnThreshold,
			Message: fmt.Sprintf("avg_similarity_delta=%.6f threshold=%.6f", diagnostics.SimilarityDeltaAvg, candidateGateAvgSimilarityDeltaWarnThreshold),
		},
	}
	semanticCandidateCount, topKTraceCount := semanticTopKTraceCounts(records)
	topKTraceMissing := manifest.TopKTrace <= 0 || (semanticCandidateCount > 0 && topKTraceCount == 0)
	warnings = append(warnings, CandidateGateWarning{
		Name:    "topk_trace_missing",
		Active:  topKTraceMissing,
		Message: fmt.Sprintf("top_k_trace=%d semantic_candidates=%d topk_trace_records=%d", manifest.TopKTrace, semanticCandidateCount, topKTraceCount),
	})
	return warnings
}

func semanticTopKTraceCounts(records []DecisionRecord) (int, int) {
	var semanticCandidates int
	var withTrace int
	for _, record := range records {
		if !record.SemanticCandidateFound {
			continue
		}
		semanticCandidates++
		if len(record.SemanticTopK) > 0 {
			withTrace++
		}
	}
	return semanticCandidates, withTrace
}

func buildCandidateKeySamples(records []DecisionRecord, diagnostics Top1DiagnosticsReport, sampleIDs []string) []CandidateKeySample {
	recordsBySample := map[string][]DecisionRecord{}
	for _, record := range records {
		recordsBySample[record.SampleID] = append(recordsBySample[record.SampleID], record)
	}
	var out []CandidateKeySample
	for _, sampleID := range sampleIDs {
		check := CandidateKeySample{SampleID: sampleID}
		if matches := recordsBySample[sampleID]; len(matches) > 0 {
			sort.SliceStable(matches, func(i, j int) bool {
				left := candidateKeySamplePriority(matches[i])
				right := candidateKeySamplePriority(matches[j])
				if left == right {
					return matches[i].SafeCostSaved > matches[j].SafeCostSaved
				}
				return left < right
			})
			record := matches[0]
			check.Found = true
			check.Category = record.Category
			check.CacheLayer = record.CacheLayer
			check.CandidatePromptPreview = record.CandidatePromptPreview
			check.CandidateAnswerPreview = record.CandidateAnswerPreview
			check.CachedScore = record.CachedScore
			check.FreshScore = record.FreshScore
			check.BadHit = record.BadHit
			check.JudgeCacheSafe = record.JudgeCacheSafe
			check.JudgeReason = record.JudgeReason
		}
		if focusRecords := diagnostics.FocusExamples[sampleID]; len(focusRecords) > 0 {
			focus := focusRecords[0]
			check.DiagnosticScoreDelta = focus.ScoreDeltaChange
			check.DiagnosticAnswerChanged = focus.CandidateAnswerChanged
			check.DiagnosticPromptChanged = focus.CandidatePromptChanged
			check.DiagnosticSafeHitLost = focus.SafeHitLost
			check.DiagnosticSafeHitGained = focus.SafeHitGained
		}
		out = append(out, check)
	}
	return out
}

func candidateKeySamplePriority(record DecisionRecord) int {
	if record.CacheLayer == "semantic" {
		return 0
	}
	if record.CacheLayer == "exact" || record.CacheLayer == "canonical" {
		return 1
	}
	if record.SemanticCandidateFound {
		return 2
	}
	return 3
}

func renderCandidateGateMarkdown(report CandidateGateReport) string {
	var b strings.Builder
	b.WriteString("# Candidate Regression Gate\n\n")
	b.WriteString(fmt.Sprintf("## Status\n- result: `%s`\n", report.Status))
	b.WriteString(fmt.Sprintf("- candidate can be retained: `%t`\n", report.CandidateCanBeRetained))
	b.WriteString(fmt.Sprintf("- default enable allowed: `%t`\n", report.DefaultEnableAllowed))

	b.WriteString("\n## Baseline Summary\n")
	writeCandidateGateSummary(&b, report.BaselineSummary)

	b.WriteString("\n## Candidate Summary\n")
	writeCandidateGateSummary(&b, report.CandidateSummary)

	b.WriteString("\n## Hard Gates\n")
	for _, check := range report.HardGates {
		b.WriteString(fmt.Sprintf("- `%s`: passed=`%t` baseline=`%s` candidate=`%s` message=`%s`\n", check.Name, check.Passed, check.Baseline, check.Candidate, check.Message))
	}

	b.WriteString("\n## Warnings\n")
	if len(report.Warnings) == 0 {
		b.WriteString("- none\n")
	} else {
		for _, warning := range report.Warnings {
			b.WriteString(fmt.Sprintf("- `%s`: active=`%t` %s\n", warning.Name, warning.Active, warning.Message))
		}
	}

	b.WriteString("\n## Key Sample Checks\n")
	for _, sample := range report.KeySampleChecks {
		b.WriteString(fmt.Sprintf("- `%s`: found=`%t` layer=`%s` category=`%s` cached=`%.1f` fresh=`%.1f` bad_hit=`%t` score_delta_change=`%.1f` answer_changed=`%t` prompt_changed=`%t` safe_hit_lost=`%t`\n",
			sample.SampleID, sample.Found, sample.CacheLayer, sample.Category, sample.CachedScore, sample.FreshScore, sample.BadHit, sample.DiagnosticScoreDelta, sample.DiagnosticAnswerChanged, sample.DiagnosticPromptChanged, sample.DiagnosticSafeHitLost))
		if sample.CandidatePromptPreview != "" {
			b.WriteString(fmt.Sprintf("  - candidate prompt: %s\n", sample.CandidatePromptPreview))
		}
		if sample.CandidateAnswerPreview != "" {
			b.WriteString(fmt.Sprintf("  - candidate answer preview: %s\n", sample.CandidateAnswerPreview))
		}
	}

	b.WriteString("\n## Decision\n")
	b.WriteString("- allow candidate to remain: ")
	if report.CandidateCanBeRetained {
		b.WriteString("YES\n")
	} else {
		b.WriteString("NO\n")
	}
	b.WriteString("- allow default enable: NO\n")
	return b.String()
}

func writeCandidateGateSummary(b *strings.Builder, summary CandidateGateSummary) {
	b.WriteString(fmt.Sprintf("- sample_count: `%d`\n", summary.SampleCount))
	b.WriteString(fmt.Sprintf("- sampling_mode: `%s`\n", summary.SamplingMode))
	b.WriteString(fmt.Sprintf("- strata_count: `%d`\n", summary.StrataCount))
	b.WriteString(fmt.Sprintf("- vector_store: `%s`\n", summary.VectorStore))
	b.WriteString(fmt.Sprintf("- semantic_rerank: `%s`\n", summary.SemanticRerank))
	b.WriteString(fmt.Sprintf("- top_k_trace: `%d`\n", summary.TopKTrace))
	b.WriteString(fmt.Sprintf("- threshold: `%.2f`\n", summary.Threshold))
	b.WriteString(fmt.Sprintf("- bad_hit_count: `%d`\n", summary.BadHitCount))
	b.WriteString(fmt.Sprintf("- unknown_count: `%d`\n", summary.UnknownCount))
	b.WriteString(fmt.Sprintf("- judge_error_count: `%d`\n", summary.JudgeErrorCount))
	b.WriteString(fmt.Sprintf("- semantic_judged_safe: `%d`\n", summary.SemanticJudgedSafe))
	b.WriteString(fmt.Sprintf("- safe_cost_saved: `%.4f`\n", summary.SafeCostSaved))
	b.WriteString(fmt.Sprintf("- cached_ge_fresh_minus_1_rate: `%.4f`\n", summary.CachedGEFreshMinus1Rate))
	b.WriteString(fmt.Sprintf("- hard_negative_semantic_safe_hit: `%d`\n", summary.HardNegativeSemanticSafeHit))
	b.WriteString(fmt.Sprintf("- consistency_ok: `%t`\n", summary.ConsistencyOK))
}

func nonZeroFloat(primary float64, fallback float64) float64 {
	if primary != 0 {
		return primary
	}
	return fallback
}
