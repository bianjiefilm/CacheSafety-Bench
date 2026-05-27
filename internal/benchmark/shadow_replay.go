package benchmark

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type ShadowReplayInput struct {
	DecisionLogPath string
	RequestLogDB    string
	RequestLogDBs   []string
	ProgressLog     string
	ProgressLogs    []string
	RunID           string
}

type ShadowReplayReport struct {
	SourceDecisionLog        string                    `json:"source_decision_log,omitempty"`
	DecisionLogTotal         int                       `json:"decision_log_total"`
	RequestLogPresent        bool                      `json:"request_log_present"`
	RequestLogRunID          string                    `json:"request_log_run_id,omitempty"`
	RequestLogTotal          int                       `json:"request_log_total,omitempty"`
	RequestLogConsistent     bool                      `json:"request_log_consistent"`
	WouldCacheHitCount       int                       `json:"would_cache_hit_count"`
	WouldCacheHitRate        float64                   `json:"would_cache_hit_rate"`
	RefusedCount             int                       `json:"refused_count"`
	SemanticCandidates       int                       `json:"semantic_candidates"`
	SemanticJudgedSafe       int                       `json:"semantic_judged_safe"`
	BadHitCount              int                       `json:"bad_hit_count"`
	ExpectedRefusalLeakCount int                       `json:"expected_refusal_leak_count"`
	HardTrapSafeHitCount     int                       `json:"hard_trap_safe_hit_count"`
	HardNegativeSafeHitCount int                       `json:"hard_negative_safe_hit_count"`
	JudgeErrorCount          int                       `json:"judge_error_count"`
	UnknownCount             int                       `json:"unknown_count"`
	SafeCostSaved            float64                   `json:"safe_cost_saved"`
	CachedTokensSaved        int                       `json:"cached_tokens_saved"`
	NetCostSavedUSD          float64                   `json:"net_cost_saved_usd"`
	ByCacheLayer             map[string]int            `json:"by_cache_layer"`
	ByCategory               map[string]int            `json:"by_category"`
	BySourceKind             map[string]int            `json:"by_source_kind,omitempty"`
	ByExpectedCacheBehavior  map[string]int            `json:"by_expected_cache_behavior,omitempty"`
	ByRefusedReason          map[string]int            `json:"by_refused_reason,omitempty"`
	CategoryMetrics          map[string]CategoryMetric `json:"category_metrics,omitempty"`
	GateChecks               []ShadowReplayCheck       `json:"gate_checks"`
	ProgressSummary          *ProgressSummary          `json:"progress_summary,omitempty"`
	OfflineShadowReplayClean bool                      `json:"offline_shadow_replay_clean"`
	Warnings                 []ShadowReplayWarning     `json:"warnings,omitempty"`
}

type ShadowReplayCheck struct {
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Message string `json:"message,omitempty"`
}

type ShadowReplayWarning struct {
	Kind    string `json:"kind"`
	Message string `json:"message,omitempty"`
}

