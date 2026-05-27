package benchmark

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

type CostReportInput struct {
	MetricsPath  string
	RequestLogDB string
	RunID        string
}

type CostReportData struct {
	Metrics           Metrics            `json:"metrics"`
	RequestLogSummary *RequestLogSummary `json:"request_log_summary,omitempty"`
}

func BuildCostReport(ctx context.Context, input CostReportInput) (CostReportData, string, error) {
	metrics, err := loadMetricsFile(input.MetricsPath)
	if err != nil {
		return CostReportData{}, "", err
	}
	data := CostReportData{Metrics: metrics}
	if strings.TrimSpace(input.RequestLogDB) != "" {
		store, err := OpenRequestLogStore(ctx, input.RequestLogDB)
		if err != nil {
			return CostReportData{}, "", err
		}
		defer func() { _ = store.Close() }()
		runID := strings.TrimSpace(input.RunID)
		if runID == "" {
			runs, err := store.ListRuns(ctx)
			if err == nil && len(runs) == 1 {
				runID = runs[0].RunID
			}
		}
		if runID != "" {
			summary, err := store.Summary(ctx, runID)
			if err != nil {
				return CostReportData{}, "", err
			}
			data.RequestLogSummary = &summary
		}
	}
	return data, renderCostReportMarkdown(data), nil
}

func WriteCostReport(path string, markdown string) error {
	return WriteBaselineReport(path, markdown)
}

func renderCostReportMarkdown(data CostReportData) string {
	metrics := data.Metrics
	var b strings.Builder
	b.WriteString("# Token Cost Accounting Report\n\n")
	b.WriteString("## Summary\n")
	b.WriteString(fmt.Sprintf("- total requests: `%d`\n", metrics.Total))
	b.WriteString(fmt.Sprintf("- hit_rate: `%.4f`\n", metrics.HitRate))
	b.WriteString(fmt.Sprintf("- bad_hit_rate: `%.4f`\n", metrics.BadHitRate))
	b.WriteString(fmt.Sprintf("- token_estimation_mode: `%s`\n", defaultString(metrics.TokenEstimationMode, "unknown")))
	b.WriteString(fmt.Sprintf("- total_tokens: `%d`\n", metrics.TotalTokens))
	b.WriteString(fmt.Sprintf("- cached_tokens_saved: `%d`\n", metrics.CachedTokensSaved))
	b.WriteString(fmt.Sprintf("- token saving rate: `%.4f`\n", ratioInt(metrics.CachedTokensSaved, metrics.TotalTokens)))
	b.WriteString(fmt.Sprintf("- gross_cost_usd: `%.8f`\n", metrics.GrossCostUSD))
	b.WriteString(fmt.Sprintf("- cached_cost_usd_saved: `%.8f`\n", metrics.CachedCostUSDSaved))
	b.WriteString(fmt.Sprintf("- judge_cost_usd: `%.8f`\n", metrics.JudgeCostUSD))
	b.WriteString(fmt.Sprintf("- embedding_cost_usd: `%.8f`\n", metrics.EmbeddingCostUSD))
	b.WriteString(fmt.Sprintf("- net_cost_saved_usd: `%.8f`\n", metrics.NetCostSavedUSD))
	b.WriteString(fmt.Sprintf("- gross_saving_rate: `%.4f`\n", metrics.GrossSavingRate))
	b.WriteString(fmt.Sprintf("- net_saving_rate: `%.4f`\n", metrics.NetSavingRate))
	b.WriteString(fmt.Sprintf("- judge + embedding cost share: `%.4f`\n", costShare(metrics.JudgeCostUSD+metrics.EmbeddingCostUSD, metrics.GrossCostUSD)))

	b.WriteString("\n## Category Token Saving\n")
	if len(metrics.CategoryMetrics) == 0 {
		b.WriteString("- none\n")
	} else {
		for _, category := range sortedCategoryCostKeys(metrics.CategoryMetrics) {
			categoryMetric := metrics.CategoryMetrics[category]
			b.WriteString(fmt.Sprintf("- `%s`: total=`%d` semantic_safe=`%d` cached_tokens_saved=`%d` cached_cost_saved=`%.8f` net_saved=`%.8f`\n",
				category, categoryMetric.Total, categoryMetric.SemanticJudgedSafe, categoryMetric.CachedTokensSaved, categoryMetric.CachedCostUSDSaved, categoryMetric.NetCostSavedUSD))
		}
	}

	b.WriteString("\n## Most Profitable Categories\n")
	writeCategoryProfitList(&b, metrics.CategoryMetrics, true)

	b.WriteString("\n## Least Suitable Categories\n")
	writeCategoryProfitList(&b, metrics.CategoryMetrics, false)

	b.WriteString("\n## Request Log Cross Check\n")
	if data.RequestLogSummary == nil {
		b.WriteString("- request_log summary: unavailable\n")
	} else {
		summary := data.RequestLogSummary
		b.WriteString(fmt.Sprintf("- request_log total: `%d`\n", summary.Total))
		b.WriteString(fmt.Sprintf("- request_log cached_tokens_saved: `%d`\n", summary.CachedTokensSaved))
		b.WriteString(fmt.Sprintf("- request_log net_cost_saved_usd: `%.8f`\n", summary.NetCostSavedUSD))
		b.WriteString(fmt.Sprintf("- request_log bad_hit_rate: `%.4f`\n", summary.BadHitRate))
	}

	b.WriteString("\n## Commercial Readiness Signal\n")
	if metrics.GrossCostUSD > 0 && metrics.TokenEstimationMode != "" && metrics.BadHitRate == 0 {
		b.WriteString("- usable for directional business modeling: YES\n")
	} else {
		b.WriteString("- usable for directional business modeling: NO\n")
	}
	b.WriteString("- note: Phase 1 cost uses local pricing config and approximate token estimation unless provider usage is available.\n")
	return b.String()
}

func ratioInt(numerator int, denominator int) float64 {
	if denominator <= 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

func costShare(numerator float64, denominator float64) float64 {
	if denominator <= 0 {
		return 0
	}
	return numerator / denominator
}

func sortedCategoryCostKeys(values map[string]CategoryMetric) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func writeCategoryProfitList(b *strings.Builder, values map[string]CategoryMetric, descending bool) {
	if len(values) == 0 {
		b.WriteString("- none\n")
		return
	}
	type entry struct {
		category string
		metric   CategoryMetric
	}
	entries := make([]entry, 0, len(values))
	for category, metric := range values {
		entries = append(entries, entry{category: category, metric: metric})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].metric.NetCostSavedUSD == entries[j].metric.NetCostSavedUSD {
			return entries[i].category < entries[j].category
		}
		if descending {
			return entries[i].metric.NetCostSavedUSD > entries[j].metric.NetCostSavedUSD
		}
		return entries[i].metric.NetCostSavedUSD < entries[j].metric.NetCostSavedUSD
	})
	limit := 5
	if len(entries) < limit {
		limit = len(entries)
	}
	for i := 0; i < limit; i++ {
		entry := entries[i]
		b.WriteString(fmt.Sprintf("- `%s`: net_saved=`%.8f` cached_tokens_saved=`%d` semantic_safe=`%d` refused=`%d`\n",
			entry.category, entry.metric.NetCostSavedUSD, entry.metric.CachedTokensSaved, entry.metric.SemanticJudgedSafe, entry.metric.SemanticRejectedBySafety))
	}
}
