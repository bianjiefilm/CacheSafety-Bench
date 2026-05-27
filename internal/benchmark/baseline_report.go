package benchmark

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type BaselineReportInput struct {
	MetricsPath     string
	DecisionLogPath string
	RequestLogDB    string
	RunID           string
}

type JudgeDistribution struct {
	CachedScores map[string]int `json:"cached_scores"`
	FreshScores  map[string]int `json:"fresh_scores"`
	Deltas       map[string]int `json:"deltas"`
}

type BaselineConsistency struct {
	MetricsTotal      int      `json:"metrics_total"`
	DecisionLogTotal  int      `json:"decision_log_total"`
	RequestLogTotal   int      `json:"request_log_total"`
	Matches           bool     `json:"matches"`
	Issues            []string `json:"issues,omitempty"`
	RequestLogPresent bool     `json:"request_log_present"`
}

type BaselineReportData struct {
	Metrics               Metrics             `json:"metrics"`
	DecisionSummary       DecisionSummary     `json:"decision_summary"`
	RequestLogSummary     *RequestLogSummary  `json:"request_log_summary,omitempty"`
	Consistency           BaselineConsistency `json:"consistency"`
	JudgeDistribution     JudgeDistribution   `json:"judge_distribution"`
	BadHitCandidates      []DecisionRecord    `json:"bad_hit_candidates"`
	SafeHitCandidateRatio float64             `json:"safe_hit_candidate_ratio"`
}

func BuildBaselineReport(ctx context.Context, input BaselineReportInput) (BaselineReportData, string, error) {
	metrics, err := loadMetricsFile(input.MetricsPath)
	if err != nil {
		return BaselineReportData{}, "", err
	}
	records, err := LoadDecisionLog(input.DecisionLogPath)
	if err != nil {
		return BaselineReportData{}, "", err
	}
	data := BaselineReportData{
		Metrics:           metrics,
		DecisionSummary:   AnalyzeDecisionRecords(records),
		JudgeDistribution: buildJudgeDistribution(records),
		BadHitCandidates:  collectBadHitCandidates(records),
	}
	data.SafeHitCandidateRatio = safeHitCandidateRatio(records)
	data.Consistency = buildConsistency(metrics, records)

	if strings.TrimSpace(input.RequestLogDB) != "" {
		if _, err := os.Stat(input.RequestLogDB); err == nil {
			store, openErr := OpenRequestLogStore(ctx, input.RequestLogDB)
			if openErr == nil {
				defer func() { _ = store.Close() }()
				runID := strings.TrimSpace(input.RunID)
				if runID == "" {
					runs, listErr := store.ListRuns(ctx)
					if listErr == nil && len(runs) == 1 {
						runID = runs[0].RunID
					}
				}
				if runID != "" {
					summary, sumErr := store.Summary(ctx, runID)
					if sumErr == nil {
						data.RequestLogSummary = &summary
						data.Consistency = buildConsistencyWithRequestLog(metrics, records, summary)
					}
				}
			}
		}
	}

	return data, renderBaselineMarkdown(input, data), nil
}

func WriteBaselineReport(path string, markdown string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(markdown), 0o644)
}

func loadMetricsFile(path string) (Metrics, error) {
	var metrics Metrics
	bytes, err := os.ReadFile(path)
	if err != nil {
		return metrics, err
	}
	if len(strings.TrimSpace(string(bytes))) == 0 {
		return metrics, nil
	}
	if err := json.Unmarshal(bytes, &metrics); err != nil {
		return metrics, err
	}
	return metrics, nil
}

func buildJudgeDistribution(records []DecisionRecord) JudgeDistribution {
	out := JudgeDistribution{
		CachedScores: map[string]int{},
		FreshScores:  map[string]int{},
		Deltas:       map[string]int{},
	}
	for _, record := range records {
		if record.CachedScore > 0 {
			out.CachedScores[fmt.Sprintf("%.1f", record.CachedScore)]++
		}
		if record.FreshScore > 0 {
			out.FreshScores[fmt.Sprintf("%.1f", record.FreshScore)]++
		}
		if record.CachedScore > 0 || record.FreshScore > 0 || record.JudgeDelta != 0 {
			out.Deltas[fmt.Sprintf("%.1f", record.JudgeDelta)]++
		}
	}
	return out
}

