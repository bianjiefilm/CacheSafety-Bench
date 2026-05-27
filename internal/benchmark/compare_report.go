package benchmark

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

type CompareReportInput struct {
	BaselineManifestPath  string
	CandidateManifestPath string
}

type CountDiff struct {
	Baseline  int `json:"baseline"`
	Candidate int `json:"candidate"`
	Delta     int `json:"delta"`
}

type CompareWarning struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

type CompareBadHitDiff struct {
	New        []DecisionRecord `json:"new"`
	Resolved   []DecisionRecord `json:"resolved"`
	Persistent []DecisionRecord `json:"persistent"`
}

type CompareReportData struct {
	Baseline                 BaselineManifest     `json:"baseline"`
	Candidate                BaselineManifest     `json:"candidate"`
	BaselineData             BaselineReportData   `json:"baseline_data"`
	CandidateData            BaselineReportData   `json:"candidate_data"`
	TotalSamplesAligned      bool                 `json:"total_samples_aligned"`
	StrataCoverageAligned    bool                 `json:"strata_coverage_aligned"`
	MissingStrataInCandidate []string             `json:"missing_strata_in_candidate,omitempty"`
	NewStrataInCandidate     []string             `json:"new_strata_in_candidate,omitempty"`
	CacheOutcomeDiff         map[string]CountDiff `json:"cache_outcome_diff"`
	RefusedReasonDiff        map[string]CountDiff `json:"refused_reason_diff"`
	SafeHitRatioDiff         float64              `json:"safe_hit_ratio_diff"`
	BadHitDiff               CompareBadHitDiff    `json:"bad_hit_diff"`
	Warnings                 []CompareWarning     `json:"warnings,omitempty"`
}

func BuildCompareReport(ctx context.Context, input CompareReportInput) (CompareReportData, string, error) {
	baselineManifest, err := LoadBaselineManifest(input.BaselineManifestPath)
	if err != nil {
		return CompareReportData{}, "", err
	}
	candidateManifest, err := LoadBaselineManifest(input.CandidateManifestPath)
	if err != nil {
		return CompareReportData{}, "", err
	}
	baselineData, _, err := BuildBaselineReport(ctx, BaselineReportInput{
		MetricsPath:     baselineManifest.MetricsPath,
		DecisionLogPath: baselineManifest.DecisionLogPath,
		RequestLogDB:    baselineManifest.RequestLogDBPath,
		RunID:           baselineManifest.RunID,
	})
	if err != nil {
		return CompareReportData{}, "", err
	}
	candidateData, _, err := BuildBaselineReport(ctx, BaselineReportInput{
		MetricsPath:     candidateManifest.MetricsPath,
		DecisionLogPath: candidateManifest.DecisionLogPath,
		RequestLogDB:    candidateManifest.RequestLogDBPath,
		RunID:           candidateManifest.RunID,
	})
	if err != nil {
		return CompareReportData{}, "", err
	}
	data := CompareReportData{
		Baseline:            baselineManifest,
		Candidate:           candidateManifest,
		BaselineData:        baselineData,
		CandidateData:       candidateData,
		TotalSamplesAligned: baselineManifest.SampleCount == candidateManifest.SampleCount,
		CacheOutcomeDiff:    compareCacheOutcome(baselineData, candidateData),
		RefusedReasonDiff:   compareCountMaps(baselineData.DecisionSummary.ByRefusedReason, candidateData.DecisionSummary.ByRefusedReason),
		SafeHitRatioDiff:    candidateData.SafeHitCandidateRatio - baselineData.SafeHitCandidateRatio,
		BadHitDiff:          compareBadHitCandidates(baselineData.BadHitCandidates, candidateData.BadHitCandidates),
	}
	data.StrataCoverageAligned, data.MissingStrataInCandidate, data.NewStrataInCandidate = compareStrataCoverage(baselineManifest.SampledByStratum, candidateManifest.SampledByStratum)
	data.Warnings = buildCompareWarnings(data)
	return data, renderCompareMarkdown(data), nil
}

func WriteCompareReport(path string, markdown string) error {
	return WriteBaselineReport(path, markdown)
}

func compareCacheOutcome(baseline BaselineReportData, candidate BaselineReportData) map[string]CountDiff {
	out := map[string]CountDiff{}
	keys := []string{"exact", "canonical", "semantic", "miss", "refused"}
	for _, key := range keys {
		base := cacheOutcomeCount(baseline, key)
		cand := cacheOutcomeCount(candidate, key)
		out[key] = CountDiff{Baseline: base, Candidate: cand, Delta: cand - base}
	}
	return out
}

func cacheOutcomeCount(data BaselineReportData, key string) int {
	if key == "refused" {
		return data.Metrics.SemanticRejectedBySafety
	}
	return data.Metrics.LayerContribution[key]
}

