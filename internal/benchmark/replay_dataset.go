package benchmark

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	cachepkg "github.com/bianjiefilm/CacheSafety-Bench/internal/cache"
)

type ReplayDatasetCase struct {
	ID                    string           `json:"id"`
	Category              string           `json:"category,omitempty"`
	ExpectedRisk          string           `json:"expected_risk,omitempty"`
	Request               cachepkg.Request `json:"request"`
	FreshAnswer           string           `json:"fresh_answer,omitempty"`
	TeacherScore          float64          `json:"teacher_score,omitempty"`
	FreshCost             float64          `json:"fresh_cost,omitempty"`
	CachedCost            float64          `json:"cached_cost,omitempty"`
	EstimatedUpstreamCost float64          `json:"estimated_upstream_cost,omitempty"`
	OldRequest            string           `json:"old_request,omitempty"`
	OldAnswer             string           `json:"old_answer,omitempty"`
	ExpectedCacheBehavior string           `json:"expected_cache_behavior,omitempty"`
	ExpectedRefusedReason string           `json:"expected_refused_reason,omitempty"`
	ExpectedBadHit        bool             `json:"expected_bad_hit"`
	SourceRunID           string           `json:"source_run_id,omitempty"`
	SourceCacheLayer      string           `json:"source_cache_layer,omitempty"`
	SourceSimilarity      float64          `json:"source_similarity,omitempty"`
	SourceJudgeCacheSafe  *bool            `json:"source_judge_cache_safe,omitempty"`
	SourceSafeCostSaved   float64          `json:"source_safe_cost_saved,omitempty"`
	ReplayQuality         string           `json:"replay_quality,omitempty"`
}

type ReplayDatasetOptions struct {
	RunID       string
	Include     map[string]bool
	Category    string
	MaxSamples  int
	OutputPath  string
	DecisionLog string
}

type ReplayDatasetExportSummary struct {
	Total         int            `json:"total"`
	ByInclude     map[string]int `json:"by_include"`
	ReplayQuality map[string]int `json:"replay_quality"`
	Output        string         `json:"output,omitempty"`
}

type DatasetResolver struct {
	byID     map[string]Case
	byPrompt map[string]Case
}

func NewDatasetResolver(dataset string) DatasetResolver {
	byID := loadDatasetCasesByID(dataset)
	byPrompt := map[string]Case{}
	for _, item := range byID {
		byPrompt[cachepkg.PromptText(item.Request)] = item
	}
	return DatasetResolver{byID: byID, byPrompt: byPrompt}
}

func (r DatasetResolver) ResolveByID(sampleID string) (Case, bool) {
	item, ok := r.byID[sampleID]
	return item, ok
}

func (r DatasetResolver) ResolveByPrompt(prompt string) (Case, bool) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return Case{}, false
	}
	if item, ok := r.byPrompt[prompt]; ok {
		return item, true
	}
	var match Case
	found := false
	for full, item := range r.byPrompt {
		if strings.HasPrefix(full, prompt) || strings.HasPrefix(prompt, full) {
			if found {
				return Case{}, false
			}
			match = item
			found = true
		}
	}
	return match, found
}

func ExportReplayDatasetFromRequestLog(ctx context.Context, store *RequestLogStore, runID string, opts ReplayDatasetOptions) ([]ReplayDatasetCase, ReplayDatasetExportSummary, error) {
	run, err := store.GetRun(ctx, runID)
	if err != nil {
		return nil, ReplayDatasetExportSummary{}, err
	}
	rows, err := store.AllRequestLogs(ctx, runID)
	if err != nil {
		return nil, ReplayDatasetExportSummary{}, err
	}
	resolver := NewDatasetResolver(run.Dataset)
	cases := make([]ReplayDatasetCase, 0, len(rows))
	summary := ReplayDatasetExportSummary{ByInclude: map[string]int{}, ReplayQuality: map[string]int{}}
	for _, row := range rows {
		include := classifyReplayInclude(row)
		if !opts.Include[include] {
			continue
		}
		item := replayCaseFromRequestLog(row, run, resolver)
		if !matchesReplayFilters(item, opts) {
			continue
		}
		cases = append(cases, item)
		summary.ByInclude[include]++
		summary.ReplayQuality[item.ReplayQuality]++
		if opts.MaxSamples > 0 && len(cases) >= opts.MaxSamples {
			break
		}
	}
	summary.Total = len(cases)
	summary.Output = filepath.Base(opts.OutputPath)
	return cases, summary, nil
}

