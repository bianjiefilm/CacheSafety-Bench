package benchmark

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	cachepkg "github.com/bianjiefilm/CacheSafety-Bench/internal/cache"
	"github.com/bianjiefilm/CacheSafety-Bench/internal/embedding"
	"github.com/bianjiefilm/CacheSafety-Bench/internal/safety"
	"github.com/bianjiefilm/CacheSafety-Bench/internal/teacher"
)

type FreshAnswerFunc func(context.Context, Case) (cachepkg.Response, error)

type Config struct {
	Seed              int64         `json:"seed"`
	SemanticThreshold float64       `json:"semantic_threshold"`
	SemanticRerank    string        `json:"semantic_rerank,omitempty"`
	TopKTrace         int           `json:"top_k_trace,omitempty"`
	Shuffle           bool          `json:"shuffle"`
	MaxSamples        int           `json:"max_samples,omitempty"`
	StartIndex        int           `json:"start_index,omitempty"`
	EndIndex          int           `json:"end_index,omitempty"`
	Stratified        bool          `json:"stratified,omitempty"`
	StratifyBy        []string      `json:"stratify_by,omitempty"`
	MaxPerStratum     int           `json:"max_per_stratum,omitempty"`
	MinPerStratum     int           `json:"min_per_stratum,omitempty"`
	ProgressLogPath   string        `json:"progress_log_path,omitempty"`
	CheckpointPath    string        `json:"checkpoint_path,omitempty"`
	TimeoutPerSample  time.Duration `json:"timeout_per_sample,omitempty"`
	ContinueOnTimeout bool          `json:"continue_on_timeout,omitempty"`
	OnlyReplayPairs   bool          `json:"only_replay_pairs,omitempty"`
	SampleIDs         []string      `json:"sample_ids,omitempty"`
	JudgeMode         string        `json:"judge_mode,omitempty"`
	EmbeddingMode     string        `json:"embedding_mode,omitempty"`
	EmbeddingModel    string        `json:"embedding_model,omitempty"`
	VectorStoreMode   string        `json:"vector_store,omitempty"`
	SQLitePath        string        `json:"sqlite_path,omitempty"`
	Dataset           string        `json:"dataset,omitempty"`
	Pricing           PricingConfig `json:"-"`
}

type Case struct {
	ID                    string           `json:"id"`
	Kind                  string           `json:"kind,omitempty"`
	Category              string           `json:"category,omitempty"`
	ExpectedRisk          string           `json:"expected_risk,omitempty"`
	Request               cachepkg.Request `json:"request"`
	SampleIndex           int              `json:"-"`
	FreshAnswer           string           `json:"fresh_answer"`
	TeacherScore          float64          `json:"teacher_score"`
	FreshCost             float64          `json:"fresh_cost"`
	CachedCost            float64          `json:"cached_cost"`
	EstimatedUpstreamCost float64          `json:"estimated_upstream_cost,omitempty"`
	SemanticGroup         string           `json:"semantic_group,omitempty"`
	SemanticSimilarity    float64          `json:"semantic_similarity,omitempty"`
	OldRequest            string           `json:"old_request,omitempty"`
	OldAnswer             string           `json:"old_answer,omitempty"`
	ExpectedCacheBehavior string           `json:"expected_cache_behavior,omitempty"`
	ExpectedRefusedReason string           `json:"expected_refused_reason,omitempty"`
	ExpectedBadHit        bool             `json:"expected_bad_hit,omitempty"`
	SourceRunID           string           `json:"source_run_id,omitempty"`
	SourceCacheLayer      string           `json:"source_cache_layer,omitempty"`
	SourceSimilarity      float64          `json:"source_similarity,omitempty"`
	SourceJudgeCacheSafe  *bool            `json:"source_judge_cache_safe,omitempty"`
	SourceSafeCostSaved   float64          `json:"source_safe_cost_saved,omitempty"`
	ReplayQuality         string           `json:"replay_quality,omitempty"`
	PairGenerationMode    string           `json:"pair_generation_mode,omitempty"`
	SourceSampleID        string           `json:"source_sample_id,omitempty"`
	ParaphraseNotes       string           `json:"paraphrase_notes,omitempty"`
	SourceFile            string           `json:"source_file,omitempty"`
	SourceKind            string           `json:"source_kind,omitempty"`
	TrapType              string           `json:"trap_type,omitempty"`
	GuardExpected         string           `json:"guard_expected,omitempty"`
	ModelAlias            string           `json:"model_alias,omitempty"`
	PromptTokens          int              `json:"prompt_tokens,omitempty"`
	CompletionTokens      int              `json:"completion_tokens,omitempty"`
	TotalTokens           int              `json:"total_tokens,omitempty"`
	RedactionApplied      bool             `json:"redaction_applied,omitempty"`
	FinishReason          string           `json:"finish_reason,omitempty"`
	ResponseQuality       string           `json:"response_quality,omitempty"`
	ExcludeFromReplay     bool             `json:"exclude_from_replay,omitempty"`
	ExcludeReason         string           `json:"exclude_reason,omitempty"`
}