func compareCountMaps(baseline, candidate map[string]int) map[string]CountDiff {
	keys := map[string]struct{}{}
	for key := range baseline {
		keys[key] = struct{}{}
	}
	for key := range candidate {
		keys[key] = struct{}{}
	}
	out := make(map[string]CountDiff, len(keys))
	for key := range keys {
		base := baseline[key]
		cand := candidate[key]
		out[key] = CountDiff{Baseline: base, Candidate: cand, Delta: cand - base}
	}
	return out
}

func compareStrataCoverage(baseline, candidate map[string]int) (bool, []string, []string) {
	missing := make([]string, 0)
	added := make([]string, 0)
	for key := range baseline {
		if _, ok := candidate[key]; !ok {
			missing = append(missing, key)
		}
	}
	for key := range candidate {
		if _, ok := baseline[key]; !ok {
			added = append(added, key)
		}
	}
	sort.Strings(missing)
	sort.Strings(added)
	return len(missing) == 0 && len(added) == 0, missing, added
}

func compareBadHitCandidates(baseline, candidate []DecisionRecord) CompareBadHitDiff {
	baseMap := decisionRecordIndex(baseline)
	candMap := decisionRecordIndex(candidate)
	out := CompareBadHitDiff{
		New:        []DecisionRecord{},
		Resolved:   []DecisionRecord{},
		Persistent: []DecisionRecord{},
	}
	for key, record := range candMap {
		if _, ok := baseMap[key]; ok {
			out.Persistent = append(out.Persistent, record)
			continue
		}
		out.New = append(out.New, record)
	}
	for key, record := range baseMap {
		if _, ok := candMap[key]; !ok {
			out.Resolved = append(out.Resolved, record)
		}
	}
	sortDecisionRecords(out.New)
	sortDecisionRecords(out.Resolved)
	sortDecisionRecords(out.Persistent)
	return out
}

func decisionRecordIndex(records []DecisionRecord) map[string]DecisionRecord {
	out := make(map[string]DecisionRecord, len(records))
	for _, record := range records {
		key := record.SampleID + "|" + record.Category + "|" + record.CacheLayer
		out[key] = record
	}
	return out
}

func sortDecisionRecords(records []DecisionRecord) {
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].Category == records[j].Category {
			return records[i].SampleID < records[j].SampleID
		}
		return records[i].Category < records[j].Category
	})
}

func buildCompareWarnings(data CompareReportData) []CompareWarning {
	var warnings []CompareWarning
	if !data.TotalSamplesAligned {
		warnings = append(warnings, CompareWarning{
			Kind:    "sample_count_mismatch",
			Message: fmt.Sprintf("candidate sample_count=%d baseline sample_count=%d", data.Candidate.SampleCount, data.Baseline.SampleCount),
		})
	}
	if !data.StrataCoverageAligned {
		warnings = append(warnings, CompareWarning{
			Kind:    "strata_coverage_mismatch",
			Message: fmt.Sprintf("missing=%d new=%d", len(data.MissingStrataInCandidate), len(data.NewStrataInCandidate)),
		})
	}
	if data.SafeHitRatioDiff < -0.05 {
		warnings = append(warnings, CompareWarning{
			Kind:    "safe_hit_ratio_drop",
			Message: fmt.Sprintf("candidate safe hit candidate ratio dropped by %.4f", -data.SafeHitRatioDiff),
		})
	}
	if len(data.BadHitDiff.New) > 0 {
		warnings = append(warnings, CompareWarning{
			Kind:    "new_bad_hit_candidates",
			Message: fmt.Sprintf("%d new bad hit candidates appeared", len(data.BadHitDiff.New)),
		})
	}
	if diff, ok := data.CacheOutcomeDiff["refused"]; ok && diff.Delta > 2 {
		warnings = append(warnings, CompareWarning{
			Kind:    "refusal_increase",
			Message: fmt.Sprintf("candidate refusals increased by %d", diff.Delta),
		})
	}
	if metric, ok := data.CandidateData.DecisionSummary.CategoryCoverage["hard_negative"]; ok && metric.SemanticJudgedSafe > 0 {
		warnings = append(warnings, CompareWarning{
			Kind:    "hard_negative_allowed",
			Message: fmt.Sprintf("candidate allowed %d hard_negative semantic safe hits", metric.SemanticJudgedSafe),
		})
	}
	return warnings
}