func ExportReplayDatasetFromDecisionLog(records []DecisionRecord, opts ReplayDatasetOptions) ([]ReplayDatasetCase, ReplayDatasetExportSummary, error) {
	grouped := map[string]DatasetResolver{}
	cases := make([]ReplayDatasetCase, 0, len(records))
	summary := ReplayDatasetExportSummary{ByInclude: map[string]int{}, ReplayQuality: map[string]int{}}
	for _, record := range records {
		include := classifyDecisionReplayInclude(record)
		if !opts.Include[include] {
			continue
		}
		resolver, ok := grouped[record.Dataset]
		if !ok {
			resolver = NewDatasetResolver(record.Dataset)
			grouped[record.Dataset] = resolver
		}
		item := replayCaseFromDecisionRecord(record, resolver)
		if !matchesReplayFilters(item, opts) {
			continue
		}
		cases = append(cases, item)
		summary.ByInclude[include]++
		summary.ReplayQuality[item.ReplayQuality]++
		if opts.MaxSamples > 0 && len(cases) >= opts.MaxSamples {
			break
		}
	}
	summary.Total = len(cases)
	summary.Output = filepath.Base(opts.OutputPath)
	return cases, summary, nil
}

func WriteReplayDataset(path string, cases []ReplayDatasetCase) error {
	if path == "" {
		return fmt.Errorf("output path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	encoder := json.NewEncoder(file)
	for _, item := range cases {
		if err := encoder.Encode(item); err != nil {
			return err
		}
	}
	return nil
}

func LoadReplayDataset(path string) ([]ReplayDatasetCase, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	out := []ReplayDatasetCase{}
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		var item ReplayDatasetCase
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil {
			return nil, fmt.Errorf("decode replay dataset line %d: %w", line, err)
		}
		out = append(out, item)
	}
	return out, scanner.Err()
}

func ParseReplayInclude(value string) map[string]bool {
	defaults := "semantic_safe,refusals,bad_hits,unknown"
	if strings.TrimSpace(value) == "" {
		value = defaults
	}
	out := map[string]bool{}
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(strings.ToLower(part))
		if part != "" {
			out[part] = true
		}
	}
	return out
}

func replayCaseFromRequestLog(row RequestLogRow, run BenchmarkRunRow, resolver DatasetResolver) ReplayDatasetCase {
	item := ReplayDatasetCase{
		ID:                    row.SampleID,
		Category:              row.Category,
		ExpectedRisk:          row.ExpectedRisk,
		FreshAnswer:           row.FreshAnswerPreview,
		TeacherScore:          10,
		FreshCost:             row.EstimatedUpstreamCost,
		CachedCost:            row.EstimatedUpstreamCost - row.SafeCostSaved,
		EstimatedUpstreamCost: row.EstimatedUpstreamCost,
		OldRequest:            row.CandidatePromptPreview,
		OldAnswer:             row.CandidateAnswerPreview,
		ExpectedCacheBehavior: expectedCacheBehavior(row.CacheLayer, row.RefusedReason, row.BadHit, row.Unknown),
		ExpectedRefusedReason: expectedRefusedReason(row.RefusedReason, row.RefusedDetail),
		ExpectedBadHit:        row.BadHit,
		SourceRunID:           run.RunID,
		SourceCacheLayer:      row.CacheLayer,
		SourceSimilarity:      row.Similarity,
		SourceJudgeCacheSafe:  row.JudgeCacheSafe,
		SourceSafeCostSaved:   row.SafeCostSaved,
	}
	quality := "preview_only"
	if source, ok := resolver.ResolveByID(row.SampleID); ok {
		item.Request = source.Request
		item.FreshAnswer = source.FreshAnswer
		item.TeacherScore = source.TeacherScore
		item.FreshCost = source.FreshCost
		item.CachedCost = source.CachedCost
		if old, ok := resolver.ResolveByPrompt(row.CandidatePromptPreview); ok {
			item.OldRequest = cachepkg.PromptText(old.Request)
			item.OldAnswer = old.FreshAnswer
			quality = "full_from_dataset"
		}
	}
	if len(item.Request.Messages) == 0 {
		item.Request = replayRequest(row.RequestPreview)
	}
	if item.OldRequest == "" {
		item.OldRequest = row.CandidatePromptPreview
	}
	if item.OldAnswer == "" {
		item.OldAnswer = row.CandidateAnswerPreview
	}
	item.ReplayQuality = quality
	return item
}

