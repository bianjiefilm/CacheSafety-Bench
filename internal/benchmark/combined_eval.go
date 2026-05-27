package benchmark

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	cachepkg "github.com/bianjiefilm/CacheSafety-Bench/internal/cache"
	"github.com/bianjiefilm/CacheSafety-Bench/internal/safety"
)

type BuildCombinedEvalOptions struct {
	Sources           []string
	OutputPath        string
	Dedupe            bool
	MaxPerCategory    int
	IncludeRegression bool
	IncludeBoundaryV0 bool
	Seed              int64
}

type CombinedEvalReport struct {
	Total                      int            `json:"total"`
	ByCategory                 map[string]int `json:"by_category"`
	ByExpectedRisk             map[string]int `json:"by_expected_risk"`
	ByExpectedCacheBehavior    map[string]int `json:"by_expected_cache_behavior"`
	BySourceKind               map[string]int `json:"by_source_kind"`
	HardNegativeCount          int            `json:"hard_negative_count"`
	RegressionCount            int            `json:"regression_count"`
	BoundarySampleCount        int            `json:"boundary_sample_count"`
	MissingOldAnswerCount      int            `json:"missing_old_answer_count"`
	MissingOldRequestCount     int            `json:"missing_old_request_count"`
	DuplicateRemoved           int            `json:"duplicate_removed"`
	LegacyBadHitExamples       int            `json:"legacy_bad_hit_examples,omitempty"`
	LegacyBadHitConverted      int            `json:"legacy_bad_hit_converted,omitempty"`
	LegacyBadHitKeptAsExamples int            `json:"legacy_bad_hit_kept_as_examples,omitempty"`
}

type regressionCase struct {
	CaseID                string  `json:"case_id"`
	SourceResultFile      string  `json:"source_result_file"`
	SourceDecisionLog     string  `json:"source_decision_log"`
	Category              string  `json:"category"`
	ExpectedRisk          string  `json:"expected_risk"`
	OldRequest            string  `json:"old_request"`
	CachedAnswer          string  `json:"cached_answer"`
	NewRequest            string  `json:"new_request"`
	FreshAnswer           string  `json:"fresh_answer"`
	Similarity            float64 `json:"similarity"`
	Threshold             float64 `json:"threshold"`
	ExpectedCacheLayer    string  `json:"expected_cache_layer"`
	ExpectedRefusedReason string  `json:"expected_refused_reason"`
	ExpectedBadHit        bool    `json:"expected_bad_hit"`
	ExpectedCacheSafe     bool    `json:"expected_cache_safe"`
	JudgeReason           string  `json:"judge_reason"`
}

