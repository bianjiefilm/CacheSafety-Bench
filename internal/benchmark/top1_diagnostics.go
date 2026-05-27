package benchmark

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

type Top1DiagnosticsInput struct {
	FloatDecisionLogPath string
	Int8DecisionLogPath  string
	FloatRequestLogDB    string
	Int8RequestLogDB     string
	FocusSampleIDs       []string
}

type Top1DiagnosticsReport struct {
	TotalAlignedSamples        int                           `json:"total_aligned_samples"`
	FloatDecisionLogTotal      int                           `json:"float_decision_log_total"`
	Int8DecisionLogTotal       int                           `json:"int8_decision_log_total"`
	FloatRequestLogTotal       int                           `json:"float_request_log_total,omitempty"`
	Int8RequestLogTotal        int                           `json:"int8_request_log_total,omitempty"`
	RequestLogConsistent       bool                          `json:"request_log_consistent"`
	CacheLayerChangedCount     int                           `json:"cache_layer_changed_count"`
	SemanticTop1ChangedCount   int                           `json:"semantic_top1_changed_count"`
	AnswerChangedCount         int                           `json:"answer_changed_count"`
	SimilarityDeltaAvg         float64                       `json:"similarity_delta_avg"`
	SimilarityDeltaP95         float64                       `json:"similarity_delta_p95"`
	SimilarityDeltaMax         float64                       `json:"similarity_delta_max"`
	ScoreWorsenedCount         int                           `json:"score_worsened_count"`
	ScoreImprovedCount         int                           `json:"score_improved_count"`
	BadHitNewlyIntroducedCount int                           `json:"bad_hit_newly_introduced_count"`
	SafeHitLostCount           int                           `json:"safe_hit_lost_count"`
	SafeHitGainedCount         int                           `json:"safe_hit_gained_count"`
	TopChangedCategories       map[string]int                `json:"top_changed_categories"`
	Records                    []Top1ChangeRecord            `json:"records"`
	TopRiskyExamples           []Top1ChangeRecord            `json:"top_risky_examples"`
	FocusExamples              map[string][]Top1ChangeRecord `json:"focus_examples"`
	Warnings                   []Top1DiagnosticsWarning      `json:"warnings,omitempty"`
	TopKAvailable              bool                          `json:"top_k_available"`
	TopKUnavailableReason      string                        `json:"top_k_unavailable_reason,omitempty"`
}

type Top1DiagnosticsWarning struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

type Top1ChangeRecord struct {
	AlignmentKey                string   `json:"alignment_key"`
	SampleID                    string   `json:"sample_id"`
	Category                    string   `json:"category,omitempty"`
	ExpectedRisk                string   `json:"expected_risk,omitempty"`
	SourceKind                  string   `json:"source_kind,omitempty"`
	ExpectedCacheBehavior       string   `json:"expected_cache_behavior,omitempty"`
	FloatCacheLayer             string   `json:"float_cache_layer"`
	Int8CacheLayer              string   `json:"int8_cache_layer"`
	FloatSimilarity             float64  `json:"float_similarity,omitempty"`
	Int8Similarity              float64  `json:"int8_similarity,omitempty"`
	SimilarityDelta             float64  `json:"similarity_delta"`
	FloatCandidatePromptPreview string   `json:"float_candidate_prompt_preview,omitempty"`
	Int8CandidatePromptPreview  string   `json:"int8_candidate_prompt_preview,omitempty"`
	FloatCandidateAnswerPreview string   `json:"float_candidate_answer_preview,omitempty"`
	Int8CandidateAnswerPreview  string   `json:"int8_candidate_answer_preview,omitempty"`
	FloatCandidateAnswerLength  int      `json:"float_candidate_answer_length,omitempty"`
	Int8CandidateAnswerLength   int      `json:"int8_candidate_answer_length,omitempty"`
	CandidatePromptChanged      bool     `json:"candidate_prompt_changed"`
	CandidateAnswerChanged      bool     `json:"candidate_answer_changed"`
	FloatCachedScore            float64  `json:"float_cached_score,omitempty"`
	Int8CachedScore             float64  `json:"int8_cached_score,omitempty"`
	ScoreDeltaChange            float64  `json:"score_delta_change"`
	FloatJudgeReason            string   `json:"float_judge_reason,omitempty"`
	Int8JudgeReason             string   `json:"int8_judge_reason,omitempty"`
	FloatBadHit                 bool     `json:"float_bad_hit"`
	Int8BadHit                  bool     `json:"int8_bad_hit"`
	BadHitChanged               bool     `json:"bad_hit_changed"`
	FloatJudgeCacheSafe         *bool    `json:"float_judge_cache_safe,omitempty"`
	Int8JudgeCacheSafe          *bool    `json:"int8_judge_cache_safe,omitempty"`
	SafeHitLost                 bool     `json:"safe_hit_lost"`
	SafeHitGained               bool     `json:"safe_hit_gained"`
	FloatSafeCostSaved          float64  `json:"float_safe_cost_saved"`
	Int8SafeCostSaved           float64  `json:"int8_safe_cost_saved"`
	SafeCostSavedDelta          float64  `json:"safe_cost_saved_delta"`
	SafeCostSavedChanged        bool     `json:"safe_cost_saved_changed"`
	LikelyCauses                []string `json:"likely_causes,omitempty"`
}