func replayCaseFromDecisionRecord(record DecisionRecord, resolver DatasetResolver) ReplayDatasetCase {
	item := ReplayDatasetCase{
		ID:                    record.SampleID,
		Category:              record.Category,
		ExpectedRisk:          record.ExpectedRisk,
		FreshAnswer:           record.FreshAnswerPreview,
		TeacherScore:          10,
		FreshCost:             record.EstimatedUpstreamCost,
		CachedCost:            record.EstimatedUpstreamCost - record.SafeCostSaved,
		EstimatedUpstreamCost: record.EstimatedUpstreamCost,
		OldRequest:            record.CandidatePromptPreview,
		OldAnswer:             record.CandidateAnswerPreview,
		ExpectedCacheBehavior: expectedCacheBehavior(record.CacheLayer, record.RefusedReason, record.BadHit, record.JudgeError != ""),
		ExpectedRefusedReason: expectedRefusedReason(record.RefusedReason, record.RefusedDetail),
		ExpectedBadHit:        record.BadHit,
		SourceCacheLayer:      record.CacheLayer,
		SourceSimilarity:      record.Similarity,
		SourceJudgeCacheSafe:  record.JudgeCacheSafe,
		SourceSafeCostSaved:   record.SafeCostSaved,
	}
	quality := "preview_only"
	if source, ok := resolver.ResolveByID(record.SampleID); ok {
		item.Request = source.Request
		item.FreshAnswer = source.FreshAnswer
		item.TeacherScore = source.TeacherScore
		item.FreshCost = source.FreshCost
		item.CachedCost = source.CachedCost
		if old, ok := resolver.ResolveByPrompt(record.CandidatePromptPreview); ok {
			item.OldRequest = cachepkg.PromptText(old.Request)
			item.OldAnswer = old.FreshAnswer
			quality = "full_from_dataset"
		}
	}
	if len(item.Request.Messages) == 0 {
		item.Request = replayRequest(record.RequestPreview)
	}
	if item.OldRequest == "" {
		item.OldRequest = record.CandidatePromptPreview
	}
	if item.OldAnswer == "" {
		item.OldAnswer = record.CandidateAnswerPreview
	}
	item.ReplayQuality = quality
	return item
}

func replayRequest(prompt string) cachepkg.Request {
	return cachepkg.Request{
		Model:    "benchmark-provider-model",
		Messages: []cachepkg.Message{{Role: "user", Content: strings.TrimSpace(prompt)}},
	}
}

func classifyReplayInclude(row RequestLogRow) string {
	switch {
	case row.CacheLayer == "semantic" && row.JudgeCacheSafe != nil && *row.JudgeCacheSafe:
		return "semantic_safe"
	case !row.SafetyAllowed || row.RefusedReason != "":
		return "refusals"
	case row.BadHit:
		return "bad_hits"
	case row.Unknown || row.JudgeError != "":
		return "unknown"
	case row.CacheLayer == "miss":
		return "miss"
	default:
		return ""
	}
}

func classifyDecisionReplayInclude(record DecisionRecord) string {
	switch {
	case record.CacheLayer == "semantic" && record.JudgeCacheSafe != nil && *record.JudgeCacheSafe:
		return "semantic_safe"
	case !record.SafetyAllowed || record.RefusedReason != "":
		return "refusals"
	case record.BadHit:
		return "bad_hits"
	case record.JudgeError != "":
		return "unknown"
	case record.CacheLayer == "miss":
		return "miss"
	default:
		return ""
	}
}

func expectedCacheBehavior(cacheLayer, refusedReason string, badHit, unknown bool) string {
	switch {
	case refusedReason != "":
		return "refusal"
	case badHit:
		return "bad_hit"
	case unknown:
		return "unknown"
	case cacheLayer == "semantic":
		return "semantic_safe"
	case cacheLayer == "miss":
		return "miss"
	default:
		return cacheLayer
	}
}

func expectedRefusedReason(reason, detail string) string {
	if detail != "" {
		return detail
	}
	return reason
}

func matchesReplayFilters(item ReplayDatasetCase, opts ReplayDatasetOptions) bool {
	if category := strings.TrimSpace(opts.Category); category != "" && item.Category != category {
		return false
	}
	return true
}

func SortReplayCases(cases []ReplayDatasetCase) {
	sort.SliceStable(cases, func(i, j int) bool {
		return cases[i].ID < cases[j].ID
	})
}