func BuildCombinedEval(opts BuildCombinedEvalOptions) ([]Case, CombinedEvalReport, error) {
	if len(opts.Sources) == 0 {
		return nil, CombinedEvalReport{}, fmt.Errorf("sources are required")
	}
	if strings.TrimSpace(opts.OutputPath) == "" {
		return nil, CombinedEvalReport{}, fmt.Errorf("output path is required")
	}
	expanded, err := expandSourcePaths(opts.Sources)
	if err != nil {
		return nil, CombinedEvalReport{}, err
	}
	report := CombinedEvalReport{
		ByCategory:              map[string]int{},
		ByExpectedRisk:          map[string]int{},
		ByExpectedCacheBehavior: map[string]int{},
		BySourceKind:            map[string]int{},
	}
	var merged []Case
	indexByKey := map[string]int{}
	for _, path := range expanded {
		loaded, kind, err := loadCombinedSource(path)
		if err != nil {
			return nil, report, err
		}
		if strings.Contains(filepath.ToSlash(path), "/bad_hits/") && strings.HasSuffix(path, ".jsonl") {
			total := countLegacyBadHitLines(path)
			report.LegacyBadHitExamples += total
			report.LegacyBadHitConverted += len(loaded)
			report.LegacyBadHitKeptAsExamples += total - len(loaded)
		}
		for _, item := range loaded {
			if item.SourceFile == "" {
				item.SourceFile = filepath.Base(path)
			}
			if item.SourceKind == "" || item.SourceKind == "dataset" {
				item.SourceKind = kind
			}
			if item.ExpectedCacheBehavior == "refusal" {
				item.ExpectedCacheBehavior = "expected_refusal"
			}
			if item.SourceSampleID == "" {
				item.SourceSampleID = item.ID
			}
			if !opts.IncludeRegression && kind == "regression" {
				continue
			}
			if opts.Dedupe {
				key := combinedDedupeKey(item)
				if pos, ok := indexByKey[key]; ok {
					if preferCombinedCandidate(item, merged[pos]) {
						merged[pos] = item
					}
					report.DuplicateRemoved++
					continue
				}
				indexByKey[key] = len(merged)
			}
			merged = append(merged, item)
		}
	}
	if opts.IncludeBoundaryV0 {
		for _, item := range BoundarySamplesV0() {
			item.SourceFile = "boundary_samples_v0"
			item.SourceKind = "boundary"
			if item.SourceSampleID == "" {
				item.SourceSampleID = item.ID
			}
			if opts.Dedupe {
				key := combinedDedupeKey(item)
				if pos, ok := indexByKey[key]; ok {
					if preferCombinedCandidate(item, merged[pos]) {
						merged[pos] = item
					}
					report.DuplicateRemoved++
					continue
				}
				indexByKey[key] = len(merged)
			}
			merged = append(merged, item)
		}
	}
	if opts.MaxPerCategory > 0 {
		filtered := make([]Case, 0, len(merged))
		counts := map[string]int{}
		for _, item := range merged {
			category := normalizedCategory(item.Category)
			if counts[category] >= opts.MaxPerCategory && item.SourceKind != "regression" {
				continue
			}
			counts[category]++
			filtered = append(filtered, item)
		}
		merged = filtered
	}
	sort.SliceStable(merged, func(i, j int) bool { return merged[i].ID < merged[j].ID })
	for _, item := range merged {
		report.Total++
		report.ByCategory[normalizedCategory(item.Category)]++
		report.ByExpectedRisk[defaultString(item.ExpectedRisk, "unspecified")]++
		report.ByExpectedCacheBehavior[defaultString(item.ExpectedCacheBehavior, "unspecified")]++
		report.BySourceKind[defaultString(item.SourceKind, "unknown")]++
		if normalizedCategory(item.Category) == "hard_negative" {
			report.HardNegativeCount++
		}
		if item.SourceKind == "regression" {
			report.RegressionCount++
		}
		if item.SourceKind == "boundary" {
			report.BoundarySampleCount++
		}
		if strings.TrimSpace(item.OldAnswer) == "" {
			report.MissingOldAnswerCount++
		}
		if strings.TrimSpace(item.OldRequest) == "" {
			report.MissingOldRequestCount++
		}
	}
	return merged, report, nil
}

func WriteCombinedEvalReport(outputPath string, report CombinedEvalReport) (string, error) {
	reportPath := filepath.Join("results", strings.TrimSuffix(filepath.Base(outputPath), filepath.Ext(outputPath))+"_report.json")
	if err := os.MkdirAll(filepath.Dir(reportPath), 0o755); err != nil {
		return "", err
	}
	file, err := os.Create(reportPath)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		return "", err
	}
	return reportPath, nil
}

