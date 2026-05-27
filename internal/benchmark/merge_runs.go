package benchmark

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type MergeBenchmarkRunsInput struct {
	MetricsPaths       []string
	DecisionLogPaths   []string
	RequestLogDBPaths  []string
	ProgressLogPaths   []string
	OutputMetricsPath  string
	OutputDecisionPath string
	OutputReportPath   string
}

type MergeBenchmarkRunsResult struct {
	Metrics                  Metrics          `json:"metrics"`
	DecisionSummary          DecisionSummary  `json:"decision_summary"`
	HardTrapSafeHitCount     int              `json:"hard_trap_safe_hit_count"`
	HardNegativeSafeHitCount int              `json:"hard_negative_safe_hit_count"`
	ExpectedRefusalLeaks     int              `json:"expected_refusal_leak_count"`
	RequestLogDBPathCount    int              `json:"request_log_db_path_count"`
	RequestLogTotal          int              `json:"request_log_total,omitempty"`
	RequestLogConsistent     bool             `json:"request_log_consistent"`
	RequestLogWarnings       []string         `json:"request_log_warnings,omitempty"`
	ProgressSummary          *ProgressSummary `json:"progress_summary,omitempty"`
}

func MergeBenchmarkRuns(input MergeBenchmarkRunsInput) (MergeBenchmarkRunsResult, error) {
	metrics := make([]Metrics, 0, len(input.MetricsPaths))
	for _, path := range input.MetricsPaths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		metric, err := loadMetricsFile(path)
		if err != nil {
			return MergeBenchmarkRunsResult{}, err
		}
		metrics = append(metrics, metric)
	}
	mergedMetrics := MergeMetrics(metrics)
	records := make([]DecisionRecord, 0)
	for _, path := range input.DecisionLogPaths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		chunkRecords, err := LoadDecisionLog(path)
		if err != nil {
			return MergeBenchmarkRunsResult{}, err
		}
		records = append(records, chunkRecords...)
	}
	summary := AnalyzeDecisionRecords(records)
	mergedMetrics.ExactSavedTokens = savedTokensByLayer(records, "exact")
	mergedMetrics.SemanticSavedTokens = savedTokensByLayer(records, "semantic")
	result := MergeBenchmarkRunsResult{
		Metrics:                  mergedMetrics,
		DecisionSummary:          summary,
		HardTrapSafeHitCount:     countHardTrapSafeHits(records),
		HardNegativeSafeHitCount: semanticHardNegativeSafeHits(records),
		ExpectedRefusalLeaks:     countExpectedRefusalLeaks(records),
		RequestLogDBPathCount:    len(nonEmptyStrings(input.RequestLogDBPaths)),
		RequestLogConsistent:     true,
	}
	requestLogTotal, requestLogWarnings := summarizeRequestLogDBs(context.Background(), input.RequestLogDBPaths)
	result.RequestLogTotal = requestLogTotal
	result.RequestLogWarnings = requestLogWarnings
	if len(nonEmptyStrings(input.RequestLogDBPaths)) > 0 && requestLogTotal != len(records) {
		result.RequestLogConsistent = false
	} else if len(requestLogWarnings) > 0 {
		result.RequestLogConsistent = false
	}
	progressPaths := nonEmptyStrings(input.ProgressLogPaths)
	if len(progressPaths) > 0 {
		progressSummary, progressErr := BuildProgressSummary(progressPaths)
		if progressErr != nil {
			result.ProgressSummary = &ProgressSummary{Warnings: []string{SanitizeErrorSummary(progressErr)}}
		} else {
			result.ProgressSummary = &progressSummary
		}
	}
	if strings.TrimSpace(input.OutputMetricsPath) != "" {
		if err := writeJSONFile(input.OutputMetricsPath, mergedMetrics); err != nil {
			return result, err
		}
	}
	if strings.TrimSpace(input.OutputDecisionPath) != "" {
		if err := WriteDecisionLog(input.OutputDecisionPath, records); err != nil {
			return result, err
		}
	}
	if strings.TrimSpace(input.OutputReportPath) != "" {
		if err := os.MkdirAll(filepath.Dir(input.OutputReportPath), 0o755); err != nil {
			return result, err
		}
		if err := os.WriteFile(input.OutputReportPath, []byte(RenderMergedBenchmarkReport(result)), 0o644); err != nil {
			return result, err
		}
	}
	return result, nil
}