func renderCompareMarkdown(data CompareReportData) string {
	var b strings.Builder
	b.WriteString("# Int8 Candidate Compare Report\n\n")
	b.WriteString("## Inputs\n")
	b.WriteString(fmt.Sprintf("- baseline manifest: `%s`\n", filepath.Base(data.Baseline.MetricsPath)))
	b.WriteString(fmt.Sprintf("- candidate manifest: `%s`\n", filepath.Base(data.Candidate.MetricsPath)))
	b.WriteString(fmt.Sprintf("- baseline run_id: `%s`\n", data.Baseline.RunID))
	b.WriteString(fmt.Sprintf("- candidate run_id: `%s`\n", data.Candidate.RunID))

	b.WriteString("\n## Alignment Check\n")
	b.WriteString(fmt.Sprintf("- total samples aligned: `%t`\n", data.TotalSamplesAligned))
	b.WriteString(fmt.Sprintf("- baseline samples: `%d`\n", data.Baseline.SampleCount))
	b.WriteString(fmt.Sprintf("- candidate samples: `%d`\n", data.Candidate.SampleCount))
	b.WriteString(fmt.Sprintf("- strata coverage aligned: `%t`\n", data.StrataCoverageAligned))
	if len(data.MissingStrataInCandidate) > 0 {
		b.WriteString("- missing strata in candidate:\n")
		for _, key := range data.MissingStrataInCandidate {
			b.WriteString(fmt.Sprintf("  - `%s`\n", key))
		}
	}
	if len(data.NewStrataInCandidate) > 0 {
		b.WriteString("- new strata in candidate:\n")
		for _, key := range data.NewStrataInCandidate {
			b.WriteString(fmt.Sprintf("  - `%s`\n", key))
		}
	}

	b.WriteString("\n## Cache Outcome Diff\n")
	for _, key := range []string{"exact", "canonical", "semantic", "miss", "refused"} {
		diff := data.CacheOutcomeDiff[key]
		b.WriteString(fmt.Sprintf("- `%s`: baseline=`%d` candidate=`%d` delta=`%+d`\n", key, diff.Baseline, diff.Candidate, diff.Delta))
	}

	b.WriteString("\n## Safe Hit Candidate Ratio Diff\n")
	b.WriteString(fmt.Sprintf("- baseline: `%.4f`\n", data.BaselineData.SafeHitCandidateRatio))
	b.WriteString(fmt.Sprintf("- candidate: `%.4f`\n", data.CandidateData.SafeHitCandidateRatio))
	b.WriteString(fmt.Sprintf("- delta: `%+.4f`\n", data.SafeHitRatioDiff))

	b.WriteString("\n## Bad Hit Candidate Diff\n")
	writeDecisionRecordList(&b, "new", data.BadHitDiff.New)
	writeDecisionRecordList(&b, "resolved", data.BadHitDiff.Resolved)
	writeDecisionRecordList(&b, "persistent", data.BadHitDiff.Persistent)

	b.WriteString("\n## Refused Reason Diff\n")
	if len(data.RefusedReasonDiff) == 0 {
		b.WriteString("- none\n")
	} else {
		for _, key := range sortedCountDiffKeys(data.RefusedReasonDiff) {
			diff := data.RefusedReasonDiff[key]
			b.WriteString(fmt.Sprintf("- `%s`: baseline=`%d` candidate=`%d` delta=`%+d`\n", key, diff.Baseline, diff.Candidate, diff.Delta))
		}
	}

	b.WriteString("\n## Judge Delta Distribution\n")
	b.WriteString("- baseline:\n")
	for _, key := range sortedIntKeys(data.BaselineData.JudgeDistribution.Deltas) {
		b.WriteString(fmt.Sprintf("  - `%s`: `%d`\n", key, data.BaselineData.JudgeDistribution.Deltas[key]))
	}
	b.WriteString("- candidate:\n")
	for _, key := range sortedIntKeys(data.CandidateData.JudgeDistribution.Deltas) {
		b.WriteString(fmt.Sprintf("  - `%s`: `%d`\n", key, data.CandidateData.JudgeDistribution.Deltas[key]))
	}

	b.WriteString("\n## Consistency Check\n")
	b.WriteString(fmt.Sprintf("- baseline consistent: `%t`\n", data.BaselineData.Consistency.Matches))
	b.WriteString(fmt.Sprintf("- candidate consistent: `%t`\n", data.CandidateData.Consistency.Matches))
	if len(data.BaselineData.Consistency.Issues) > 0 {
		b.WriteString("- baseline issues:\n")
		for _, issue := range data.BaselineData.Consistency.Issues {
			b.WriteString(fmt.Sprintf("  - `%s`\n", issue))
		}
	}
	if len(data.CandidateData.Consistency.Issues) > 0 {
		b.WriteString("- candidate issues:\n")
		for _, issue := range data.CandidateData.Consistency.Issues {
			b.WriteString(fmt.Sprintf("  - `%s`\n", issue))
		}
	}

	b.WriteString("\n## Recommendation Signals\n")
	if len(data.Warnings) == 0 {
		b.WriteString("- no comparison warnings from the current heuristics\n")
	} else {
		for _, warning := range data.Warnings {
			b.WriteString(fmt.Sprintf("- `%s`: %s\n", warning.Kind, warning.Message))
		}
	}
	return b.String()
}

func writeDecisionRecordList(b *strings.Builder, label string, records []DecisionRecord) {
	b.WriteString(fmt.Sprintf("- %s: `%d`\n", label, len(records)))
	for _, record := range records {
		b.WriteString(fmt.Sprintf("  - `%s` `%s` cached=`%.1f` fresh=`%.1f` delta=`%.1f`\n", record.SampleID, record.Category, record.CachedScore, record.FreshScore, record.JudgeDelta))
	}
}

func sortedCountDiffKeys(input map[string]CountDiff) []string {
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