func WriteCombinedEvalMarkdownReport(outputPath string, report CombinedEvalReport) (string, error) {
	reportPath := filepath.Join("reports", strings.TrimSuffix(filepath.Base(outputPath), filepath.Ext(outputPath))+"_report.md")
	if err := os.MkdirAll(filepath.Dir(reportPath), 0o755); err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("# Combined Eval Dataset Report\n\n")
	b.WriteString(fmt.Sprintf("- total: `%d`\n", report.Total))
	b.WriteString(fmt.Sprintf("- hard_negative_count: `%d`\n", report.HardNegativeCount))
	b.WriteString(fmt.Sprintf("- regression_count: `%d`\n", report.RegressionCount))
	b.WriteString(fmt.Sprintf("- boundary_sample_count: `%d`\n", report.BoundarySampleCount))
	b.WriteString(fmt.Sprintf("- missing_old_request_count: `%d`\n", report.MissingOldRequestCount))
	b.WriteString(fmt.Sprintf("- missing_old_answer_count: `%d`\n", report.MissingOldAnswerCount))
	b.WriteString(fmt.Sprintf("- duplicate_removed: `%d`\n", report.DuplicateRemoved))
	if report.LegacyBadHitExamples > 0 {
		b.WriteString(fmt.Sprintf("- legacy_bad_hit_examples: `%d`\n", report.LegacyBadHitExamples))
		b.WriteString(fmt.Sprintf("- legacy_bad_hit_converted: `%d`\n", report.LegacyBadHitConverted))
		b.WriteString(fmt.Sprintf("- legacy_bad_hit_kept_as_examples: `%d`\n", report.LegacyBadHitKeptAsExamples))
	}
	writeReportMap(&b, "by_category", report.ByCategory)
	writeReportMap(&b, "by_expected_risk", report.ByExpectedRisk)
	writeReportMap(&b, "by_expected_cache_behavior", report.ByExpectedCacheBehavior)
	writeReportMap(&b, "by_source_kind", report.BySourceKind)
	if err := os.WriteFile(reportPath, []byte(b.String()), 0o644); err != nil {
		return "", err
	}
	return reportPath, nil
}

func expandSourcePaths(inputs []string) ([]string, error) {
	var out []string
	for _, raw := range inputs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		matches, err := filepath.Glob(raw)
		if err != nil {
			return nil, err
		}
		if len(matches) == 0 {
			out = append(out, raw)
			continue
		}
		sort.Strings(matches)
		out = append(out, matches...)
	}
	return out, nil
}

func loadCombinedSource(path string) ([]Case, string, error) {
	if strings.Contains(filepath.ToSlash(path), "/bad_hits/") && strings.HasSuffix(path, ".jsonl") {
		items, err := loadConvertibleLegacyBadHits(path)
		return items, "legacy_bad_hit", err
	}
	if strings.HasSuffix(path, ".json") {
		item, err := loadRegressionJSON(path)
		if err != nil {
			return nil, "", err
		}
		return []Case{item}, "regression", nil
	}
	items, err := LoadJSONL(path)
	if err != nil {
		return nil, "", err
	}
	kind := detectSourceKind(filepath.Base(path), items)
	if kind == "safe_paraphrase" {
		items = normalizeSafeParaphraseCases(items)
	}
	return items, kind, nil
}

type legacyBadHitExample struct {
	ID           string           `json:"id"`
	Category     string           `json:"category"`
	OldRequest   string           `json:"old_request"`
	NewRequest   string           `json:"new_request"`
	Request      cachepkg.Request `json:"request"`
	CachedAnswer string           `json:"cached_answer"`
	FreshAnswer  string           `json:"fresh_answer"`
	Similarity   float64          `json:"similarity"`
	JudgeReason  string           `json:"judge_reason"`
}

func loadConvertibleLegacyBadHits(path string) ([]Case, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	checker := safety.DefaultChecker()
	var out []Case
	for _, line := range strings.Split(strings.TrimSpace(string(bytes)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var item legacyBadHitExample
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			return nil, err
		}
		decision := checker.Evaluate(safety.Input{
			Prompt:       item.NewRequest,
			CachedPrompt: item.OldRequest,
		})
		if decision.Allowed {
			continue
		}
		reason := decision.Detail
		if reason == "" {
			reason = string(decision.Rule)
		}
		out = append(out, Case{
			ID:                    "legacy-" + item.ID,
			Kind:                  "legacy_bad_hit",
			Category:              defaultString(item.Category, "hard_negative"),
			ExpectedRisk:          "guard_refuse",
			Request:               item.Request,
			FreshAnswer:           item.FreshAnswer,
			FreshCost:             1,
			CachedCost:            0.1,
			EstimatedUpstreamCost: 1,
			OldRequest:            item.OldRequest,
			OldAnswer:             item.CachedAnswer,
			ExpectedCacheBehavior: "expected_refusal",
			ExpectedRefusedReason: reason,
			ExpectedBadHit:        true,
			SourceSampleID:        item.ID,
		})
	}
	return out, nil
}