func BuildTop1Diagnostics(ctx context.Context, input Top1DiagnosticsInput) (Top1DiagnosticsReport, string, error) {
	floatRecords, err := LoadDecisionLog(input.FloatDecisionLogPath)
	if err != nil {
		return Top1DiagnosticsReport{}, "", err
	}
	int8Records, err := LoadDecisionLog(input.Int8DecisionLogPath)
	if err != nil {
		return Top1DiagnosticsReport{}, "", err
	}
	report := Top1DiagnosticsReport{
		FloatDecisionLogTotal: len(floatRecords),
		Int8DecisionLogTotal:  len(int8Records),
		TopChangedCategories:  map[string]int{},
		FocusExamples:         map[string][]Top1ChangeRecord{},
		TopKAvailable:         false,
		TopKUnavailableReason: "decision logs and request_log databases contain only top1 previews; vector DB paths or topK traces are required for true topK diagnostics",
		RequestLogConsistent:  true,
	}
	floatRequestTotal, floatPresent, err := requestLogCount(ctx, input.FloatRequestLogDB)
	if err != nil {
		report.Warnings = append(report.Warnings, Top1DiagnosticsWarning{Kind: "float_request_log_error", Message: err.Error()})
	}
	int8RequestTotal, int8Present, err := requestLogCount(ctx, input.Int8RequestLogDB)
	if err != nil {
		report.Warnings = append(report.Warnings, Top1DiagnosticsWarning{Kind: "int8_request_log_error", Message: err.Error()})
	}
	if floatPresent {
		report.FloatRequestLogTotal = floatRequestTotal
		if floatRequestTotal != len(floatRecords) {
			report.RequestLogConsistent = false
			report.Warnings = append(report.Warnings, Top1DiagnosticsWarning{Kind: "float_request_log_count_mismatch", Message: fmt.Sprintf("request_log=%d decision_log=%d", floatRequestTotal, len(floatRecords))})
		}
	} else if strings.TrimSpace(input.FloatRequestLogDB) != "" {
		report.RequestLogConsistent = false
		report.Warnings = append(report.Warnings, Top1DiagnosticsWarning{Kind: "float_request_log_missing", Message: input.FloatRequestLogDB})
	}
	if int8Present {
		report.Int8RequestLogTotal = int8RequestTotal
		if int8RequestTotal != len(int8Records) {
			report.RequestLogConsistent = false
			report.Warnings = append(report.Warnings, Top1DiagnosticsWarning{Kind: "int8_request_log_count_mismatch", Message: fmt.Sprintf("request_log=%d decision_log=%d", int8RequestTotal, len(int8Records))})
		}
	} else if strings.TrimSpace(input.Int8RequestLogDB) != "" {
		report.RequestLogConsistent = false
		report.Warnings = append(report.Warnings, Top1DiagnosticsWarning{Kind: "int8_request_log_missing", Message: input.Int8RequestLogDB})
	}

	floatIndex, floatDuplicates := decisionRecordAlignmentIndex(floatRecords)
	int8Index, int8Duplicates := decisionRecordAlignmentIndex(int8Records)
	for _, warning := range duplicateWarnings("float", floatDuplicates) {
		report.Warnings = append(report.Warnings, warning)
	}
	for _, warning := range duplicateWarnings("int8", int8Duplicates) {
		report.Warnings = append(report.Warnings, warning)
	}

	keys := make([]string, 0, len(floatIndex))
	for key := range floatIndex {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var absSimilarityDeltas []float64
	focusSet := map[string]struct{}{}
	for _, sampleID := range input.FocusSampleIDs {
		focusSet[sampleID] = struct{}{}
	}
	for _, key := range keys {
		floatRecord := floatIndex[key]
		int8Record, ok := int8Index[key]
		if !ok {
			report.Warnings = append(report.Warnings, Top1DiagnosticsWarning{Kind: "missing_int8_sample", Message: key})
			continue
		}
		record := compareTop1Record(key, floatRecord, int8Record)
		report.Records = append(report.Records, record)
		report.TotalAlignedSamples++
		absSimilarityDeltas = append(absSimilarityDeltas, math.Abs(record.SimilarityDelta))
		if record.FloatCacheLayer != record.Int8CacheLayer {
			report.CacheLayerChangedCount++
			report.TopChangedCategories[record.Category]++
		}
		if isSemanticTop1Changed(record) {
			report.SemanticTop1ChangedCount++
			report.TopChangedCategories[record.Category]++
		}
		if record.CandidateAnswerChanged {
			report.AnswerChangedCount++
		}
		if record.ScoreDeltaChange < 0 {
			report.ScoreWorsenedCount++
		} else if record.ScoreDeltaChange > 0 {
			report.ScoreImprovedCount++
		}
		if !record.FloatBadHit && record.Int8BadHit {
			report.BadHitNewlyIntroducedCount++
		}
		if record.SafeHitLost {
			report.SafeHitLostCount++
		}
		if record.SafeHitGained {
			report.SafeHitGainedCount++
		}
		if _, ok := focusSet[record.SampleID]; ok {
			report.FocusExamples[record.SampleID] = append(report.FocusExamples[record.SampleID], record)
		}
	}
	for key := range int8Index {
		if _, ok := floatIndex[key]; !ok {
			report.Warnings = append(report.Warnings, Top1DiagnosticsWarning{Kind: "missing_float_sample", Message: key})
		}
	}
	report.SimilarityDeltaAvg = averageFloat64(absSimilarityDeltas)
	report.SimilarityDeltaP95 = percentileFloat64(absSimilarityDeltas, 0.95)
	report.SimilarityDeltaMax = maxFloat64(absSimilarityDeltas)
	report.TopRiskyExamples = topRiskyTop1Examples(report.Records, 10)
	return report, renderTop1DiagnosticsMarkdown(report), nil
}

func WriteTop1DiagnosticsJSON(path string, report Top1DiagnosticsReport) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, payload, 0o644)
}

