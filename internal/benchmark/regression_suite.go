package benchmark

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	cachepkg "github.com/bianjiefilm/CacheSafety-Bench/internal/cache"
	"github.com/bianjiefilm/CacheSafety-Bench/internal/safety"
)

const (
	RegressionDirBadHits        = "bad_hits"
	RegressionDirUnknown        = "unknown"
	RegressionDirSafetyRefusals = "safety_refusals"
	RegressionDirGolden         = "golden"
)

type RegressionCase struct {
	CaseID                string  `json:"case_id"`
	SourceResultFile      string  `json:"source_result_file,omitempty"`
	SourceDecisionLog     string  `json:"source_decision_log,omitempty"`
	Category              string  `json:"category,omitempty"`
	ExpectedRisk          string  `json:"expected_risk,omitempty"`
	OldRequest            string  `json:"old_request,omitempty"`
	CachedAnswer          string  `json:"cached_answer,omitempty"`
	NewRequest            string  `json:"new_request,omitempty"`
	FreshAnswer           string  `json:"fresh_answer,omitempty"`
	Similarity            float64 `json:"similarity,omitempty"`
	Threshold             float64 `json:"threshold"`
	ExpectedCacheLayer    string  `json:"expected_cache_layer"`
	ExpectedRefusedReason string  `json:"expected_refused_reason,omitempty"`
	ExpectedBadHit        bool    `json:"expected_bad_hit"`
	ExpectedCacheSafe     bool    `json:"expected_cache_safe"`
	JudgeReason           string  `json:"judge_reason,omitempty"`
	JudgeError            string  `json:"judge_error,omitempty"`
	CreatedAt             string  `json:"created_at"`
}

type ExportRegressionOptions struct {
	OutputDir         string
	Mode              string
	SourceResultFile  string
	SourceDecisionLog string
	Now               func() time.Time
}

type ExportRegressionSummary struct {
	BadHits        int      `json:"bad_hits"`
	Unknown        int      `json:"unknown"`
	SafetyRefusals int      `json:"safety_refusals"`
	Written        int      `json:"written"`
	Skipped        int      `json:"skipped"`
	Files          []string `json:"files,omitempty"`
}

type ReplayOptions struct {
	RegressionDir string
	MaxCases      int
}

type ReplaySummary struct {
	Total           int                   `json:"total"`
	Passed          int                   `json:"passed"`
	Failed          int                   `json:"failed"`
	ByDir           map[string]ReplayStat `json:"by_dir"`
	FailureExamples []ReplayFailure       `json:"failure_examples,omitempty"`
}

type ReplayStat struct {
	Total  int `json:"total"`
	Passed int `json:"passed"`
	Failed int `json:"failed"`
}

type ReplayFailure struct {
	CaseID string `json:"case_id"`
	Dir    string `json:"dir"`
	Reason string `json:"reason"`
}