func countLegacyBadHitLines(path string) int {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	var count int
	for _, line := range strings.Split(strings.TrimSpace(string(bytes)), "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}

func loadRegressionJSON(path string) (Case, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return Case{}, err
	}
	var reg regressionCase
	if err := json.Unmarshal(bytes, &reg); err != nil {
		return Case{}, err
	}
	behavior := "miss"
	if reg.ExpectedRefusedReason != "" || !reg.ExpectedCacheSafe {
		behavior = "expected_refusal"
	}
	risk := reg.ExpectedRisk
	if risk == "" {
		risk = "high"
	}
	return Case{
		ID:           reg.CaseID,
		Kind:         "regression",
		Category:     reg.Category,
		ExpectedRisk: risk,
		Request: cachepkg.Request{
			Model:    "benchmark-provider-model",
			Messages: []cachepkg.Message{{Role: "user", Content: strings.TrimSpace(reg.NewRequest)}},
		},
		FreshAnswer:           strings.TrimSpace(reg.FreshAnswer),
		FreshCost:             1,
		CachedCost:            0.1,
		EstimatedUpstreamCost: 1,
		OldRequest:            strings.TrimSpace(reg.OldRequest),
		OldAnswer:             strings.TrimSpace(reg.CachedAnswer),
		ExpectedCacheBehavior: behavior,
		ExpectedRefusedReason: strings.TrimSpace(reg.ExpectedRefusedReason),
		ExpectedBadHit:        reg.ExpectedBadHit,
		SourceSampleID:        reg.CaseID,
	}, nil
}

func combinedDedupeKey(item Case) string {
	return strings.Join([]string{
		normalizeCombinedText(item.OldRequest),
		normalizeCombinedText(cachepkg.PromptText(item.Request)),
		strings.ToLower(strings.TrimSpace(item.Category)),
		strings.ToLower(strings.TrimSpace(item.ExpectedCacheBehavior)),
	}, "||")
}

func normalizeCombinedText(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
}

func preferCombinedCandidate(next Case, current Case) bool {
	if next.SourceKind == "regression" && current.SourceKind != "regression" {
		return true
	}
	if next.SourceKind == "boundary" && current.SourceKind == "dataset" {
		return true
	}
	if next.SourceKind == "llm_paraphrase" && current.SourceKind == "rule_based" {
		return true
	}
	return false
}

func detectSourceKind(name string, items []Case) string {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "safe_paraphrase"):
		return "safe_paraphrase"
	case strings.Contains(lower, "llm"):
		return "llm_paraphrase"
	case strings.Contains(lower, "rule"):
		return "rule_based"
	default:
		if len(items) > 0 && items[0].PairGenerationMode != "" {
			return items[0].PairGenerationMode
		}
		return "dataset"
	}
}

func writeReportMap(b *strings.Builder, title string, values map[string]int) {
	b.WriteString("\n## " + title + "\n")
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

func defaultString(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func normalizeSafeParaphraseCases(items []Case) []Case {
	out := make([]Case, 0, len(items))
	checker := safety.DefaultChecker()
	var lastHardSeed *Case
	for _, item := range items {
		if item.Category == "hard_negative" && item.Kind == "seed" {
			clone := item
			lastHardSeed = &clone
			continue
		}
		if item.Category == "hard_negative" {
			if lastHardSeed != nil {
				item.OldRequest = cachepkg.PromptText(lastHardSeed.Request)
				item.OldAnswer = lastHardSeed.FreshAnswer
				item.ExpectedCacheBehavior = "expected_refusal"
				decision := checker.Evaluate(safety.Input{
					Prompt:       cachepkg.PromptText(item.Request),
					CachedPrompt: item.OldRequest,
				})
				if !decision.Allowed {
					item.ExpectedRefusedReason = strings.TrimSpace(defaultString(decision.Detail, string(decision.Rule)))
				}
			}
		}
		out = append(out, item)
	}
	return out
}