func WriteTop1DiagnosticsMarkdown(path string, markdown string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(markdown), 0o644)
}

func requestLogCount(ctx context.Context, path string) (int, bool, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return 0, false, nil
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return 0, false, nil
		}
		return 0, false, err
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return 0, true, err
	}
	defer func() { _ = db.Close() }()
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM request_log`).Scan(&count); err != nil {
		return 0, true, err
	}
	return count, true, nil
}

func decisionRecordAlignmentIndex(records []DecisionRecord) (map[string]DecisionRecord, map[string]int) {
	out := make(map[string]DecisionRecord, len(records))
	duplicates := map[string]int{}
	for _, record := range records {
		key := top1AlignmentKey(record)
		if _, exists := out[key]; exists {
			duplicates[key]++
		}
		out[key] = record
	}
	return out, duplicates
}

func top1AlignmentKey(record DecisionRecord) string {
	parts := []string{
		record.SampleID,
		defaultString(record.SourceKind, "unknown"),
		defaultString(record.ExpectedCacheBehavior, "unspecified"),
		record.RequestPreview,
	}
	return strings.Join(parts, "|")
}

func duplicateWarnings(side string, duplicates map[string]int) []Top1DiagnosticsWarning {
	if len(duplicates) == 0 {
		return nil
	}
	keys := make([]string, 0, len(duplicates))
	for key := range duplicates {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	warnings := make([]Top1DiagnosticsWarning, 0, len(keys))
	for _, key := range keys {
		warnings = append(warnings, Top1DiagnosticsWarning{Kind: side + "_duplicate_alignment_key", Message: key})
	}
	return warnings
}

func compareTop1Record(key string, floatRecord DecisionRecord, int8Record DecisionRecord) Top1ChangeRecord {
	record := Top1ChangeRecord{
		AlignmentKey:                key,
		SampleID:                    floatRecord.SampleID,
		Category:                    firstNonEmptyTop1(floatRecord.Category, int8Record.Category),
		ExpectedRisk:                firstNonEmptyTop1(floatRecord.ExpectedRisk, int8Record.ExpectedRisk),
		SourceKind:                  firstNonEmptyTop1(floatRecord.SourceKind, int8Record.SourceKind),
		ExpectedCacheBehavior:       firstNonEmptyTop1(floatRecord.ExpectedCacheBehavior, int8Record.ExpectedCacheBehavior),
		FloatCacheLayer:             floatRecord.CacheLayer,
		Int8CacheLayer:              int8Record.CacheLayer,
		FloatSimilarity:             floatRecord.Similarity,
		Int8Similarity:              int8Record.Similarity,
		SimilarityDelta:             int8Record.Similarity - floatRecord.Similarity,
		FloatCandidatePromptPreview: floatRecord.CandidatePromptPreview,
		Int8CandidatePromptPreview:  int8Record.CandidatePromptPreview,
		FloatCandidateAnswerPreview: floatRecord.CandidateAnswerPreview,
		Int8CandidateAnswerPreview:  int8Record.CandidateAnswerPreview,
		FloatCandidateAnswerLength:  len([]rune(floatRecord.CandidateAnswerPreview)),
		Int8CandidateAnswerLength:   len([]rune(int8Record.CandidateAnswerPreview)),
		CandidatePromptChanged:      normalizePreview(floatRecord.CandidatePromptPreview) != normalizePreview(int8Record.CandidatePromptPreview),
		CandidateAnswerChanged:      normalizePreview(floatRecord.CandidateAnswerPreview) != normalizePreview(int8Record.CandidateAnswerPreview),
		FloatCachedScore:            floatRecord.CachedScore,
		Int8CachedScore:             int8Record.CachedScore,
		ScoreDeltaChange:            int8Record.CachedScore - floatRecord.CachedScore,
		FloatJudgeReason:            floatRecord.JudgeReason,
		Int8JudgeReason:             int8Record.JudgeReason,
		FloatBadHit:                 floatRecord.BadHit,
		Int8BadHit:                  int8Record.BadHit,
		BadHitChanged:               floatRecord.BadHit != int8Record.BadHit,
		FloatJudgeCacheSafe:         floatRecord.JudgeCacheSafe,
		Int8JudgeCacheSafe:          int8Record.JudgeCacheSafe,
		FloatSafeCostSaved:          floatRecord.SafeCostSaved,
		Int8SafeCostSaved:           int8Record.SafeCostSaved,
		SafeCostSavedDelta:          int8Record.SafeCostSaved - floatRecord.SafeCostSaved,
	}
	record.SafeCostSavedChanged = math.Abs(record.SafeCostSavedDelta) > 1e-9
	record.SafeHitLost = safeDecisionHit(floatRecord) && !safeDecisionHit(int8Record)
	record.SafeHitGained = !safeDecisionHit(floatRecord) && safeDecisionHit(int8Record)
	record.LikelyCauses = classifyTop1Change(record)
	return record
}

func normalizePreview(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func safeDecisionHit(record DecisionRecord) bool {
	return record.JudgeCacheSafe != nil && *record.JudgeCacheSafe && !record.BadHit
}

func isSemanticTop1Changed(record Top1ChangeRecord) bool {
	if record.FloatCacheLayer != "semantic" && record.Int8CacheLayer != "semantic" {
		return false
	}
	return record.CandidatePromptChanged || record.CandidateAnswerChanged || record.FloatCacheLayer != record.Int8CacheLayer
}

func classifyTop1Change(record Top1ChangeRecord) []string {
	causes := map[string]struct{}{}
	if record.CandidatePromptChanged {
		causes["quantization_or_near_tie_prompt_ranking_change"] = struct{}{}
	}
	if !record.CandidatePromptChanged && record.CandidateAnswerChanged {
		causes["duplicate_or_near_duplicate_prompt_answer_variant"] = struct{}{}
	}
	if record.CandidateAnswerChanged && record.Int8CandidateAnswerLength < record.FloatCandidateAnswerLength && record.ScoreDeltaChange < 0 {
		causes["shorter_cached_answer_scored_lower"] = struct{}{}
	}
	if !record.CandidateAnswerChanged && record.ScoreDeltaChange != 0 {
		causes["teacher_score_variance"] = struct{}{}
	}
	if math.Abs(record.SimilarityDelta) > 0 && math.Abs(record.SimilarityDelta) < 0.001 && (record.CandidatePromptChanged || record.CandidateAnswerChanged) {
		causes["tiny_similarity_delta_near_tie"] = struct{}{}
	}
	if record.Category == "howto_guidance" || record.Category == "rewrite_polish" || record.Category == "faq_generalization" {
		if record.CandidatePromptChanged || record.CandidateAnswerChanged {
			causes["dataset_same_category_samples_are_close"] = struct{}{}
		}
	}
	out := make([]string, 0, len(causes))
	for cause := range causes {
		out = append(out, cause)
	}
	sort.Strings(out)
	return out
}

func averageFloat64(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var sum float64
	for _, value := range values {
		sum += value
	}
	return sum / float64(len(values))
}

func percentileFloat64(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	index := int(math.Ceil(p*float64(len(sorted)))) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}

func maxFloat64(values []float64) float64 {
	var maxValue float64
	for _, value := range values {
		if value > maxValue {
			maxValue = value
		}
	}
	return maxValue
}

func topRiskyTop1Examples(records []Top1ChangeRecord, limit int) []Top1ChangeRecord {
	filtered := make([]Top1ChangeRecord, 0)
	for _, record := range records {
		if record.Int8BadHit || record.SafeHitLost || record.ScoreDeltaChange < 0 || record.CandidateAnswerChanged || record.CandidatePromptChanged {
			filtered = append(filtered, record)
		}
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		left := top1RiskScore(filtered[i])
		right := top1RiskScore(filtered[j])
		if left == right {
			return filtered[i].SampleID < filtered[j].SampleID
		}
		return left > right
	})
	if limit > 0 && len(filtered) > limit {
		return filtered[:limit]
	}
	return filtered
}

func top1RiskScore(record Top1ChangeRecord) float64 {
	var score float64
	if record.Int8BadHit && !record.FloatBadHit {
		score += 100
	}
	if record.SafeHitLost {
		score += 50
	}
	if record.ScoreDeltaChange < 0 {
		score += -record.ScoreDeltaChange * 10
	}
	if record.CandidateAnswerChanged {
		score += 5
	}
	if record.CandidatePromptChanged {
		score += 5
	}
	return score
}

func renderTop1DiagnosticsMarkdown(report Top1DiagnosticsReport) string {
	var b strings.Builder
	b.WriteString("# Top1 Change Diagnostics\n\n")
	b.WriteString("## Summary\n")
	b.WriteString(fmt.Sprintf("- total aligned samples: `%d`\n", report.TotalAlignedSamples))
	b.WriteString(fmt.Sprintf("- float decision log total: `%d`\n", report.FloatDecisionLogTotal))
	b.WriteString(fmt.Sprintf("- int8 decision log total: `%d`\n", report.Int8DecisionLogTotal))
	b.WriteString(fmt.Sprintf("- request_log consistent: `%t`\n", report.RequestLogConsistent))
	b.WriteString(fmt.Sprintf("- cache layer changed count: `%d`\n", report.CacheLayerChangedCount))
	b.WriteString(fmt.Sprintf("- semantic top1 changed count: `%d`\n", report.SemanticTop1ChangedCount))
	b.WriteString(fmt.Sprintf("- answer changed count: `%d`\n", report.AnswerChangedCount))
	b.WriteString(fmt.Sprintf("- similarity delta avg: `%.6f`\n", report.SimilarityDeltaAvg))
	b.WriteString(fmt.Sprintf("- similarity delta p95: `%.6f`\n", report.SimilarityDeltaP95))
	b.WriteString(fmt.Sprintf("- similarity delta max: `%.6f`\n", report.SimilarityDeltaMax))
	b.WriteString(fmt.Sprintf("- score worsened count: `%d`\n", report.ScoreWorsenedCount))
	b.WriteString(fmt.Sprintf("- score improved count: `%d`\n", report.ScoreImprovedCount))
	b.WriteString(fmt.Sprintf("- bad hit newly introduced count: `%d`\n", report.BadHitNewlyIntroducedCount))
	b.WriteString(fmt.Sprintf("- safe hit lost count: `%d`\n", report.SafeHitLostCount))
	b.WriteString(fmt.Sprintf("- safe hit gained count: `%d`\n", report.SafeHitGainedCount))

	b.WriteString("\n## Changed Categories\n")
	writeTop1CountMap(&b, report.TopChangedCategories)

	b.WriteString("\n## Top Risky Examples\n")
	if len(report.TopRiskyExamples) == 0 {
		b.WriteString("- none\n")
	} else {
		for _, record := range report.TopRiskyExamples {
			b.WriteString(fmt.Sprintf("- `%s` `%s` score_delta_change=`%.1f` prompt_changed=`%t` answer_changed=`%t` causes=`%s`\n",
				record.SampleID, record.Category, record.ScoreDeltaChange, record.CandidatePromptChanged, record.CandidateAnswerChanged, strings.Join(record.LikelyCauses, ",")))
		}
	}

	b.WriteString("\n## Focus Examples\n")
	if len(report.FocusExamples) == 0 {
		b.WriteString("- none\n")
	} else {
		keys := make([]string, 0, len(report.FocusExamples))
		for key := range report.FocusExamples {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, sampleID := range keys {
			b.WriteString(fmt.Sprintf("### `%s`\n", sampleID))
			for _, record := range report.FocusExamples[sampleID] {
				b.WriteString(fmt.Sprintf("- source_kind: `%s`; behavior: `%s`; layer: `%s -> %s`\n", record.SourceKind, record.ExpectedCacheBehavior, record.FloatCacheLayer, record.Int8CacheLayer))
				b.WriteString(fmt.Sprintf("- similarity: `%.6f -> %.6f` delta=`%.6f`\n", record.FloatSimilarity, record.Int8Similarity, record.SimilarityDelta))
				b.WriteString(fmt.Sprintf("- candidate prompt changed: `%t`; answer changed: `%t`\n", record.CandidatePromptChanged, record.CandidateAnswerChanged))
				b.WriteString(fmt.Sprintf("- candidate answer length: `%d -> %d`\n", record.FloatCandidateAnswerLength, record.Int8CandidateAnswerLength))
				b.WriteString(fmt.Sprintf("- cached score: `%.1f -> %.1f`; fresh score is compared independently by judge\n", record.FloatCachedScore, record.Int8CachedScore))
				if record.FloatCandidatePromptPreview != "" {
					b.WriteString(fmt.Sprintf("- float top1 prompt: %s\n", record.FloatCandidatePromptPreview))
				}
				if record.Int8CandidatePromptPreview != "" {
					b.WriteString(fmt.Sprintf("- int8 top1 prompt: %s\n", record.Int8CandidatePromptPreview))
				}
				if record.FloatCandidateAnswerPreview != "" {
					b.WriteString(fmt.Sprintf("- float answer preview: %s\n", record.FloatCandidateAnswerPreview))
				}
				if record.Int8CandidateAnswerPreview != "" {
					b.WriteString(fmt.Sprintf("- int8 answer preview: %s\n", record.Int8CandidateAnswerPreview))
				}
				if len(record.LikelyCauses) > 0 {
					b.WriteString(fmt.Sprintf("- likely causes: `%s`\n", strings.Join(record.LikelyCauses, "`, `")))
				}
				if record.Int8JudgeReason != "" {
					b.WriteString(fmt.Sprintf("- int8 judge reason: %s\n", record.Int8JudgeReason))
				}
			}
		}
	}

	b.WriteString("\n## TopK Diagnostics\n")
	b.WriteString(fmt.Sprintf("- topK available: `%t`\n", report.TopKAvailable))
	if report.TopKUnavailableReason != "" {
		b.WriteString(fmt.Sprintf("- reason: %s\n", report.TopKUnavailableReason))
	}

	if len(report.Warnings) > 0 {
		b.WriteString("\n## Warnings\n")
		for _, warning := range report.Warnings {
			b.WriteString(fmt.Sprintf("- `%s`: %s\n", warning.Kind, warning.Message))
		}
	}
	return b.String()
}

func writeTop1CountMap(b *strings.Builder, values map[string]int) {
	if len(values) == 0 {
		b.WriteString("- none\n")
		return
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		b.WriteString(fmt.Sprintf("- `%s`: `%d`\n", key, values[key]))
	}
}

func firstNonEmptyTop1(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