type Metrics struct {
	Threshold                   float64                   `json:"threshold,omitempty"`
	Total                       int                       `json:"total"`
	HitRate                     float64                   `json:"hit_rate"`
	SafeHitRate                 float64                   `json:"safe_hit_rate"`
	BadHitRate                  float64                   `json:"bad_hit_rate"`
	SafeCostSaved               float64                   `json:"safe_cost_saved"`
	BlendedCostPerRequest       float64                   `json:"blended_cost_per_request"`
	LayerContribution           map[string]int            `json:"layer_contribution"`
	RefusedByRule               map[string]int            `json:"refused_by_rule"`
	RefusedByDetail             map[string]int            `json:"refused_by_detail,omitempty"`
	JudgeMode                   string                    `json:"judge_mode"`
	JudgedCount                 int                       `json:"judged_count"`
	JudgeErrorCount             int                       `json:"judge_error_count"`
	UnknownCount                int                       `json:"unknown_count"`
	CachedGEFreshMinus1Rate     float64                   `json:"cached_ge_fresh_minus_1_rate"`
	EmbeddingMode               string                    `json:"embedding_mode"`
	VectorStore                 string                    `json:"vector_store"`
	VectorDim                   int                       `json:"vector_dim,omitempty"`
	SQLitePath                  string                    `json:"sqlite_path,omitempty"`
	SemanticRerank              string                    `json:"semantic_rerank,omitempty"`
	TopKTrace                   int                       `json:"top_k_trace,omitempty"`
	SemanticCandidates          int                       `json:"semantic_candidates"`
	SemanticRejectedBySafety    int                       `json:"semantic_rejected_by_safety"`
	SemanticJudgedSafe          int                       `json:"semantic_judged_safe"`
	AvgSemanticSimilarity       float64                   `json:"avg_semantic_similarity"`
	BadHitExamplesCount         int                       `json:"bad_hit_examples_count"`
	TotalPromptTokens           int                       `json:"total_prompt_tokens"`
	TotalCompletionTokens       int                       `json:"total_completion_tokens"`
	TotalTokens                 int                       `json:"total_tokens"`
	CachedTokensSaved           int                       `json:"cached_tokens_saved"`
	CachedPromptTokensSaved     int                       `json:"cached_prompt_tokens_saved"`
	CachedCompletionTokensSaved int                       `json:"cached_completion_tokens_saved"`
	ExactSavedTokens            int                       `json:"exact_saved_tokens,omitempty"`
	SemanticSavedTokens         int                       `json:"semantic_saved_tokens,omitempty"`
	GrossCostUSD                float64                   `json:"gross_cost_usd"`
	CachedCostUSDSaved          float64                   `json:"cached_cost_usd_saved"`
	JudgeCostUSD                float64                   `json:"judge_cost_usd"`
	EmbeddingCostUSD            float64                   `json:"embedding_cost_usd"`
	NetCostSavedUSD             float64                   `json:"net_cost_saved_usd"`
	GrossSavingRate             float64                   `json:"gross_saving_rate"`
	NetSavingRate               float64                   `json:"net_saving_rate"`
	TokenEstimationMode         string                    `json:"token_estimation_mode"`
	CategoryMetrics             map[string]CategoryMetric `json:"category_metrics,omitempty"`
	SamplingMode                string                    `json:"sampling_mode,omitempty"`
	StratifyBy                  []string                  `json:"stratify_by,omitempty"`
	StrataCount                 int                       `json:"strata_count,omitempty"`
	SampledByStratum            map[string]int            `json:"sampled_by_stratum,omitempty"`
	BadHits                     []BadHitSample            `json:"bad_hits,omitempty"`
	JudgeRecords                []teacher.Record          `json:"-"`
	DecisionRecords             []DecisionRecord          `json:"-"`
}

type CategoryMetric struct {
	Total                    int     `json:"total"`
	SemanticCandidates       int     `json:"semantic_candidates"`
	SemanticJudgedSafe       int     `json:"semantic_judged_safe"`
	BadHitRate               float64 `json:"bad_hit_rate"`
	SafeCostSaved            float64 `json:"safe_cost_saved"`
	SemanticRejectedBySafety int     `json:"rejected_by_safety"`
	CachedTokensSaved        int     `json:"cached_tokens_saved"`
	CachedCostUSDSaved       float64 `json:"cached_cost_usd_saved"`
	NetCostSavedUSD          float64 `json:"net_cost_saved_usd"`
	badHits                  int
}

type BadHitSample struct {
	ID            string           `json:"id"`
	Category      string           `json:"category,omitempty"`
	OldRequest    string           `json:"old_request,omitempty"`
	NewRequest    string           `json:"new_request,omitempty"`
	Request       cachepkg.Request `json:"request"`
	CachedAnswer  string           `json:"cached_answer"`
	FreshAnswer   string           `json:"fresh_answer"`
	CacheLayer    string           `json:"cache_layer"`
	Similarity    float64          `json:"similarity,omitempty"`
	TeacherDelta  float64          `json:"teacher_delta"`
	JudgeReason   string           `json:"judge_reason,omitempty"`
	ReplayFixture Case             `json:"replay_fixture"`
}

type ThresholdScan struct {
	Results []Metrics `json:"results"`
}

type TeacherScoreRecord struct {
	ID          string  `json:"id"`
	Prompt      string  `json:"prompt"`
	CachedScore float64 `json:"cached_score"`
	FreshScore  float64 `json:"fresh_score"`
	Delta       float64 `json:"delta"`
}

func LoadJSONL(path string) ([]Case, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return DecodeJSONL(file)
}