func MergeMetrics(metrics []Metrics) Metrics {
	out := Metrics{
		LayerContribution: map[string]int{
			"exact":     0,
			"canonical": 0,
			"semantic":  0,
			"miss":      0,
		},
		RefusedByRule:   map[string]int{},
		RefusedByDetail: map[string]int{},
		CategoryMetrics: map[string]CategoryMetric{},
	}
	var safeHits, badHits, judgedGEFreshMinus1 int
	var blendedCostTotal, semanticSimilarityTotal float64
	for _, metric := range metrics {
		out.Total += metric.Total
		out.SafeCostSaved += metric.SafeCostSaved
		out.JudgedCount += metric.JudgedCount
		out.JudgeErrorCount += metric.JudgeErrorCount
		out.UnknownCount += metric.UnknownCount
		out.SemanticCandidates += metric.SemanticCandidates
		out.SemanticRejectedBySafety += metric.SemanticRejectedBySafety
		out.SemanticJudgedSafe += metric.SemanticJudgedSafe
		out.BadHitExamplesCount += metric.BadHitExamplesCount
		out.TotalPromptTokens += metric.TotalPromptTokens
		out.TotalCompletionTokens += metric.TotalCompletionTokens
		out.TotalTokens += metric.TotalTokens
		out.CachedTokensSaved += metric.CachedTokensSaved
		out.CachedPromptTokensSaved += metric.CachedPromptTokensSaved
		out.CachedCompletionTokensSaved += metric.CachedCompletionTokensSaved
		out.GrossCostUSD += metric.GrossCostUSD
		out.CachedCostUSDSaved += metric.CachedCostUSDSaved
		out.JudgeCostUSD += metric.JudgeCostUSD
		out.EmbeddingCostUSD += metric.EmbeddingCostUSD
		out.NetCostSavedUSD += metric.NetCostSavedUSD
		safeHits += int(math.Round(metric.SafeHitRate * float64(metric.Total)))
		badHits += int(math.Round(metric.BadHitRate * float64(metric.Total)))
		judgedGEFreshMinus1 += int(math.Round(metric.CachedGEFreshMinus1Rate * float64(metric.JudgedCount)))
		blendedCostTotal += metric.BlendedCostPerRequest * float64(metric.Total)
		semanticSimilarityTotal += metric.AvgSemanticSimilarity * float64(metric.SemanticCandidates)
		mergeStringInt(out.LayerContribution, metric.LayerContribution)
		mergeStringInt(out.RefusedByRule, metric.RefusedByRule)
		mergeStringInt(out.RefusedByDetail, metric.RefusedByDetail)
		mergeCategoryMetrics(out.CategoryMetrics, metric.CategoryMetrics)
		if out.JudgeMode == "" {
			out.JudgeMode = metric.JudgeMode
		}
		if out.EmbeddingMode == "" {
			out.EmbeddingMode = metric.EmbeddingMode
		}
		if out.VectorStore == "" {
			out.VectorStore = metric.VectorStore
		}
		if out.VectorDim == 0 {
			out.VectorDim = metric.VectorDim
		}
		if out.SemanticRerank == "" {
			out.SemanticRerank = metric.SemanticRerank
		}
		if out.TopKTrace == 0 {
			out.TopKTrace = metric.TopKTrace
		}
		if out.Threshold == 0 {
			out.Threshold = metric.Threshold
		}
		if out.TokenEstimationMode == "" {
			out.TokenEstimationMode = metric.TokenEstimationMode
		} else if metric.TokenEstimationMode != "" && out.TokenEstimationMode != metric.TokenEstimationMode {
			out.TokenEstimationMode = "mixed"
		}
	}
	if out.Total > 0 {
		hits := out.Total - out.LayerContribution["miss"]
		out.HitRate = float64(hits) / float64(out.Total)
		out.SafeHitRate = float64(safeHits) / float64(out.Total)
		out.BadHitRate = float64(badHits) / float64(out.Total)
		out.BlendedCostPerRequest = blendedCostTotal / float64(out.Total)
	}
	if out.JudgedCount > 0 {
		out.CachedGEFreshMinus1Rate = float64(judgedGEFreshMinus1) / float64(out.JudgedCount)
	}
	if out.SemanticCandidates > 0 {
		out.AvgSemanticSimilarity = semanticSimilarityTotal / float64(out.SemanticCandidates)
	}
	finalizeCostRates(&out)
	for category, metric := range out.CategoryMetrics {
		if metric.Total > 0 {
			metric.BadHitRate = float64(metric.badHits) / float64(metric.Total)
		}
		out.CategoryMetrics[category] = metric
	}
	if len(out.RefusedByDetail) == 0 {
		out.RefusedByDetail = nil
	}
	return out
}