func ExportRegressionCases(records []DecisionRecord, opts ExportRegressionOptions) (ExportRegressionSummary, error) {
	if strings.TrimSpace(opts.OutputDir) == "" {
		opts.OutputDir = filepath.Join("testdata", "regression")
	}
	mode := strings.ToLower(strings.TrimSpace(opts.Mode))
	if mode == "" {
		mode = "all"
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	var summary ExportRegressionSummary
	datasetCache := map[string]map[string]Case{}
	for _, record := range records {
		dir, ok := regressionDirForDecision(record, mode)
		if !ok {
			continue
		}
		record = enrichDecisionRecordForRegression(record, datasetCache)
		item := regressionCaseFromDecision(record, dir, opts, now().UTC())
		targetDir := filepath.Join(opts.OutputDir, dir)
		if err := os.MkdirAll(targetDir, 0o755); err != nil {
			return summary, err
		}
		path := filepath.Join(targetDir, stableRegressionFilename(item))
		switch dir {
		case RegressionDirBadHits:
			summary.BadHits++
		case RegressionDirUnknown:
			summary.Unknown++
		case RegressionDirSafetyRefusals:
			summary.SafetyRefusals++
		}
		if existing, err := loadExistingRegressionCase(path); err == nil {
			if regressionCaseHasReplayFields(existing) {
				summary.Skipped++
				continue
			}
		} else if !os.IsNotExist(err) {
			return summary, err
		}
		payload, err := json.MarshalIndent(item, "", "  ")
		if err != nil {
			return summary, err
		}
		payload = append(payload, '\n')
		if err := os.WriteFile(path, payload, 0o644); err != nil {
			return summary, err
		}
		summary.Written++
		summary.Files = append(summary.Files, path)
	}
	sort.Strings(summary.Files)
	return summary, nil
}

func ReplayRegressionCases(opts ReplayOptions) (ReplaySummary, error) {
	if strings.TrimSpace(opts.RegressionDir) == "" {
		opts.RegressionDir = filepath.Join("testdata", "regression")
	}
	cases, err := LoadRegressionCases(opts.RegressionDir, opts.MaxCases)
	if err != nil {
		return ReplaySummary{}, err
	}
	summary := ReplaySummary{ByDir: map[string]ReplayStat{}}
	checker := safety.DefaultChecker()
	for _, loaded := range cases {
		summary.Total++
		dir := loaded.Dir
		stat := summary.ByDir[dir]
		stat.Total++
		pass, reason := replayRegressionCase(checker, loaded.Case, dir)
		if pass {
			summary.Passed++
			stat.Passed++
		} else {
			summary.Failed++
			stat.Failed++
			if len(summary.FailureExamples) < 10 {
				summary.FailureExamples = append(summary.FailureExamples, ReplayFailure{
					CaseID: loaded.Case.CaseID,
					Dir:    dir,
					Reason: reason,
				})
			}
		}
		summary.ByDir[dir] = stat
	}
	return summary, nil
}

type LoadedRegressionCase struct {
	Dir  string
	Path string
	Case RegressionCase
}

func LoadRegressionCases(root string, maxCases int) ([]LoadedRegressionCase, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	var paths []string
	if !info.IsDir() {
		paths = append(paths, root)
	} else {
		if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(path))
			if ext == ".json" || ext == ".jsonl" {
				paths = append(paths, path)
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	sort.Strings(paths)
	out := make([]LoadedRegressionCase, 0, len(paths))
	for _, path := range paths {
		dir := regressionParentDir(path)
		loaded, err := loadRegressionFile(path, dir)
		if err != nil {
			return nil, err
		}
		out = append(out, loaded...)
		if maxCases > 0 && len(out) >= maxCases {
			return out[:maxCases], nil
		}
	}
	return out, nil
}

func loadRegressionFile(path string, dir string) ([]LoadedRegressionCase, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	payload = []byte(strings.TrimSpace(string(payload)))
	if len(payload) == 0 {
		return nil, nil
	}
	var single RegressionCase
	if err := json.Unmarshal(payload, &single); err == nil && single.CaseID != "" {
		return []LoadedRegressionCase{{Dir: dir, Path: path, Case: single}}, nil
	}
	lines := strings.Split(string(payload), "\n")
	out := make([]LoadedRegressionCase, 0, len(lines))
	for lineNo, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var item RegressionCase
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			return nil, fmt.Errorf("decode regression %s line %d: %w", path, lineNo+1, err)
		}
		if item.CaseID == "" {
			continue
		}
		out = append(out, LoadedRegressionCase{Dir: dir, Path: path, Case: item})
	}
	return out, nil
}

func regressionDirForDecision(record DecisionRecord, mode string) (string, bool) {
	if record.BadHit && modeMatches(mode, "bad_hits") {
		return RegressionDirBadHits, true
	}
	if record.JudgeError != "" && modeMatches(mode, "unknown") {
		return RegressionDirUnknown, true
	}
	if !record.SafetyAllowed && record.RefusedReason != "" && modeMatches(mode, "safety_refusals") {
		return RegressionDirSafetyRefusals, true
	}
	return "", false
}

func modeMatches(mode string, target string) bool {
	return mode == "all" || mode == target
}

func regressionCaseFromDecision(record DecisionRecord, dir string, opts ExportRegressionOptions, createdAt time.Time) RegressionCase {
	expectedReason := record.RefusedDetail
	if expectedReason == "" {
		expectedReason = record.RefusedReason
	}
	cacheSafe := false
	if record.JudgeCacheSafe != nil {
		cacheSafe = *record.JudgeCacheSafe
	}
	caseID := stableCaseID(record, dir, expectedReason)
	return RegressionCase{
		CaseID:                caseID,
		SourceResultFile:      baseOrEmpty(opts.SourceResultFile),
		SourceDecisionLog:     baseOrEmpty(opts.SourceDecisionLog),
		Category:              record.Category,
		ExpectedRisk:          record.ExpectedRisk,
		OldRequest:            record.CandidatePromptPreview,
		CachedAnswer:          record.CandidateAnswerPreview,
		NewRequest:            record.RequestPreview,
		FreshAnswer:           record.FreshAnswerPreview,
		Similarity:            record.Similarity,
		Threshold:             record.Threshold,
		ExpectedCacheLayer:    record.CacheLayer,
		ExpectedRefusedReason: expectedReason,
		ExpectedBadHit:        record.BadHit,
		ExpectedCacheSafe:     cacheSafe,
		JudgeReason:           record.JudgeReason,
		JudgeError:            record.JudgeError,
		CreatedAt:             createdAt.Format(time.RFC3339),
	}
}