func BuildShadowReplayReport(ctx context.Context, input ShadowReplayInput) (ShadowReplayReport, string, error) {
	if strings.TrimSpace(input.DecisionLogPath) == "" {
		return ShadowReplayReport{}, "", fmt.Errorf("decision log path is required")
	}
	records, err := LoadDecisionLog(input.DecisionLogPath)
	if err != nil {
		return ShadowReplayReport{}, "", err
	}
	report := BuildShadowReplayReportFromRecords(records)
	report.SourceDecisionLog = input.DecisionLogPath
	report.DecisionLogTotal = len(records)
	report.RequestLogConsistent = true

	requestLogPaths := nonEmptyStrings(append([]string{input.RequestLogDB}, input.RequestLogDBs...))
	if len(requestLogPaths) > 0 {
		report.RequestLogConsistent = true
		summariesLoaded := 0
		for _, requestLogPath := range requestLogPaths {
			if _, statErr := os.Stat(requestLogPath); statErr != nil {
				report.RequestLogConsistent = false
				report.Warnings = append(report.Warnings, ShadowReplayWarning{Kind: "request_log_missing", Message: filepath.Base(requestLogPath)})
				continue
			}
			store, openErr := OpenRequestLogStore(ctx, requestLogPath)
			if openErr != nil {
				report.RequestLogConsistent = false
				report.Warnings = append(report.Warnings, ShadowReplayWarning{Kind: "request_log_open_error", Message: SanitizeErrorSummary(openErr)})
				continue
			}
			runID := strings.TrimSpace(input.RunID)
			if runID == "" {
				runs, listErr := store.ListRuns(ctx)
				if listErr != nil {
					report.RequestLogConsistent = false
					report.Warnings = append(report.Warnings, ShadowReplayWarning{Kind: "request_log_list_error", Message: SanitizeErrorSummary(listErr)})
				} else if len(runs) == 1 {
					runID = runs[0].RunID
				} else {
					report.RequestLogConsistent = false
					report.Warnings = append(report.Warnings, ShadowReplayWarning{Kind: "request_log_run_id_required", Message: fmt.Sprintf("%s runs=%d", filepath.Base(requestLogPath), len(runs))})
				}
			}
			if runID != "" {
				summary, sumErr := store.Summary(ctx, runID)
				if sumErr != nil {
					report.RequestLogConsistent = false
					report.Warnings = append(report.Warnings, ShadowReplayWarning{Kind: "request_log_summary_error", Message: SanitizeErrorSummary(sumErr)})
				} else {
					summariesLoaded++
					report.RequestLogPresent = true
					if report.RequestLogRunID == "" {
						report.RequestLogRunID = runID
					} else if report.RequestLogRunID != runID {
						report.RequestLogRunID = "multiple"
					}
					report.RequestLogTotal += summary.Total
				}
			}
			_ = store.Close()
		}
		if summariesLoaded == 0 {
			report.RequestLogConsistent = false
		} else if report.RequestLogTotal != len(records) {
			report.RequestLogConsistent = false
			report.Warnings = append(report.Warnings, ShadowReplayWarning{Kind: "request_log_total_mismatch", Message: fmt.Sprintf("decision_log=%d request_log=%d", len(records), report.RequestLogTotal)})
		}
	}
	progressPaths := nonEmptyStrings(append([]string{input.ProgressLog}, input.ProgressLogs...))
	if len(progressPaths) > 0 {
		progressSummary, progressErr := BuildProgressSummary(progressPaths)
		if progressErr != nil {
			report.Warnings = append(report.Warnings, ShadowReplayWarning{Kind: "progress_log_error", Message: SanitizeErrorSummary(progressErr)})
		} else {
			report.ProgressSummary = &progressSummary
		}
	}
	return report, RenderShadowReplayMarkdown(report), nil
}

func BuildShadowReplayReportFromRecords(records []DecisionRecord) ShadowReplayReport {
	summary := AnalyzeDecisionRecords(records)
	report := ShadowReplayReport{
		DecisionLogTotal:         len(records),
		RequestLogConsistent:     true,
		SemanticCandidates:       summary.SemanticCandidates,
		BadHitCount:              summary.BadHitCount,
		ExpectedRefusalLeakCount: countExpectedRefusalLeaks(records),
		HardTrapSafeHitCount:     countHardTrapSafeHits(records),
		HardNegativeSafeHitCount: semanticHardNegativeSafeHits(records),
		SafeCostSaved:            summary.SafeCostSavedTotal,
		ByCacheLayer:             summary.ByCacheLayer,
		ByCategory:               summary.ByCategory,
		BySourceKind:             summary.BySourceKind,
		ByExpectedCacheBehavior:  summary.ByExpectedCacheBehavior,
		ByRefusedReason:          summary.ByRefusedReason,
		CategoryMetrics:          shadowCategoryMetrics(records),
	}
	for _, record := range records {
		if isCacheHitLayer(record.CacheLayer) {
			report.WouldCacheHitCount++
		}
		if record.CacheLayer == "semantic" && record.JudgeCacheSafe != nil && *record.JudgeCacheSafe && !record.BadHit {
			report.SemanticJudgedSafe++
		}
		if record.RefusedReason != "" {
			report.RefusedCount++
		}
		if record.JudgeError != "" {
			report.JudgeErrorCount++
			report.UnknownCount++
		}
		report.CachedTokensSaved += record.CachedPromptTokensSaved + record.CachedCompletionTokensSaved
		report.NetCostSavedUSD += record.NetCostSavedUSD
	}
	if len(records) > 0 {
		report.WouldCacheHitRate = float64(report.WouldCacheHitCount) / float64(len(records))
	}
	report.GateChecks = []ShadowReplayCheck{
		shadowReplayCheck("bad_hit_zero", report.BadHitCount == 0, "bad_hit_count must be zero"),
		shadowReplayCheck("expected_refusal_leak_zero", report.ExpectedRefusalLeakCount == 0, "expected_refusal samples must not be semantic safe hits"),
		shadowReplayCheck("hard_trap_safe_hit_zero", report.HardTrapSafeHitCount == 0, "hard_trap samples must not be semantic safe hits"),
		shadowReplayCheck("hard_negative_safe_hit_zero", report.HardNegativeSafeHitCount == 0, "hard_negative samples must not be semantic safe hits"),
		shadowReplayCheck("judge_error_zero", report.JudgeErrorCount == 0, "judge_error_count must be zero"),
		shadowReplayCheck("unknown_zero", report.UnknownCount == 0, "unknown_count must be zero"),
	}
	report.OfflineShadowReplayClean = true
	for _, check := range report.GateChecks {
		if !check.Passed {
			report.OfflineShadowReplayClean = false
			break
		}
	}
	return report
}