func RenderMergedBenchmarkReport(result MergeBenchmarkRunsResult) string {
	metrics := result.Metrics
	var b strings.Builder
	b.WriteString("# Merged Benchmark Report\n\n")
	b.WriteString("## Summary\n")
	b.WriteString(fmt.Sprintf("- total: `%d`\n", metrics.Total))
	b.WriteString(fmt.Sprintf("- exact: `%d`\n", metrics.LayerContribution["exact"]))
	b.WriteString(fmt.Sprintf("- semantic: `%d`\n", metrics.LayerContribution["semantic"]))
	b.WriteString(fmt.Sprintf("- miss: `%d`\n", metrics.LayerContribution["miss"]))
	b.WriteString(fmt.Sprintf("- bad_hit_count: `%d`\n", result.DecisionSummary.BadHitCount))
	b.WriteString(fmt.Sprintf("- bad_hit_rate: `%.4f`\n", metrics.BadHitRate))
	b.WriteString(fmt.Sprintf("- hard_trap_safe_hit_count: `%d`\n", result.HardTrapSafeHitCount))
	b.WriteString(fmt.Sprintf("- hard_negative_safe_hit_count: `%d`\n", result.HardNegativeSafeHitCount))
	b.WriteString(fmt.Sprintf("- expected_refusal_leak_count: `%d`\n", result.ExpectedRefusalLeaks))
	b.WriteString(fmt.Sprintf("- judge_error_count: `%d`\n", metrics.JudgeErrorCount))
	b.WriteString(fmt.Sprintf("- unknown_count: `%d`\n", metrics.UnknownCount))
	b.WriteString(fmt.Sprintf("- semantic_judged_safe: `%d`\n", metrics.SemanticJudgedSafe))
	b.WriteString(fmt.Sprintf("- safe_cost_saved: `%.4f`\n", metrics.SafeCostSaved))
	b.WriteString(fmt.Sprintf("- cached_tokens_saved: `%d`\n", metrics.CachedTokensSaved))
	b.WriteString(fmt.Sprintf("- exact_saved_tokens: `%d`\n", metrics.ExactSavedTokens))
	b.WriteString(fmt.Sprintf("- semantic_saved_tokens: `%d`\n", metrics.SemanticSavedTokens))
	b.WriteString(fmt.Sprintf("- gross_saving_rate: `%.4f`\n", metrics.GrossSavingRate))
	b.WriteString(fmt.Sprintf("- net_saving_rate: `%.4f`\n", metrics.NetSavingRate))
	b.WriteString(fmt.Sprintf("- net_cost_saved_usd: `%.8f`\n", metrics.NetCostSavedUSD))
	if metrics.GrossCostUSD == 0 {
		b.WriteString("- pricing_note: `model_pricing is zero; net_saving_rate is not business-actionable`\n")
	}
	b.WriteString("\n## Cache Layers\n")
	for _, key := range sortedMergeKeys(metrics.LayerContribution) {
		b.WriteString(fmt.Sprintf("- `%s`: `%d`\n", key, metrics.LayerContribution[key]))
	}
	b.WriteString("\n## Category Saving Distribution\n")
	for _, category := range sortedCategoryCostKeys(metrics.CategoryMetrics) {
		categoryMetric := metrics.CategoryMetrics[category]
		b.WriteString(fmt.Sprintf("- `%s`: total=`%d` cached_tokens_saved=`%d` cached_cost_saved=`%.8f` net_saved=`%.8f`\n",
			category, categoryMetric.Total, categoryMetric.CachedTokensSaved, categoryMetric.CachedCostUSDSaved, categoryMetric.NetCostSavedUSD))
	}
	b.WriteString("\n## Request Log Consistency\n")
	b.WriteString(fmt.Sprintf("- request_log_db_path_count: `%d`\n", result.RequestLogDBPathCount))
	b.WriteString(fmt.Sprintf("- request_log_total: `%d`\n", result.RequestLogTotal))
	b.WriteString(fmt.Sprintf("- decision_log total: `%d`\n", result.DecisionSummary.Total))
	b.WriteString(fmt.Sprintf("- consistent: `%t`\n", result.RequestLogConsistent))
	if len(result.RequestLogWarnings) > 0 {
		for _, warning := range result.RequestLogWarnings {
			b.WriteString(fmt.Sprintf("- warning: `%s`\n", warning))
		}
	}
	b.WriteString("\n## Latency\n")
	if result.ProgressSummary == nil {
		b.WriteString("- progress_log summary: unavailable\n")
		b.WriteString("- timeout count: `0`\n")
		b.WriteString("- error count: `0`\n")
	} else {
		writeProgressSummaryMarkdown(&b, *result.ProgressSummary)
	}
	b.WriteString("\n## Refusals\n")
	for _, key := range sortedMergeKeys(metrics.RefusedByRule) {
		b.WriteString(fmt.Sprintf("- `%s`: `%d`\n", key, metrics.RefusedByRule[key]))
	}
	return b.String()
}