func DecodeJSONL(r io.Reader) ([]Case, error) {
	scanner := bufio.NewScanner(r)
	var cases []Case
	line := 0
	for scanner.Scan() {
		line++
		if scanner.Text() == "" {
			continue
		}
		var item Case
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil {
			return nil, fmt.Errorf("decode jsonl line %d: %w", line, err)
		}
		item.SampleIndex = len(cases)
		normalizeCase(&item)
		cases = append(cases, item)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return cases, nil
}

func Run(ctx context.Context, cases []Case, cfg Config) (Metrics, error) {
	return RunWithFreshAnswer(ctx, cases, cfg, nil)
}

func RunWithFreshAnswer(ctx context.Context, cases []Case, cfg Config, fresh FreshAnswerFunc) (Metrics, error) {
	return RunWithOptions(ctx, cases, cfg, fresh, nil)
}

func RunWithOptions(ctx context.Context, cases []Case, cfg Config, fresh FreshAnswerFunc, judge teacher.Judge) (Metrics, error) {
	return RunWithEmbedding(ctx, cases, cfg, fresh, judge, nil)
}

func RunWithEmbedding(ctx context.Context, cases []Case, cfg Config, fresh FreshAnswerFunc, judge teacher.Judge, embedder embedding.Embedder) (Metrics, error) {
	if cfg.SemanticThreshold <= 0 {
		cfg.SemanticThreshold = 0.95
	}
	if fresh == nil {
		fresh = fixtureFreshAnswer
	}
	judgeMode := cfg.JudgeMode
	if judgeMode == "" {
		judgeMode = "fixture"
	}
	if judge == nil {
		judgeMode = "fixture"
	}
	embeddingMode := cfg.EmbeddingMode
	if embeddingMode == "" {
		embeddingMode = "fixture"
	}
	vectorStoreMode := strings.TrimSpace(cfg.VectorStoreMode)
	if vectorStoreMode == "" {
		vectorStoreMode = "memory"
	}
	progress, err := OpenProgressLogger(cfg.ProgressLogPath, cfg.CheckpointPath)
	if err != nil {
		return Metrics{}, err
	}
	defer func() { _ = progress.Close() }()
	selectedCases, sampling := selectCases(cases, cfg)
	ordered := append([]Case(nil), selectedCases...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	if cfg.Shuffle {
		rng := rand.New(rand.NewSource(cfg.Seed))
		rng.Shuffle(len(ordered), func(i, j int) {
			ordered[i], ordered[j] = ordered[j], ordered[i]
		})
	}

	byPrompt := map[string]Case{}
	for _, item := range ordered {
		byPrompt[cachepkg.PromptText(item.Request)] = item
	}
	vectorIndex, err := buildVectorIndex(ctx, ordered, cfg, embedder, vectorStoreMode, progress)
	if err != nil {
		return Metrics{}, err
	}
	defer func() { _ = vectorIndex.close() }()

	pipelineConfig := cachepkg.PipelineConfig{
		SemanticThreshold: cfg.SemanticThreshold,
		Similarity: func(a cachepkg.Request, b cachepkg.Request) float64 {
			if vectorIndex.enabled {
				left, okLeft := vectorIndex.vectors[cachepkg.PromptText(a)]
				right, okRight := vectorIndex.vectors[cachepkg.PromptText(b)]
				if !okLeft || !okRight {
					return 0
				}
				return cachepkg.CosineSimilarity(left, right)
			}
			left := byPrompt[cachepkg.PromptText(a)]
			right := byPrompt[cachepkg.PromptText(b)]
			if left.SemanticGroup != "" && left.SemanticGroup == right.SemanticGroup {
				if left.SemanticSimilarity > 0 {
					return left.SemanticSimilarity
				}
				if right.SemanticSimilarity > 0 {
					return right.SemanticSimilarity
				}
			}
			return 0
		},
		Safety: safety.DefaultChecker(),
	}
	if vectorIndex.store != nil {
		pipelineConfig.SemanticCandidate = vectorIndex.semanticCandidateFunc(cfg)
	}
	pipeline := cachepkg.NewPipeline(pipelineConfig)

	metrics := Metrics{
		Threshold: cfg.SemanticThreshold,
		LayerContribution: map[string]int{
			string(cachepkg.LayerExact):     0,
			string(cachepkg.LayerCanonical): 0,
			string(cachepkg.LayerSemantic):  0,
			string(cachepkg.LayerMiss):      0,
		},
		RefusedByRule:    make(map[string]int),
		RefusedByDetail:  make(map[string]int),
		CategoryMetrics:  make(map[string]CategoryMetric),
		JudgeMode:        judgeMode,
		EmbeddingMode:    embeddingMode,
		VectorStore:      vectorStoreMode,
		VectorDim:        vectorIndex.dim,
		SQLitePath:       sanitizedSQLitePath(cfg.SQLitePath),
		SemanticRerank:   normalizedSemanticRerank(cfg.SemanticRerank),
		TopKTrace:        cfg.TopKTrace,
		SamplingMode:     sampling.Mode,
		StratifyBy:       append([]string(nil), sampling.StratifyBy...),
		StrataCount:      sampling.StrataCount,
		SampledByStratum: sampling.SampledByStratum,
	}
	var safeHits, badHits, cachedGEFreshMinus1 int
	var totalCost float64
	var semanticSimilaritySum float64
	responsesByPrompt := map[string]cachepkg.Response{}
	seededReplayEntries := map[string]bool{}

	for _, item := range ordered {
		seedReq, seedResp, ok := replaySeed(item)
		if !ok {
			continue
		}
		key := cachepkg.PromptText(seedReq) + "\n" + seedResp.Text
		if seededReplayEntries[key] {
			continue
		}
		pipeline.Put(seedReq, seedResp)
		if err := vectorIndex.insertSeed(ctx, seedReq, seedResp, item); err != nil {
			return Metrics{}, err
		}
		seededReplayEntries[key] = true
	}

	for _, item := range ordered {
		current := item
		category := normalizedCategory(caseCategory(item))
		sampleCtx, cancelSample := context.WithCancel(ctx)
		if cfg.TimeoutPerSample > 0 {
			sampleCtx, cancelSample = context.WithTimeout(ctx, cfg.TimeoutPerSample)
		}
		cacheSearchStarted := time.Now()
		result, err := pipeline.Handle(sampleCtx, item.Request, func(callCtx context.Context, req cachepkg.Request) (cachepkg.Response, error) {
			providerStarted := time.Now()
			resp, providerErr := fresh(callCtx, current)
			progress.Log(current, ProgressStageProvider, time.Since(providerStarted), providerErr)
			return resp, providerErr
		})
		progress.Log(item, ProgressStageCacheSearch, time.Since(cacheSearchStarted), err)
		if err != nil {
			if isTimeoutError(err, sampleCtx) {
				progress.Log(item, ProgressStageTimeout, time.Since(cacheSearchStarted), err)
			} else {
				progress.Log(item, ProgressStageError, time.Since(cacheSearchStarted), err)
			}
			if isTimeoutError(err, sampleCtx) && cfg.ContinueOnTimeout {
				cancelSample()
				metrics.Total++
				categoryMetric := metrics.CategoryMetrics[category]
				categoryMetric.Total++
				metrics.LayerContribution[string(cachepkg.LayerMiss)]++
				metrics.UnknownCount++
				metrics.JudgeErrorCount++
				metrics.DecisionRecords = append(metrics.DecisionRecords, timeoutDecisionRecord(cfg, item, vectorStoreMode, embeddingMode, judgeMode, err))
				metrics.CategoryMetrics[category] = categoryMetric
				continue
			}
			cancelSample()
			return Metrics{}, err
		}
		if result.Layer == cachepkg.LayerSemantic || result.RefusedByRule != safety.RuleNone {
			progress.Log(item, ProgressStageSafety, 0, nil)
		}

		metrics.Total++
		categoryMetric := metrics.CategoryMetrics[category]
		categoryMetric.Total++
		metrics.LayerContribution[string(result.Layer)]++
		if result.RefusedByRule != safety.RuleNone {
			metrics.RefusedByRule[string(result.RefusedByRule)]++
			if result.RefusalDetail != "" {
				metrics.RefusedByDetail[result.RefusalDetail]++
			}
		}
		if result.Layer == cachepkg.LayerSemantic || result.RefusedByRule != safety.RuleNone {
			metrics.SemanticCandidates++
			categoryMetric.SemanticCandidates++
			semanticSimilaritySum += result.Similarity
		}
		if result.RefusedByRule != safety.RuleNone {
			metrics.SemanticRejectedBySafety++
			categoryMetric.SemanticRejectedBySafety++
		}

		if result.Layer == cachepkg.LayerMiss {
			totalCost += item.FreshCost
			cost := BuildTokenCostAccount(item, result, hitEvaluation{}, cfg.Pricing, false, embeddingMode)
			applyTokenCost(&metrics, &categoryMetric, cost)
			pipeline.Put(item.Request, result.Response)
			if err := vectorIndex.insert(ctx, item, result.Response); err != nil {
				cancelSample()
				return Metrics{}, err
			}
			responsesByPrompt[cachepkg.PromptText(item.Request)] = result.Response
			metrics.DecisionRecords = append(metrics.DecisionRecords, NewDecisionRecord(DecisionInput{
				Dataset:       cfg.Dataset,
				Item:          item,
				Result:        result,
				Threshold:     cfg.SemanticThreshold,
				VectorStore:   vectorStoreMode,
				EmbeddingMode: embeddingMode,
				JudgeMode:     judgeMode,
				Cost:          cost,
			}))
			metrics.CategoryMetrics[category] = categoryMetric
			progress.Log(item, ProgressStageWriteLog, time.Since(cacheSearchStarted), nil)
			progress.Log(item, ProgressStageDone, time.Since(cacheSearchStarted), nil)
			cancelSample()
			continue
		}

		totalCost += item.CachedCost
		freshAnswer := item.FreshAnswer
		if response, ok := responsesByPrompt[cachepkg.PromptText(item.Request)]; ok && response.Text != "" {
			freshAnswer = response.Text
		}
		judgeStarted := time.Now()
		evaluation, known := evaluateHit(sampleCtx, item, result, freshAnswer, judge)
		if evaluation.judged {
			progress.Log(item, ProgressStageJudge, time.Since(judgeStarted), evaluation.recordError())
		}
		if !known {
			metrics.UnknownCount++
			metrics.JudgeErrorCount++
			metrics.JudgeRecords = append(metrics.JudgeRecords, evaluation.record)
			cost := BuildTokenCostAccount(item, result, evaluation, cfg.Pricing, false, embeddingMode)
			applyTokenCost(&metrics, &categoryMetric, cost)
			metrics.DecisionRecords = append(metrics.DecisionRecords, NewDecisionRecord(DecisionInput{
				Dataset:       cfg.Dataset,
				Item:          item,
				Result:        result,
				Evaluation:    evaluation,
				Threshold:     cfg.SemanticThreshold,
				VectorStore:   vectorStoreMode,
				EmbeddingMode: embeddingMode,
				JudgeMode:     judgeMode,
				Cost:          cost,
			}))
			metrics.CategoryMetrics[category] = categoryMetric
			progress.Log(item, ProgressStageWriteLog, time.Since(cacheSearchStarted), nil)
			progress.Log(item, ProgressStageDone, time.Since(cacheSearchStarted), nil)
			cancelSample()
			continue
		}
		if evaluation.judged {
			metrics.JudgedCount++
			metrics.JudgeRecords = append(metrics.JudgeRecords, evaluation.record)
			if evaluation.record.Result.CachedScore >= evaluation.record.Result.FreshScore-1 {
				cachedGEFreshMinus1++
			}
		}
		if !evaluation.badHit {
			safeHits++
			saved := item.FreshCost - item.CachedCost
			cost := BuildTokenCostAccount(item, result, evaluation, cfg.Pricing, true, embeddingMode)
			applyTokenCost(&metrics, &categoryMetric, cost)
			if result.Layer == cachepkg.LayerSemantic {
				metrics.SemanticJudgedSafe++
				categoryMetric.SemanticJudgedSafe++
			}
			metrics.SafeCostSaved += saved
			categoryMetric.SafeCostSaved += saved
			metrics.DecisionRecords = append(metrics.DecisionRecords, NewDecisionRecord(DecisionInput{
				Dataset:       cfg.Dataset,
				Item:          item,
				Result:        result,
				Evaluation:    evaluation,
				Threshold:     cfg.SemanticThreshold,
				VectorStore:   vectorStoreMode,
				EmbeddingMode: embeddingMode,
				JudgeMode:     judgeMode,
				SafeCostSaved: saved,
				Cost:          cost,
			}))
			metrics.CategoryMetrics[category] = categoryMetric
			progress.Log(item, ProgressStageWriteLog, time.Since(cacheSearchStarted), nil)
			progress.Log(item, ProgressStageDone, time.Since(cacheSearchStarted), nil)
			cancelSample()
			continue
		}

		badHits++
		categoryMetric.badHits++
		cost := BuildTokenCostAccount(item, result, evaluation, cfg.Pricing, false, embeddingMode)
		applyTokenCost(&metrics, &categoryMetric, cost)
		metrics.CategoryMetrics[category] = categoryMetric
		metrics.DecisionRecords = append(metrics.DecisionRecords, NewDecisionRecord(DecisionInput{
			Dataset:       cfg.Dataset,
			Item:          item,
			Result:        result,
			Evaluation:    evaluation,
			Threshold:     cfg.SemanticThreshold,
			VectorStore:   vectorStoreMode,
			EmbeddingMode: embeddingMode,
			JudgeMode:     judgeMode,
			BadHit:        true,
			Cost:          cost,
		}))
		metrics.BadHits = append(metrics.BadHits, BadHitSample{
			ID:            item.ID,
			Category:      caseCategory(item),
			OldRequest:    result.CachedPrompt,
			NewRequest:    cachepkg.PromptText(item.Request),
			Request:       item.Request,
			CachedAnswer:  result.Response.Text,
			FreshAnswer:   freshAnswer,
			CacheLayer:    string(result.Layer),
			Similarity:    result.Similarity,
			TeacherDelta:  evaluation.delta,
			JudgeReason:   evaluation.record.Result.Reason,
			ReplayFixture: item,
		})
		progress.Log(item, ProgressStageWriteLog, time.Since(cacheSearchStarted), nil)
		progress.Log(item, ProgressStageDone, time.Since(cacheSearchStarted), nil)
		cancelSample()
	}

	if metrics.Total > 0 {
		hits := metrics.Total - metrics.LayerContribution[string(cachepkg.LayerMiss)]
		metrics.HitRate = float64(hits) / float64(metrics.Total)
		metrics.SafeHitRate = float64(safeHits) / float64(metrics.Total)
		metrics.BadHitRate = float64(badHits) / float64(metrics.Total)
		metrics.BlendedCostPerRequest = totalCost / float64(metrics.Total)
	}
	if metrics.JudgedCount > 0 {
		metrics.CachedGEFreshMinus1Rate = float64(cachedGEFreshMinus1) / float64(metrics.JudgedCount)
	}
	if metrics.SemanticCandidates > 0 {
		metrics.AvgSemanticSimilarity = semanticSimilaritySum / float64(metrics.SemanticCandidates)
	}
	finalizeCostRates(&metrics)
	for category, metric := range metrics.CategoryMetrics {
		if metric.Total > 0 {
			metric.BadHitRate = float64(metric.badHits) / float64(metric.Total)
		}
		metrics.CategoryMetrics[category] = metric
	}
	metrics.BadHitExamplesCount = len(metrics.BadHits)
	if len(metrics.RefusedByDetail) == 0 {
		metrics.RefusedByDetail = nil
	}

	return metrics, nil
}

func RunThresholdScan(ctx context.Context, cases []Case, cfg Config, thresholds []float64, fresh FreshAnswerFunc, judge teacher.Judge, embedder embedding.Embedder) (ThresholdScan, error) {
	results := make([]Metrics, 0, len(thresholds))
	for _, threshold := range thresholds {
		scanCfg := cfg
		scanCfg.SemanticThreshold = threshold
		metrics, err := RunWithEmbedding(ctx, cases, scanCfg, fresh, judge, embedder)
		if err != nil {
			return ThresholdScan{}, err
		}
		results = append(results, metrics)
	}
	return ThresholdScan{Results: results}, nil
}

type vectorIndex struct {
	enabled bool
	vectors map[string][]float64
	store   cachepkg.VectorStore
	dim     int
}

func buildVectorIndex(ctx context.Context, cases []Case, cfg Config, embedder embedding.Embedder, vectorStoreMode string, progress *ProgressLogger) (vectorIndex, error) {
	if embedder == nil {
		return vectorIndex{}, nil
	}
	vectors := make(map[string][]float64, len(cases))
	var dim int
	replayDim := maxInt(16, len(cases)*2)
	for idx, item := range cases {
		if cfg.EmbeddingMode == "fake" {
			if seedReq, _, ok := replaySeed(item); ok {
				if dim == 0 {
					dim = replayDim
				}
				vector := make([]float64, dim)
				vector[idx%dim] = 1
				vectors[cachepkg.PromptText(item.Request)] = vector
				vectors[cachepkg.PromptText(seedReq)] = vector
				progress.Log(item, ProgressStageEmbedding, 0, nil)
				continue
			}
		}
		for _, target := range embeddingTargets(item) {
			if !eligibleForSemanticEmbedding(target.Request, target.Prompt) {
				continue
			}
			if _, ok := vectors[target.Prompt]; ok {
				continue
			}
			embedCtx, cancelEmbed := context.WithCancel(ctx)
			if cfg.TimeoutPerSample > 0 {
				embedCtx, cancelEmbed = context.WithTimeout(ctx, cfg.TimeoutPerSample)
			}
			started := time.Now()
			result, err := embedder.Embed(embedCtx, embedding.EmbedRequest{Text: target.Prompt, Model: cfg.EmbeddingModel})
			cancelEmbed()
			progress.Log(item, ProgressStageEmbedding, time.Since(started), err)
			if err != nil {
				if isTimeoutError(err, embedCtx) && cfg.ContinueOnTimeout {
					continue
				}
				return vectorIndex{}, err
			}
			if dim == 0 {
				dim = result.Dim
			}
			if result.Dim != dim {
				return vectorIndex{}, fmt.Errorf("embedding dimension mismatch for %s: got %d want %d", item.ID, result.Dim, dim)
			}
			vectors[target.Prompt] = result.Vector
		}
	}
	index := vectorIndex{enabled: true, vectors: vectors, dim: dim}
	switch vectorStoreMode {
	case "memory", "":
		return index, nil
	case "sqlite":
		store, err := cachepkg.NewSQLiteVectorStore(ctx, cfg.SQLitePath, dim)
		if err != nil {
			return vectorIndex{}, err
		}
		index.store = store
		return index, nil
	case "sqlite-int8":
		store, err := cachepkg.NewSQLiteInt8VectorStore(ctx, cfg.SQLitePath, dim)
		if err != nil {
			return vectorIndex{}, err
		}
		index.store = store
		return index, nil
	default:
		return vectorIndex{}, fmt.Errorf("unsupported vector store %q", vectorStoreMode)
	}
}

func (v *vectorIndex) insert(ctx context.Context, item Case, resp cachepkg.Response) error {
	if v == nil || v.store == nil {
		return nil
	}
	prompt := cachepkg.PromptText(item.Request)
	vector, ok := v.vectors[prompt]
	if !ok {
		return nil
	}
	return v.store.Insert(ctx, cachepkg.SemanticVectorEntry{
		Prompt:       prompt,
		Answer:       resp.Text,
		Model:        item.Request.Model,
		Category:     caseCategory(item),
		TeacherScore: resp.TeacherScore,
		Vector:       vector,
		Metadata: cachepkg.SemanticVectorMetadata{
			"id":            item.ID,
			"expected_risk": item.ExpectedRisk,
		},
	})
}

func (v *vectorIndex) insertSeed(ctx context.Context, req cachepkg.Request, resp cachepkg.Response, item Case) error {
	if v == nil || v.store == nil {
		return nil
	}
	prompt := cachepkg.PromptText(req)
	vector, ok := v.vectors[prompt]
	if !ok {
		return nil
	}
	return v.store.Insert(ctx, cachepkg.SemanticVectorEntry{
		Prompt:       prompt,
		Answer:       resp.Text,
		Model:        req.Model,
		Category:     caseCategory(item),
		TeacherScore: resp.TeacherScore,
		Vector:       vector,
		Metadata: cachepkg.SemanticVectorMetadata{
			"id":            item.ID,
			"expected_risk": item.ExpectedRisk,
			"source":        "replay_seed",
		},
	})
}

func (v *vectorIndex) semanticCandidateFunc(cfg Config) cachepkg.SemanticCandidateFunc {
	return func(ctx context.Context, req cachepkg.Request) (cachepkg.SemanticCandidate, bool, error) {
		prompt := cachepkg.PromptText(req)
		queryVector, ok := v.vectors[prompt]
		if !ok {
			return cachepkg.SemanticCandidate{}, false, nil
		}
		candidates, err := v.store.Search(ctx, queryVector, cfg.SemanticThreshold, semanticSearchK(cfg))
		if err != nil {
			return cachepkg.SemanticCandidate{}, false, err
		}
		if len(candidates) == 0 {
			return cachepkg.SemanticCandidate{}, false, nil
		}
		candidate := chooseSemanticCandidate(req, candidates, cfg.SemanticRerank)
		return cachepkg.SemanticCandidate{
			Request: cachepkg.Request{
				Model:    candidate.Entry.Model,
				Messages: []cachepkg.Message{{Role: "user", Content: candidate.Entry.Prompt}},
			},
			Response: cachepkg.Response{
				Text:         candidate.Entry.Answer,
				TeacherScore: candidate.Entry.TeacherScore,
			},
			Similarity: candidate.Similarity,
			TopK:       semanticTopKTrace(candidates, cfg.TopKTrace),
		}, true, nil
	}
}

func semanticSearchK(cfg Config) int {
	searchK := 1
	if cfg.TopKTrace > searchK {
		searchK = cfg.TopKTrace
	}
	if normalizedSemanticRerank(cfg.SemanticRerank) != "none" && searchK < 5 {
		searchK = 5
	}
	return searchK
}

func normalizedSemanticRerank(mode string) string {
	mode = strings.TrimSpace(strings.ToLower(mode))
	if mode == "" {
		return "none"
	}
	return mode
}

func chooseSemanticCandidate(req cachepkg.Request, candidates []cachepkg.SemanticVectorCandidate, rerankMode string) cachepkg.SemanticVectorCandidate {
	if len(candidates) == 0 {
		return cachepkg.SemanticVectorCandidate{}
	}
	rerankMode = normalizedSemanticRerank(rerankMode)
	if rerankMode == "none" {
		return candidates[0]
	}
	const closeSimilarityDelta = 0.003
	best := candidates[0]
	topPromptKey := normalizePromptForVariantKey(candidates[0].Entry.Prompt)
	bestAllowed := semanticCandidateSafetyDecision(req, best).Allowed
	for _, candidate := range candidates[1:] {
		if candidates[0].Similarity-candidate.Similarity > closeSimilarityDelta {
			continue
		}
		if rerankMode == "same_prompt_answer_quality" && normalizePromptForVariantKey(candidate.Entry.Prompt) != topPromptKey {
			continue
		}
		if !semanticCandidateSafetyDecision(req, candidate).Allowed {
			continue
		}
		if !bestAllowed || betterAnswerQuality(candidate, best) {
			best = candidate
			bestAllowed = true
		}
	}
	return best
}

func normalizePromptForVariantKey(prompt string) string {
	normalized := strings.ToLower(strings.TrimSpace(prompt))
	normalized = strings.Join(strings.Fields(normalized), " ")
	normalized = strings.TrimRight(normalized, " \t\r\n.!?。！？；;")
	return normalized
}

func semanticCandidateSafetyDecision(req cachepkg.Request, candidate cachepkg.SemanticVectorCandidate) safety.Decision {
	checker := safety.DefaultChecker()
	return checker.Evaluate(safety.Input{
		Prompt:             cachepkg.PromptText(req),
		CachedPrompt:       candidate.Entry.Prompt,
		HasTools:           len(req.Tools) > 0,
		CachedTeacherScore: candidate.Entry.TeacherScore,
	})
}

func betterAnswerQuality(candidate cachepkg.SemanticVectorCandidate, current cachepkg.SemanticVectorCandidate) bool {
	candidateScore := candidate.Entry.TeacherScore
	currentScore := current.Entry.TeacherScore
	if candidateScore > 0 && currentScore > 0 && candidateScore != currentScore {
		return candidateScore > currentScore
	}
	candidateLenScore := answerLengthQualityScore(candidate.Entry.Answer)
	currentLenScore := answerLengthQualityScore(current.Entry.Answer)
	if candidateLenScore != currentLenScore {
		return candidateLenScore > currentLenScore
	}
	return false
}

func answerLengthQualityScore(answer string) int {
	length := len([]rune(strings.TrimSpace(answer)))
	if length < 80 {
		return length - 80
	}
	if length > 2000 {
		return 2000
	}
	return length
}

func semanticTopKTrace(candidates []cachepkg.SemanticVectorCandidate, limit int) []cachepkg.SemanticTopKCandidate {
	if limit <= 0 || len(candidates) == 0 {
		return nil
	}
	if len(candidates) < limit {
		limit = len(candidates)
	}
	out := make([]cachepkg.SemanticTopKCandidate, 0, limit)
	for i := 0; i < limit; i++ {
		candidate := candidates[i]
		out = append(out, cachepkg.SemanticTopKCandidate{
			Rank:                   i + 1,
			Similarity:             candidate.Similarity,
			CandidatePromptPreview: Preview(candidate.Entry.Prompt, 200),
			CandidateAnswerPreview: Preview(candidate.Entry.Answer, 200),
			AnswerLength:           len([]rune(strings.TrimSpace(candidate.Entry.Answer))),
			TeacherScore:           candidate.Entry.TeacherScore,
			CandidateID:            candidate.Entry.Metadata["id"],
		})
	}
	return out
}

func (v *vectorIndex) close() error {
	if v == nil || v.store == nil {
		return nil
	}
	return v.store.Close()
}

func eligibleForSemanticEmbedding(req cachepkg.Request, prompt string) bool {
	if len([]rune(prompt)) > 512 {
		return false
	}
	if len(req.Tools) > 0 {
		return false
	}
	if len(req.Extra) > 0 {
		for _, key := range []string{"function_call", "tool_choice"} {
			if value, ok := req.Extra[key]; ok && value != nil {
				return false
			}
		}
	}
	return strings.TrimSpace(prompt) != ""
}

type embeddingTarget struct {
	Request cachepkg.Request
	Prompt  string
}

func embeddingTargets(item Case) []embeddingTarget {
	targets := []embeddingTarget{{
		Request: item.Request,
		Prompt:  cachepkg.PromptText(item.Request),
	}}
	if seedReq, _, ok := replaySeed(item); ok {
		targets = append(targets, embeddingTarget{
			Request: seedReq,
			Prompt:  cachepkg.PromptText(seedReq),
		})
	}
	return targets
}

func replaySeed(item Case) (cachepkg.Request, cachepkg.Response, bool) {
	oldPrompt := strings.TrimSpace(item.OldRequest)
	oldAnswer := strings.TrimSpace(item.OldAnswer)
	if oldPrompt == "" || oldAnswer == "" {
		return cachepkg.Request{}, cachepkg.Response{}, false
	}
	model := strings.TrimSpace(item.Request.Model)
	if model == "" {
		model = "benchmark-provider-model"
	}
	req := cachepkg.Request{
		Model:    model,
		Messages: []cachepkg.Message{{Role: "user", Content: oldPrompt}},
	}
	if item.Request.Temperature != nil {
		req.Temperature = item.Request.Temperature
	}
	if item.Request.MaxTokens != nil {
		req.MaxTokens = item.Request.MaxTokens
	}
	resp := cachepkg.Response{
		Text:         oldAnswer,
		TeacherScore: item.TeacherScore,
	}
	return req, resp, true
}

type hitEvaluation struct {
	badHit bool
	delta  float64
	judged bool
	record teacher.Record
}

func evaluateHit(ctx context.Context, item Case, result cachepkg.LookupResult, freshAnswer string, judge teacher.Judge) (hitEvaluation, bool) {
	if usesReplayExpectation(item, judge) {
		cacheSafe := true
		if item.SourceJudgeCacheSafe != nil {
			cacheSafe = *item.SourceJudgeCacheSafe
		}
		if item.ExpectedBadHit {
			cacheSafe = false
		}
		judgeResult := teacher.JudgeResult{
			CacheSafe:   cacheSafe,
			CachedScore: 8,
			FreshScore:  9,
			Delta:       boolDelta(!cacheSafe),
			BadHit:      !cacheSafe,
			Reason:      "replay dataset expectation",
		}
		return hitEvaluation{
			badHit: !cacheSafe,
			delta:  judgeResult.Delta,
			judged: true,
			record: teacher.Record{
				ID: item.ID,
				Input: teacher.JudgeInput{
					OldRequest:   result.CachedPrompt,
					CachedAnswer: result.Response.Text,
					NewRequest:   cachepkg.PromptText(item.Request),
					FreshAnswer:  freshAnswer,
					Category:     caseCategory(item),
				},
				Result: judgeResult,
			},
		}, true
	}
	if judge == nil {
		badHit := result.Response.Text != freshAnswer
		return hitEvaluation{badHit: badHit, delta: boolDelta(badHit)}, true
	}
	input := teacher.JudgeInput{
		OldRequest:   result.CachedPrompt,
		CachedAnswer: result.Response.Text,
		NewRequest:   cachepkg.PromptText(item.Request),
		FreshAnswer:  freshAnswer,
		Category:     caseCategory(item),
	}
	judgeResult, err := judge.Score(ctx, input)
	if err != nil && isTimeoutError(err, ctx) {
		judgeResult, err = judge.Score(ctx, input)
	}
	record := teacher.Record{ID: item.ID, Input: input, Result: judgeResult}
	if err != nil {
		record.Error = err.Error()
		return hitEvaluation{record: record, judged: true}, false
	}
	return hitEvaluation{
		badHit: judgeResult.BadHit,
		delta:  judgeResult.Delta,
		judged: true,
		record: record,
	}, true
}

func usesReplayExpectation(item Case, judge teacher.Judge) bool {
	if strings.TrimSpace(item.ExpectedCacheBehavior) == "" {
		return false
	}
	if strings.TrimSpace(item.SourceRunID) == "" && strings.TrimSpace(item.PairGenerationMode) == "" {
		return false
	}
	_, ok := judge.(teacher.FakeJudge)
	return ok
}

func boolDelta(badHit bool) float64 {
	if badHit {
		return 1
	}
	return 0
}

func maxInt(left int, right int) int {
	if left > right {
		return left
	}
	return right
}

func fixtureFreshAnswer(ctx context.Context, item Case) (cachepkg.Response, error) {
	return cachepkg.Response{Text: item.FreshAnswer, TeacherScore: item.TeacherScore}, nil
}

func MarshalTeacherScoreJSONL(record TeacherScoreRecord) ([]byte, error) {
	payload, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	return append(payload, '\n'), nil
}

func ReplayBadHit(sample BadHitSample) Case {
	return sample.ReplayFixture
}

func WriteBadHitRegression(path string, sample BadHitSample) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	payload, err := json.Marshal(ReplayBadHit(sample))
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	return os.WriteFile(path, payload, 0o644)
}

func LoadRegressionSample(path string) (Case, error) {
	cases, err := LoadJSONL(path)
	if err != nil {
		return Case{}, err
	}
	if len(cases) == 0 {
		return Case{}, fmt.Errorf("regression sample is empty: %s", path)
	}
	return cases[0], nil
}

func WriteBadHitExamples(dir string, threshold float64, samples []BadHitSample) error {
	if len(samples) == 0 {
		return nil
	}
	if dir == "" {
		dir = filepath.Join("testdata", "regression", "bad_hits")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, fmt.Sprintf("threshold_%.2f.jsonl", threshold))
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	encoder := json.NewEncoder(file)
	for _, sample := range samples {
		if err := encoder.Encode(sample); err != nil {
			return err
		}
	}
	return nil
}

func LoadBadHitExamples(path string) ([]BadHitSample, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	var samples []BadHitSample
	line := 0
	for scanner.Scan() {
		line++
		if strings.TrimSpace(scanner.Text()) == "" {
			continue
		}
		var sample BadHitSample
		if err := json.Unmarshal(scanner.Bytes(), &sample); err != nil {
			return nil, fmt.Errorf("decode bad hit jsonl line %d: %w", line, err)
		}
		samples = append(samples, sample)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return samples, nil
}

func WriteJudgeOutput(path string, records []teacher.Record) error {
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

func normalizeCase(item *Case) {
	if item.Category == "" {
		item.Category = item.Kind
	}
	if item.Kind == "" {
		item.Kind = item.Category
	}
	if item.FreshCost == 0 && item.EstimatedUpstreamCost > 0 {
		item.FreshCost = item.EstimatedUpstreamCost
	}
	if item.EstimatedUpstreamCost == 0 && item.FreshCost > 0 {
		item.EstimatedUpstreamCost = item.FreshCost
	}
}

func caseCategory(item Case) string {
	if item.Category != "" {
		return item.Category
	}
	return item.Kind
}

func normalizedCategory(category string) string {
	category = strings.TrimSpace(category)
	if category == "" {
		return "uncategorized"
	}
	return category
}

func sanitizedSQLitePath(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	return filepath.Base(path)
}