func RenderShadowReplayMarkdown(report ShadowReplayReport) string {
	var b strings.Builder
	b.WriteString("# Shadow Replay Adapter v0 Report\n\n")
	b.WriteString("## Scope\n")
	b.WriteString("- mode: `offline_decision_log_replay`\n")
	b.WriteString("- production traffic: `false`\n")
	b.WriteString("- cache strategy changed: `false`\n")
	b.WriteString("- provider calls made by this report: `false`\n")

	b.WriteString("\n## Summary\n")
	b.WriteString(fmt.Sprintf("- total: `%d`\n", report.DecisionLogTotal))
	b.WriteString(fmt.Sprintf("- would_cache_hit_count: `%d`\n", report.WouldCacheHitCount))
	b.WriteString(fmt.Sprintf("- would_cache_hit_rate: `%.4f`\n", report.WouldCacheHitRate))
	b.WriteString(fmt.Sprintf("- refused_count: `%d`\n", report.RefusedCount))
	b.WriteString(fmt.Sprintf("- semantic_candidates: `%d`\n", report.SemanticCandidates))
	b.WriteString(fmt.Sprintf("- semantic_judged_safe: `%d`\n", report.SemanticJudgedSafe))
	b.WriteString(fmt.Sprintf("- bad_hit_count: `%d`\n", report.BadHitCount))
	b.WriteString(fmt.Sprintf("- expected_refusal_leak_count: `%d`\n", report.ExpectedRefusalLeakCount))
	b.WriteString(fmt.Sprintf("- hard_trap_safe_hit_count: `%d`\n", report.HardTrapSafeHitCount))
	b.WriteString(fmt.Sprintf("- hard_negative_safe_hit_count: `%d`\n", report.HardNegativeSafeHitCount))
	b.WriteString(fmt.Sprintf("- judge_error_count: `%d`\n", report.JudgeErrorCount))
	b.WriteString(fmt.Sprintf("- unknown_count: `%d`\n", report.UnknownCount))
	b.WriteString(fmt.Sprintf("- safe_cost_saved: `%.4f`\n", report.SafeCostSaved))
	b.WriteString(fmt.Sprintf("- cached_tokens_saved: `%d`\n", report.CachedTokensSaved))
	b.WriteString(fmt.Sprintf("- net_cost_saved_usd: `%.8f`\n", report.NetCostSavedUSD))
	b.WriteString(fmt.Sprintf("- offline_shadow_replay_clean: `%t`\n", report.OfflineShadowReplayClean))

	b.WriteString("\n## Cache Outcomes\n")
	b.WriteString(fmt.Sprintf("- canonical: `%d`\n", report.ByCacheLayer["canonical"]))
	b.WriteString(fmt.Sprintf("- exact: `%d`\n", report.ByCacheLayer["exact"]))
	b.WriteString(fmt.Sprintf("- semantic: `%d`\n", report.ByCacheLayer["semantic"]))
	b.WriteString(fmt.Sprintf("- miss: `%d`\n", report.ByCacheLayer["miss"]))

	b.WriteString("\n## Refusals\n")
	if len(report.ByRefusedReason) == 0 {
		b.WriteString("- none\n")
	} else {
		writeIntMapMarkdown(&b, report.ByRefusedReason)
	}

	b.WriteString("\n## Expected Behavior\n")
	writeIntMapMarkdown(&b, report.ByExpectedCacheBehavior)

	b.WriteString("\n## Category Coverage\n")
	for _, category := range sortedIntKeys(report.ByCategory) {
		metric := report.CategoryMetrics[category]
		b.WriteString(fmt.Sprintf("- `%s`: total=`%d` semantic_candidates=`%d` semantic_judged_safe=`%d` refused=`%d` bad_hit_rate=`%.4f`\n",
			category, report.ByCategory[category], metric.SemanticCandidates, metric.SemanticJudgedSafe, metric.SemanticRejectedBySafety, metric.BadHitRate))
	}

	b.WriteString("\n## Request Log Consistency\n")
	b.WriteString(fmt.Sprintf("- request_log present: `%t`\n", report.RequestLogPresent))
	b.WriteString(fmt.Sprintf("- request_log total: `%d`\n", report.RequestLogTotal))
	b.WriteString(fmt.Sprintf("- decision_log total: `%d`\n", report.DecisionLogTotal))
	b.WriteString(fmt.Sprintf("- consistent: `%t`\n", report.RequestLogConsistent))

	b.WriteString("\n## Latency\n")
	if report.ProgressSummary == nil {
		b.WriteString("- progress_log summary: unavailable\n")
	} else {
		writeProgressSummaryMarkdown(&b, *report.ProgressSummary)
	}

	b.WriteString("\n## Gate Checks\n")
	for _, check := range report.GateChecks {
		b.WriteString(fmt.Sprintf("- `%s`: `%t`", check.Name, check.Passed))
		if check.Message != "" {
			b.WriteString(fmt.Sprintf(" - %s", check.Message))
		}
		b.WriteString("\n")
	}

	if len(report.Warnings) > 0 {
		b.WriteString("\n## Warnings\n")
		for _, warning := range report.Warnings {
			b.WriteString(fmt.Sprintf("- `%s`: %s\n", warning.Kind, warning.Message))
		}
	}

	b.WriteString("\n## Next Step\n")
	b.WriteString("- Use this as a local shadow replay adapter baseline. Do not enter production shadow traffic until full Golden Eval and request-log consistency are clean.\n")
	return b.String()
}

