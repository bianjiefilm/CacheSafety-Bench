package benchmark

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	cachepkg "github.com/bianjiefilm/CacheSafety-Bench/internal/cache"
)

type DecisionRecord struct {
	SampleID                    string                           `json:"sample_id"`
	Dataset                     string                           `json:"dataset,omitempty"`
	Category                    string                           `json:"category,omitempty"`
	ExpectedRisk                string                           `json:"expected_risk,omitempty"`
	SourceKind                  string                           `json:"source_kind,omitempty"`
	ExpectedCacheBehavior       string                           `json:"expected_cache_behavior,omitempty"`
	ModelAlias                  string                           `json:"model"`
	VectorStore                 string                           `json:"vector_store"`
	EmbeddingMode               string                           `json:"embedding_mode"`
	Threshold                   float64                          `json:"threshold"`
	CacheLayer                  string                           `json:"cache_layer"`
	RequestPreview              string                           `json:"request_preview,omitempty"`
	SemanticCandidateFound      bool                             `json:"semantic_candidate_found"`
	Similarity                  float64                          `json:"similarity,omitempty"`
	CandidatePromptPreview      string                           `json:"candidate_prompt_preview,omitempty"`
	CandidateAnswerPreview      string                           `json:"candidate_answer_preview,omitempty"`
	SemanticTopK                []cachepkg.SemanticTopKCandidate `json:"semantic_topk,omitempty"`
	FreshAnswerPreview          string                           `json:"fresh_answer_preview,omitempty"`
	SafetyAllowed               bool                             `json:"safety_allowed"`
	RefusedReason               string                           `json:"refused_reason,omitempty"`
	RefusedDetail               string                           `json:"refused_detail,omitempty"`
	JudgeMode                   string                           `json:"judge_mode"`
	JudgeCacheSafe              *bool                            `json:"judge_cache_safe,omitempty"`
	CachedScore                 float64                          `json:"cached_score,omitempty"`
	FreshScore                  float64                          `json:"fresh_score,omitempty"`
	JudgeDelta                  float64                          `json:"judge_delta,omitempty"`
	BadHit                      bool                             `json:"bad_hit"`
	JudgeReason                 string                           `json:"judge_reason,omitempty"`
	JudgeError                  string                           `json:"judge_error,omitempty"`
	EstimatedUpstreamCost       float64                          `json:"estimated_upstream_cost"`
	SafeCostSaved               float64                          `json:"safe_cost_saved"`
	PromptTokens                int                              `json:"prompt_tokens"`
	CompletionTokens            int                              `json:"completion_tokens"`
	TotalTokens                 int                              `json:"total_tokens"`
	CachedPromptTokensSaved     int                              `json:"cached_prompt_tokens_saved"`
	CachedCompletionTokensSaved int                              `json:"cached_completion_tokens_saved"`
	UpstreamCostUSD             float64                          `json:"upstream_cost_usd"`
	CachedCostUSDSaved          float64                          `json:"cached_cost_usd_saved"`
	EmbeddingCostUSD            float64                          `json:"embedding_cost_usd"`
	JudgeCostUSD                float64                          `json:"judge_cost_usd"`
	NetCostSavedUSD             float64                          `json:"net_cost_saved_usd"`
	GrossSavingRate             float64                          `json:"gross_saving_rate"`
	NetSavingRate               float64                          `json:"net_saving_rate"`
	TokenEstimationMode         string                           `json:"token_estimation_mode"`
	LatencyMS                   int64                            `json:"latency_ms,omitempty"`
}

type DecisionSummary struct {
	Total                   int                       `json:"total"`
	ByCacheLayer            map[string]int            `json:"by_cache_layer"`
	ByCategory              map[string]int            `json:"by_category"`
	BySourceKind            map[string]int            `json:"by_source_kind,omitempty"`
	ByExpectedCacheBehavior map[string]int            `json:"by_expected_cache_behavior,omitempty"`
	ByRefusedReason         map[string]int            `json:"by_refused_reason"`
	ByThreshold             map[string]int            `json:"by_threshold"`
	CategoryCoverage        map[string]CategoryMetric `json:"by_category_metrics,omitempty"`
	SemanticCandidates      int                       `json:"semantic_candidates"`
	JudgedSafe              int                       `json:"judged_safe"`
	BadHitCount             int                       `json:"bad_hit_count"`
	TopBadHitCategories     map[string]int            `json:"top_bad_hit_categories"`
	AvgSimilaritySafe       float64                   `json:"avg_similarity_safe"`
	AvgSimilarityRefused    float64                   `json:"avg_similarity_refused"`
	AvgSimilarityBad        float64                   `json:"avg_similarity_bad"`
	SafeCostSavedTotal      float64                   `json:"safe_cost_saved_total"`
}