func summarizeRequestLogDBs(ctx context.Context, paths []string) (int, []string) {
	total := 0
	var warnings []string
	for _, path := range nonEmptyStrings(paths) {
		store, err := OpenRequestLogStore(ctx, path)
		if err != nil {
			warnings = append(warnings, SanitizeErrorSummary(err))
			continue
		}
		runs, err := store.ListRuns(ctx)
		if err != nil {
			warnings = append(warnings, SanitizeErrorSummary(err))
			_ = store.Close()
			continue
		}
		for _, run := range runs {
			summary, err := store.Summary(ctx, run.RunID)
			if err != nil {
				warnings = append(warnings, SanitizeErrorSummary(err))
				continue
			}
			total += summary.Total
		}
		_ = store.Close()
	}
	return total, warnings
}

func countHardTrapSafeHits(records []DecisionRecord) int {
	count := 0
	for _, record := range records {
		if record.Category == "hard_trap" && record.CacheLayer == "semantic" && record.JudgeCacheSafe != nil && *record.JudgeCacheSafe && !record.BadHit {
			count++
		}
	}
	return count
}

func countExpectedRefusalLeaks(records []DecisionRecord) int {
	count := 0
	for _, record := range records {
		if record.ExpectedCacheBehavior == "expected_refusal" && record.CacheLayer == "semantic" && record.JudgeCacheSafe != nil && *record.JudgeCacheSafe && !record.BadHit {
			count++
		}
	}
	return count
}

func savedTokensByLayer(records []DecisionRecord, layer string) int {
	total := 0
	for _, record := range records {
		if record.CacheLayer != layer {
			continue
		}
		total += record.CachedPromptTokensSaved + record.CachedCompletionTokensSaved
	}
	return total
}

func mergeStringInt(dst map[string]int, src map[string]int) {
	for key, value := range src {
		dst[key] += value
	}
}

func mergeCategoryMetrics(dst map[string]CategoryMetric, src map[string]CategoryMetric) {
	for category, metric := range src {
		current := dst[category]
		current.Total += metric.Total
		current.SemanticCandidates += metric.SemanticCandidates
		current.SemanticJudgedSafe += metric.SemanticJudgedSafe
		current.SafeCostSaved += metric.SafeCostSaved
		current.SemanticRejectedBySafety += metric.SemanticRejectedBySafety
		current.CachedTokensSaved += metric.CachedTokensSaved
		current.CachedCostUSDSaved += metric.CachedCostUSDSaved
		current.NetCostSavedUSD += metric.NetCostSavedUSD
		current.badHits += int(math.Round(metric.BadHitRate * float64(metric.Total)))
		dst[category] = current
	}
}

func writeJSONFile(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func nonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return out
}

func sortedMergeKeys(values map[string]int) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