func writeProgressSummaryMarkdown(b *strings.Builder, summary ProgressSummary) {
	b.WriteString("- avg latency by stage:\n")
	if len(summary.AvgLatencyByStage) == 0 {
		b.WriteString("  - none\n")
	} else {
		for _, stage := range sortedFloatKeys(summary.AvgLatencyByStage) {
			b.WriteString(fmt.Sprintf("  - `%s`: `%.2fms`\n", stage, summary.AvgLatencyByStage[stage]))
		}
	}
	b.WriteString(fmt.Sprintf("- timeout count: `%d`\n", summary.TimeoutCount))
	b.WriteString(fmt.Sprintf("- error count: `%d`\n", summary.ErrorCount))
	b.WriteString("- slowest samples:\n")
	if len(summary.SlowestSamples) == 0 {
		b.WriteString("  - none\n")
	} else {
		for _, sample := range summary.SlowestSamples {
			b.WriteString(fmt.Sprintf("  - `%s`: stage=`%s` category=`%s` elapsed_ms=`%d`\n", sample.SampleID, sample.Stage, sample.Category, sample.ElapsedMS))
		}
	}
	b.WriteString("- timeout/error samples:\n")
	if len(summary.TimeoutErrors) == 0 {
		b.WriteString("  - none\n")
	} else {
		for _, sample := range summary.TimeoutErrors {
			b.WriteString(fmt.Sprintf("  - `%s`: stage=`%s` category=`%s` elapsed_ms=`%d` error=`%s`\n", sample.SampleID, sample.Stage, sample.Category, sample.ElapsedMS, sample.ErrorSummary))
		}
	}
}

func sortedFloatKeys(values map[string]float64) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func WriteShadowReplayOutputs(jsonPath string, reportPath string, report ShadowReplayReport, markdown string) error {
	if strings.TrimSpace(jsonPath) != "" {
		if err := writeJSONFile(jsonPath, report); err != nil {
			return err
		}
	}
	if strings.TrimSpace(reportPath) != "" {
		if err := os.MkdirAll(filepath.Dir(reportPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(reportPath, []byte(markdown), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func shadowReplayCheck(name string, passed bool, message string) ShadowReplayCheck {
	return ShadowReplayCheck{Name: name, Passed: passed, Message: message}
}

func isCacheHitLayer(layer string) bool {
	switch layer {
	case "exact", "canonical", "semantic":
		return true
	default:
		return false
	}
}

func shadowCategoryMetrics(records []DecisionRecord) map[string]CategoryMetric {
	out := map[string]CategoryMetric{}
	for _, record := range records {
		category := record.Category
		metric := out[category]
		metric.Total++
		if record.SemanticCandidateFound {
			metric.SemanticCandidates++
		}
		if record.RefusedReason != "" {
			metric.SemanticRejectedBySafety++
		}
		if record.CacheLayer == "semantic" && record.JudgeCacheSafe != nil && *record.JudgeCacheSafe && !record.BadHit {
			metric.SemanticJudgedSafe++
		}
		if record.BadHit {
			metric.badHits++
		}
		metric.SafeCostSaved += record.SafeCostSaved
		metric.CachedTokensSaved += record.CachedPromptTokensSaved + record.CachedCompletionTokensSaved
		metric.CachedCostUSDSaved += record.CachedCostUSDSaved
		metric.NetCostSavedUSD += record.NetCostSavedUSD
		out[category] = metric
	}
	for category, metric := range out {
		if metric.Total > 0 {
			metric.BadHitRate = float64(metric.badHits) / float64(metric.Total)
		}
		out[category] = metric
	}
	return out
}

func writeIntMapMarkdown(b *strings.Builder, values map[string]int) {
	if len(values) == 0 {
		b.WriteString("- none\n")
		return
	}
	keys := sortedIntKeys(values)
	for _, key := range keys {
		b.WriteString(fmt.Sprintf("- `%s`: `%d`\n", key, values[key]))
	}
}