func WriteDecisionLog(path string, records []DecisionRecord) error {
	if path == "" {
		return nil
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
	for _, record := range records {
		if err := encoder.Encode(record); err != nil {
			return err
		}
	}
	return nil
}

func LoadDecisionLog(path string) ([]DecisionRecord, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	var records []DecisionRecord
	line := 0
	for scanner.Scan() {
		line++
		if strings.TrimSpace(scanner.Text()) == "" {
			continue
		}
		var record DecisionRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return nil, fmt.Errorf("decode decision log line %d: %w", line, err)
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func AnalyzeDecisionRecords(records []DecisionRecord) DecisionSummary {
	summary := DecisionSummary{
		ByCacheLayer:            map[string]int{},
		ByCategory:              map[string]int{},
		BySourceKind:            map[string]int{},
		ByExpectedCacheBehavior: map[string]int{},
		ByRefusedReason:         map[string]int{},
		ByThreshold:             map[string]int{},
		TopBadHitCategories:     map[string]int{},
		CategoryCoverage:        map[string]CategoryMetric{},
	}
	var safeSimilarity, refusedSimilarity, badSimilarity float64
	var safeCount, refusedCount, badCount int
	for _, record := range records {
		summary.Total++
		summary.ByCacheLayer[record.CacheLayer]++
		summary.ByCategory[record.Category]++
		summary.BySourceKind[defaultString(record.SourceKind, "unknown")]++
		summary.ByExpectedCacheBehavior[defaultString(record.ExpectedCacheBehavior, "unspecified")]++
		categoryMetric := summary.CategoryCoverage[record.Category]
		categoryMetric.Total++
		if record.SemanticCandidateFound {
			categoryMetric.SemanticCandidates++
		}
		if record.RefusedReason != "" {
			summary.ByRefusedReason[record.RefusedReason]++
			categoryMetric.SemanticRejectedBySafety++
		}
		summary.ByThreshold[fmt.Sprintf("%.2f", record.Threshold)]++
		if record.SemanticCandidateFound {
			summary.SemanticCandidates++
		}
		if record.JudgeCacheSafe != nil && *record.JudgeCacheSafe && !record.BadHit {
			summary.JudgedSafe++
			categoryMetric.SemanticJudgedSafe++
			safeSimilarity += record.Similarity
			safeCount++
		}
		if record.RefusedReason != "" {
			refusedSimilarity += record.Similarity
			refusedCount++
		}
		if record.BadHit {
			summary.BadHitCount++
			summary.TopBadHitCategories[record.Category]++
			categoryMetric.badHits++
			badSimilarity += record.Similarity
			badCount++
		}
		summary.SafeCostSavedTotal += record.SafeCostSaved
		categoryMetric.SafeCostSaved += record.SafeCostSaved
		categoryMetric.CachedTokensSaved += record.CachedPromptTokensSaved + record.CachedCompletionTokensSaved
		categoryMetric.CachedCostUSDSaved += record.CachedCostUSDSaved
		categoryMetric.NetCostSavedUSD += record.NetCostSavedUSD
		summary.CategoryCoverage[record.Category] = categoryMetric
	}
	summary.AvgSimilaritySafe = average(safeSimilarity, safeCount)
	summary.AvgSimilarityRefused = average(refusedSimilarity, refusedCount)
	summary.AvgSimilarityBad = average(badSimilarity, badCount)
	for category, metric := range summary.CategoryCoverage {
		if metric.Total > 0 {
			metric.BadHitRate = float64(metric.badHits) / float64(metric.Total)
		}
		summary.CategoryCoverage[category] = metric
	}
	return summary
}

func NewDecisionRecord(input DecisionInput) DecisionRecord {
	candidateFound := string(input.Result.Layer) == "semantic" || input.Result.RefusedByRule != ""
	record := DecisionRecord{
		SampleID:                    input.Item.ID,
		Dataset:                     filepath.Base(input.Dataset),
		Category:                    caseCategory(input.Item),
		ExpectedRisk:                input.Item.ExpectedRisk,
		SourceKind:                  input.Item.SourceKind,
		ExpectedCacheBehavior:       input.Item.ExpectedCacheBehavior,
		ModelAlias:                  ModelAlias(input.Item.Request.Model),
		VectorStore:                 input.VectorStore,
		EmbeddingMode:               input.EmbeddingMode,
		Threshold:                   input.Threshold,
		CacheLayer:                  string(input.Result.Layer),
		RequestPreview:              Preview(cachepkg.PromptText(input.Item.Request), 200),
		SemanticCandidateFound:      candidateFound,
		Similarity:                  input.Result.Similarity,
		SafetyAllowed:               input.Result.RefusedByRule == "",
		RefusedReason:               string(input.Result.RefusedByRule),
		RefusedDetail:               input.Result.RefusalDetail,
		JudgeMode:                   input.JudgeMode,
		BadHit:                      input.BadHit,
		EstimatedUpstreamCost:       input.Item.FreshCost,
		SafeCostSaved:               input.SafeCostSaved,
		PromptTokens:                input.Cost.PromptTokens,
		CompletionTokens:            input.Cost.CompletionTokens,
		TotalTokens:                 input.Cost.TotalTokens,
		CachedPromptTokensSaved:     input.Cost.CachedPromptTokensSaved,
		CachedCompletionTokensSaved: input.Cost.CachedCompletionTokensSaved,
		UpstreamCostUSD:             input.Cost.UpstreamCostUSD,
		CachedCostUSDSaved:          input.Cost.CachedCostUSDSaved,
		EmbeddingCostUSD:            input.Cost.EmbeddingCostUSD,
		JudgeCostUSD:                input.Cost.JudgeCostUSD,
		NetCostSavedUSD:             input.Cost.NetCostSavedUSD,
		TokenEstimationMode:         input.Cost.TokenEstimationMode,
	}
	if record.UpstreamCostUSD > 0 {
		record.GrossSavingRate = record.CachedCostUSDSaved / record.UpstreamCostUSD
		record.NetSavingRate = record.NetCostSavedUSD / record.UpstreamCostUSD
	}
	if candidateFound {
		record.CandidatePromptPreview = Preview(input.Result.CachedPrompt, 200)
		answer := input.Result.CandidateAnswer
		if answer == "" {
			answer = input.Result.Response.Text
		}
		record.CandidateAnswerPreview = Preview(answer, 200)
		record.SemanticTopK = input.Result.SemanticTopK
	}
	if input.Evaluation.judged {
		record.FreshAnswerPreview = Preview(input.Evaluation.record.Input.FreshAnswer, 200)
		record.JudgeError = input.Evaluation.record.Error
		if record.JudgeError == "" {
			cacheSafe := !input.Evaluation.badHit
			record.JudgeCacheSafe = &cacheSafe
			record.CachedScore = input.Evaluation.record.Result.CachedScore
			record.FreshScore = input.Evaluation.record.Result.FreshScore
			record.JudgeDelta = input.Evaluation.record.Result.Delta
			record.JudgeReason = input.Evaluation.record.Result.Reason
		}
	} else if input.Result.UpstreamCalled {
		record.FreshAnswerPreview = Preview(input.Result.Response.Text, 200)
	}
	return record
}

type DecisionInput struct {
	Dataset       string
	Item          Case
	Result        cachepkg.LookupResult
	Evaluation    hitEvaluation
	Threshold     float64
	VectorStore   string
	EmbeddingMode string
	JudgeMode     string
	BadHit        bool
	SafeCostSaved float64
	Cost          TokenCostAccount
}

func Preview(value string, limit int) string {
	if limit <= 0 {
		limit = 200
	}
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit])
}

func ModelAlias(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	if strings.HasPrefix(model, "benchmark-") || strings.HasPrefix(model, "test-") {
		return model
	}
	sum := sha256.Sum256([]byte(model))
	return "model_" + hex.EncodeToString(sum[:])[:8]
}

func average(sum float64, count int) float64 {
	if count == 0 {
		return 0
	}
	return sum / float64(count)
}