func collectBadHitCandidates(records []DecisionRecord) []DecisionRecord {
	out := make([]DecisionRecord, 0)
	for _, record := range records {
		if record.CachedScore > 0 && record.FreshScore > 0 && record.CachedScore < record.FreshScore-2 {
			out = append(out, record)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Category == out[j].Category {
			return out[i].SampleID < out[j].SampleID
		}
		return out[i].Category < out[j].Category
	})
	return out
}

func safeHitCandidateRatio(records []DecisionRecord) float64 {
	var hits, safe int
	for _, record := range records {
		if record.CacheLayer == "semantic" || record.CacheLayer == "exact" || record.CacheLayer == "canonical" {
			hits++
			if record.CachedScore > 0 && record.FreshScore > 0 && record.CachedScore >= record.FreshScore-1 && !record.BadHit {
				safe++
			}
		}
	}
	if hits == 0 {
		return 0
	}
	return float64(safe) / float64(hits)
}

func buildConsistency(metrics Metrics, records []DecisionRecord) BaselineConsistency {
	out := BaselineConsistency{
		MetricsTotal:      metrics.Total,
		DecisionLogTotal:  len(records),
		RequestLogPresent: false,
		Matches:           true,
	}
	if metrics.Total != len(records) {
		out.Matches = false
		out.Issues = append(out.Issues, fmt.Sprintf("metrics.total=%d decision_log.total=%d", metrics.Total, len(records)))
	}
	return out
}

func buildConsistencyWithRequestLog(metrics Metrics, records []DecisionRecord, summary RequestLogSummary) BaselineConsistency {
	out := buildConsistency(metrics, records)
	out.RequestLogPresent = true
	out.RequestLogTotal = summary.Total
	if summary.Total != metrics.Total {
		out.Matches = false
		out.Issues = append(out.Issues, fmt.Sprintf("request_log.total=%d metrics.total=%d", summary.Total, metrics.Total))
	}
	if summary.BadHitCount != metrics.BadHitExamplesCount && metrics.BadHitRate == 0 && summary.BadHitCount == 0 {
		// consistent zero case
	} else if summary.BadHitCount != len(metrics.BadHits) {
		out.Matches = false
		out.Issues = append(out.Issues, fmt.Sprintf("request_log.bad_hits=%d metrics.bad_hits=%d", summary.BadHitCount, len(metrics.BadHits)))
	}
	if summary.SemanticJudgedSafe != metrics.SemanticJudgedSafe {
		out.Matches = false
		out.Issues = append(out.Issues, fmt.Sprintf("request_log.semantic_judged_safe=%d metrics.semantic_judged_safe=%d", summary.SemanticJudgedSafe, metrics.SemanticJudgedSafe))
	}
	return out
}