func enrichDecisionRecordForRegression(record DecisionRecord, datasetCache map[string]map[string]Case) DecisionRecord {
	if record.RequestPreview != "" && record.FreshAnswerPreview != "" {
		return record
	}
	if strings.TrimSpace(record.Dataset) == "" || strings.TrimSpace(record.SampleID) == "" {
		return record
	}
	casesByID, ok := datasetCache[record.Dataset]
	if !ok {
		casesByID = loadDatasetCasesByID(record.Dataset)
		datasetCache[record.Dataset] = casesByID
	}
	item, ok := casesByID[record.SampleID]
	if !ok {
		return record
	}
	if record.RequestPreview == "" {
		record.RequestPreview = Preview(cachepkg.PromptText(item.Request), 200)
	}
	if record.FreshAnswerPreview == "" {
		record.FreshAnswerPreview = Preview(item.FreshAnswer, 200)
	}
	if record.ExpectedRisk == "" {
		record.ExpectedRisk = item.ExpectedRisk
	}
	if record.Category == "" {
		record.Category = caseCategory(item)
	}
	return record
}

func loadDatasetCasesByID(dataset string) map[string]Case {
	out := map[string]Case{}
	for _, path := range candidateDatasetPaths(dataset) {
		cases, err := LoadJSONL(path)
		if err != nil {
			continue
		}
		for _, item := range cases {
			out[item.ID] = item
		}
		return out
	}
	return out
}

func candidateDatasetPaths(dataset string) []string {
	if strings.TrimSpace(dataset) == "" {
		return nil
	}
	return []string{
		dataset,
		filepath.Join("testdata", "benchmark", dataset),
		filepath.Join("..", "..", "testdata", "benchmark", dataset),
	}
}

func loadExistingRegressionCase(path string) (RegressionCase, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return RegressionCase{}, err
	}
	var item RegressionCase
	if err := json.Unmarshal(payload, &item); err != nil {
		return RegressionCase{}, err
	}
	return item, nil
}

func regressionCaseHasReplayFields(item RegressionCase) bool {
	if strings.TrimSpace(item.NewRequest) == "" {
		return false
	}
	if item.ExpectedRefusedReason != "" && strings.TrimSpace(item.OldRequest) == "" {
		return false
	}
	return true
}

func baseOrEmpty(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	return filepath.Base(path)
}

func replayRegressionCase(checker safety.Checker, item RegressionCase, dir string) (bool, string) {
	decision := checker.Evaluate(safety.Input{
		Prompt:             item.NewRequest,
		CachedPrompt:       item.OldRequest,
		CachedTeacherScore: 10,
	})
	if item.ExpectedRefusedReason != "" {
		if decision.Allowed {
			return false, "expected refusal but current safety allowed semantic cache"
		}
		if !refusalMatches(decision, item.ExpectedRefusedReason) {
			return false, fmt.Sprintf("refusal mismatch: got %q/%q want %q", decision.Rule, decision.Detail, item.ExpectedRefusedReason)
		}
		return true, ""
	}
	if item.ExpectedBadHit {
		if decision.Allowed {
			return false, "historical bad hit is still allowed by deterministic safety"
		}
		return true, ""
	}
	if !item.ExpectedCacheSafe || dir == RegressionDirUnknown {
		if decision.Allowed {
			return false, "unsafe or unknown case would be allowed as cache-safe"
		}
		return true, ""
	}
	if !decision.Allowed {
		return false, fmt.Sprintf("golden safe case is now refused: %s %s", decision.Rule, decision.Detail)
	}
	return true, ""
}

func refusalMatches(decision safety.Decision, expected string) bool {
	expected = strings.TrimSpace(expected)
	if expected == "" {
		return true
	}
	rule := string(decision.Rule)
	return rule == expected ||
		decision.Detail == expected ||
		strings.HasPrefix(decision.Detail, expected) ||
		strings.HasPrefix(expected, rule)
}

func stableCaseID(record DecisionRecord, dir string, reason string) string {
	parts := []string{record.Category, record.SampleID}
	if reason != "" {
		parts = append(parts, reason)
	} else {
		parts = append(parts, dir)
	}
	return sanitizeRegressionName(strings.Join(parts, "_"))
}

func stableRegressionFilename(item RegressionCase) string {
	return sanitizeRegressionName(item.CaseID) + ".json"
}

func sanitizeRegressionName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastUnderscore := false
	for _, r := range value {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "regression_case"
	}
	return out
}

func regressionParentDir(path string) string {
	dir := filepath.Base(filepath.Dir(path))
	switch dir {
	case RegressionDirBadHits, RegressionDirUnknown, RegressionDirSafetyRefusals, RegressionDirGolden:
		return dir
	default:
		return "root"
	}
}