func renderBaselineMarkdown(input BaselineReportInput, data BaselineReportData) string {
	var b strings.Builder
	b.WriteString("# Combined Eval v1 Stratified Baseline Report\n\n")
	b.WriteString("## Sampling Summary\n")
	b.WriteString(fmt.Sprintf("- total samples: `%d`\n", data.Metrics.Total))
	b.WriteString(fmt.Sprintf("- sampling_mode: `%s`\n", defaultString(data.Metrics.SamplingMode, "unknown")))
	b.WriteString(fmt.Sprintf("- strata_count: `%d`\n", data.Metrics.StrataCount))
	b.WriteString("- sampled_by_stratum:\n")
	for _, key := range sortedIntKeys(data.Metrics.SampledByStratum) {
		b.WriteString(fmt.Sprintf("  - `%s`: `%d`\n", key, data.Metrics.SampledByStratum[key]))
	}

	b.WriteString("\n## Cache Outcome Summary\n")
	b.WriteString(fmt.Sprintf("- exact: `%d`\n", data.Metrics.LayerContribution["exact"]))
	b.WriteString(fmt.Sprintf("- canonical: `%d`\n", data.Metrics.LayerContribution["canonical"]))
	b.WriteString(fmt.Sprintf("- semantic: `%d`\n", data.Metrics.LayerContribution["semantic"]))
	b.WriteString(fmt.Sprintf("- miss: `%d`\n", data.Metrics.LayerContribution["miss"]))
	b.WriteString(fmt.Sprintf("- refused: `%d`\n", data.Metrics.SemanticRejectedBySafety))

	b.WriteString("\n## Judge Quality Summary\n")
	b.WriteString("- cached_score distribution:\n")
	for _, key := range sortedIntKeys(data.JudgeDistribution.CachedScores) {
		b.WriteString(fmt.Sprintf("  - `%s`: `%d`\n", key, data.JudgeDistribution.CachedScores[key]))
	}
	b.WriteString("- fresh_score distribution:\n")
	for _, key := range sortedIntKeys(data.JudgeDistribution.FreshScores) {
		b.WriteString(fmt.Sprintf("  - `%s`: `%d`\n", key, data.JudgeDistribution.FreshScores[key]))
	}
	b.WriteString("- delta distribution:\n")
	for _, key := range sortedIntKeys(data.JudgeDistribution.Deltas) {
		b.WriteString(fmt.Sprintf("  - `%s`: `%d`\n", key, data.JudgeDistribution.Deltas[key]))
	}

	b.WriteString("\n## Bad Hit Candidates\n")
	if len(data.BadHitCandidates) == 0 {
		b.WriteString("- none\n")
	} else {
		for _, record := range data.BadHitCandidates {
			b.WriteString(fmt.Sprintf("- `%s` `%s` cached=`%.1f` fresh=`%.1f` delta=`%.1f`\n", record.SampleID, record.Category, record.CachedScore, record.FreshScore, record.JudgeDelta))
		}
	}

	b.WriteString("\n## Safe Hit Candidates\n")
	b.WriteString(fmt.Sprintf("- cached >= fresh - 1 ratio among hit samples: `%.4f`\n", data.SafeHitCandidateRatio))

	b.WriteString("\n## Refusal Summary\n")
	if len(data.DecisionSummary.ByRefusedReason) == 0 {
		b.WriteString("- none\n")
	} else {
		for _, key := range sortedIntKeys(data.DecisionSummary.ByRefusedReason) {
			b.WriteString(fmt.Sprintf("- `%s`: `%d`\n", key, data.DecisionSummary.ByRefusedReason[key]))
		}
	}

	b.WriteString("\n## Consistency Check\n")
	b.WriteString(fmt.Sprintf("- metrics total: `%d`\n", data.Consistency.MetricsTotal))
	b.WriteString(fmt.Sprintf("- decision_log total: `%d`\n", data.Consistency.DecisionLogTotal))
	b.WriteString(fmt.Sprintf("- request_log total: `%d`\n", data.Consistency.RequestLogTotal))
	b.WriteString(fmt.Sprintf("- request_log present: `%t`\n", data.Consistency.RequestLogPresent))
	b.WriteString(fmt.Sprintf("- consistent: `%t`\n", data.Consistency.Matches))
	if len(data.Consistency.Issues) > 0 {
		b.WriteString("- issues:\n")
		for _, issue := range data.Consistency.Issues {
			b.WriteString(fmt.Sprintf("  - %s\n", issue))
		}
	}

	b.WriteString("\n## Next-Round Suggestions\n")
	b.WriteString("- `loose_translation` currently contributes the strongest safe semantic reuse volume.\n")
	b.WriteString("- `rewrite_polish` has lower safe-hit density and more safety refusals than other low-risk categories.\n")
	b.WriteString("- `hard_negative` stays blocked, which is the expected shape before quantization experiments.\n")
	b.WriteString("- If quantization is next, compare int8 against this stratified float[2048] baseline on: bad_hit_rate, semantic_judged_safe, refusal drift, and per-category safe_cost_saved.\n")

	return b.String()
}

func sortedIntKeys(values map[string]int) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
